//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"
)

type memoRow struct {
	entry       *widget.Entry
	del         *widget.Button
	view        fyne.CanvasObject
	dragOverlay *canvas.Rectangle
	dragging    bool
}

var (
	rows           []*memoRow
	listBox        *fyne.Container
	windowHandle   atomic.Uintptr
	hotkeyThreadID atomic.Uint32
	mainWindow     fyne.Window

	loadingMemos          bool
	persistenceDisabled   bool
	persistenceErrorShown bool
)

const (
	MOD_ALT         = 0x0001
	HOTKEY_ID       = 1
	VK_Z            = 0x5A
	VK_MENU         = 0x12
	VK_CONTROL      = 0x11
	VK_C            = 0x43
	WM_HOTKEY       = 0x0312
	WM_QUIT         = 0x0012
	CF_UNICODETEXT  = 13
	KEYEVENTF_KEYUP = 0x0002
)

func main() {
	a := app.New()
	w := a.NewWindow("TRPG Memo")
	mainWindow = w
	memos, err := loadMemos()
	if err != nil {
		persistenceDisabled = true
	}
	w.Resize(fyne.NewSize(300, 420))
	w.SetPadded(false)
	w.SetContent(buildUI(w))
	w.Show()
	w.SetOnClosed(stopHotkeyListener)

	if err := registerAltZHotkey(); err != nil {
		reportHotkeyError(err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		applyWindowsStyle(w)
	}()

	loadingMemos = true
	for _, memo := range memos {
		appendRow(memo)
	}
	loadingMemos = false
	if err != nil {
		reportPersistenceError(fmt.Errorf("保存済みメモを読み込めませんでした: %w", err))
	}
	if len(memos) == 0 {
		appendRow("")
	}
	a.Run()
}

func buildUI(w fyne.Window) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.NRGBA{R: 255, G: 244, B: 190, A: 255})
	bg.SetMinSize(fyne.NewSize(220, 200))

	headerBg := canvas.NewRectangle(color.NRGBA{R: 214, G: 182, B: 92, A: 255})
	headerBg.SetMinSize(fyne.NewSize(220, 36))

	title := canvas.NewText("TRPG Memo", color.NRGBA{R: 32, G: 28, B: 14, A: 255})
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	title.TextSize = 14
	closeButton := widget.NewButton("×", w.Close)
	closeButton.Importance = widget.LowImportance
	headerContent := container.NewBorder(nil, nil, nil, closeButton, container.NewStack(title, newDragHandle()))
	header := container.NewStack(headerBg, headerContent)

	listBox = container.NewVBox()
	scroll := container.NewScroll(listBox)
	scroll.SetMinSize(fyne.NewSize(220, 140))

	addBtn := widget.NewButton("+", func() {
		appendRow("")
	})
	addBtn.Importance = widget.MediumImportance

	content := container.NewBorder(header, addBtn, nil, nil, scroll)
	return container.NewStack(bg, content)
}

type dragHandle struct {
	widget.BaseWidget
}

func newDragHandle() *dragHandle {
	handle := &dragHandle{}
	handle.ExtendBaseWidget(handle)
	return handle
}

func (h *dragHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (h *dragHandle) MouseDown(event *desktop.MouseEvent) {
	if event.Button != desktop.MouseButtonPrimary {
		return
	}

	hwnd := windowHandle.Load()
	if hwnd == 0 {
		return
	}

	if result, _, err := releaseCapture.Call(); result == 0 {
		log.Printf("ReleaseCapture failed: %v", err)
	}
	sendMessageW.Call(hwnd, uintptr(WM_NCLBUTTONDOWN), uintptr(HTCAPTION), 0)
}

func (h *dragHandle) MouseUp(*desktop.MouseEvent) {
}

func appendRow(raw string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = ""
	}

	entry := widget.NewEntry()
	entry.SetText(trimmed)
	entry.SetPlaceHolder("固有名詞")
	entry.OnChanged = func(string) {
		saveMemos()
	}

	del := widget.NewButton("×", func() {
		removeRow(entry)
	})
	del.Importance = widget.DangerImportance

	row := &memoRow{entry: entry, del: del}
	row.dragOverlay = canvas.NewRectangle(color.NRGBA{R: 255, G: 216, B: 112, A: 105})
	row.dragOverlay.StrokeColor = color.NRGBA{R: 174, G: 111, B: 27, A: 235}
	row.dragOverlay.StrokeWidth = 2
	row.dragOverlay.CornerRadius = 4
	row.dragOverlay.Hide()
	rowContent := container.NewBorder(nil, nil, newReorderHandle(row), row.del, row.entry)
	row.view = container.NewStack(rowContent, row.dragOverlay)
	rows = append(rows, row)
	listBox.Add(row.view)
	listBox.Refresh()
	saveMemos()
}

type reorderHandle struct {
	widget.BaseWidget
	row        *memoRow
	offset     float32
	grip       *canvas.Text
	background *canvas.Rectangle
}

func newReorderHandle(row *memoRow) *reorderHandle {
	handle := &reorderHandle{row: row}
	handle.ExtendBaseWidget(handle)
	return handle
}

func (h *reorderHandle) CreateRenderer() fyne.WidgetRenderer {
	h.background = canvas.NewRectangle(color.Transparent)
	h.grip = canvas.NewText("≡", color.NRGBA{R: 92, G: 76, B: 38, A: 255})
	h.grip.Alignment = fyne.TextAlignCenter
	h.grip.TextSize = 18
	return widget.NewSimpleRenderer(container.NewStack(h.background, h.grip))
}

func (h *reorderHandle) Dragged(event *fyne.DragEvent) {
	if !h.row.dragging {
		h.row.dragging = true
		h.row.dragOverlay.FillColor = color.NRGBA{R: 255, G: 153, B: 36, A: 155}
		h.row.dragOverlay.StrokeColor = color.NRGBA{R: 145, G: 72, B: 0, A: 255}
		h.row.dragOverlay.StrokeWidth = 3
		h.row.dragOverlay.Show()
		h.row.dragOverlay.Refresh()
		h.background.FillColor = color.NRGBA{R: 214, G: 105, B: 0, A: 255}
		h.background.Refresh()
		h.grip.Color = color.White
		h.grip.TextStyle.Bold = true
		h.grip.Refresh()
	}

	h.offset += event.Dragged.DY
	threshold := h.row.view.Size().Height
	if threshold <= 0 {
		threshold = 32
	}

	for h.offset >= threshold {
		if !moveMemoRow(h.row, 1) {
			h.offset = 0
			return
		}
		h.offset -= threshold
	}
	for h.offset <= -threshold {
		if !moveMemoRow(h.row, -1) {
			h.offset = 0
			return
		}
		h.offset += threshold
	}
}

func (h *reorderHandle) DragEnd() {
	h.offset = 0
	h.row.dragging = false
	h.row.dragOverlay.Hide()
	h.row.dragOverlay.Refresh()
	h.background.FillColor = color.Transparent
	h.background.Refresh()
	h.grip.Color = color.NRGBA{R: 92, G: 76, B: 38, A: 255}
	h.grip.TextStyle.Bold = false
	h.grip.Refresh()
}

func moveMemoRow(row *memoRow, direction int) bool {
	from := -1
	for i, candidate := range rows {
		if candidate == row {
			from = i
			break
		}
	}
	updated, ok := reorderMemoRows(rows, from, from+direction)
	if !ok {
		return false
	}

	rows = updated
	refreshMemoRows()
	saveMemos()
	return true
}

func reorderMemoRows(current []*memoRow, from, to int) ([]*memoRow, bool) {
	if from < 0 || from >= len(current) || to < 0 || to >= len(current) {
		return current, false
	}

	updated := append([]*memoRow(nil), current...)
	row := updated[from]
	if from < to {
		copy(updated[from:to], updated[from+1:to+1])
	} else if from > to {
		copy(updated[to+1:from+1], updated[to:from])
	}
	updated[to] = row
	return updated, from != to
}

func refreshMemoRows() {
	listBox.RemoveAll()
	for _, row := range rows {
		listBox.Add(row.view)
	}
	listBox.Refresh()
}

func removeRow(target *widget.Entry) {
	newRows := make([]*memoRow, 0, len(rows))
	for _, r := range rows {
		if r.entry != target {
			newRows = append(newRows, r)
		}
	}
	rows = newRows

	refreshMemoRows()
	saveMemos()
}

func memoFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ユーザー設定フォルダーを取得できません: %w", err)
	}
	return filepath.Join(configDir, "trpg-memo-app", "memos.json"), nil
}

func loadMemos() ([]string, error) {
	path, err := memoFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を読み込めません: %w", path, err)
	}

	var memos []string
	if err := json.Unmarshal(data, &memos); err != nil {
		return nil, fmt.Errorf("%s の形式が不正です: %w", path, err)
	}
	if memos == nil {
		return nil, fmt.Errorf("%s の形式が不正です: メモ一覧は JSON 配列である必要があります", path)
	}
	return memos, nil
}

func saveMemos() {
	if loadingMemos || persistenceDisabled {
		return
	}

	memos := make([]string, 0, len(rows))
	for _, row := range rows {
		memos = append(memos, row.entry.Text)
	}
	if err := writeMemos(memos); err != nil {
		disablePersistence(err)
	}
}

func writeMemos(memos []string) error {
	path, err := memoFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("%s を作成できません: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(memos, "", "  ")
	if err != nil {
		return fmt.Errorf("メモを JSON に変換できません: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".memos-*.tmp")
	if err != nil {
		return fmt.Errorf("%s に一時ファイルを作成できません: %w", filepath.Dir(path), err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("%s に書き込めません: %w", tempPath, err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("%s を同期できません: %w", tempPath, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("%s を閉じられません: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("%s を更新できません: %w", path, err)
	}
	return nil
}

func disablePersistence(err error) {
	persistenceDisabled = true
	reportPersistenceError(fmt.Errorf("メモの保存を停止しました: %w", err))
}

func reportPersistenceError(err error) {
	log.Printf("Memo persistence error: %v", err)
	if persistenceErrorShown {
		return
	}
	persistenceErrorShown = true
	dialog.ShowError(err, mainWindow)
}

// ---------------------------------------------------------
// グローバルホットキー: Alt+Z
// 自動で Ctrl+C を送って、クリップボードをメモへ追加
// ---------------------------------------------------------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	registerHotKey             = user32.NewProc("RegisterHotKey")
	unregisterHotKey           = user32.NewProc("UnregisterHotKey")
	getMessageW                = user32.NewProc("GetMessageW")
	translateMessage           = user32.NewProc("TranslateMessage")
	dispatchMessageW           = user32.NewProc("DispatchMessageW")
	keybdEvent                 = user32.NewProc("keybd_event")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	getAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	getClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	getWindowLongW             = user32.NewProc("GetWindowLongPtrW")
	setWindowLongW             = user32.NewProc("SetWindowLongPtrW")
	setWindowPos               = user32.NewProc("SetWindowPos")
	releaseCapture             = user32.NewProc("ReleaseCapture")
	sendMessageW               = user32.NewProc("SendMessageW")
	postThreadMessageW         = user32.NewProc("PostThreadMessageW")
	getCurrentThreadID         = kernel32.NewProc("GetCurrentThreadId")
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

func registerAltZHotkey() error {
	registered := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer hotkeyThreadID.Store(0)

		threadID, _, err := getCurrentThreadID.Call()
		if threadID == 0 {
			registered <- fmt.Errorf("GetCurrentThreadId に失敗しました: %v", err)
			return
		}
		hotkeyThreadID.Store(uint32(threadID))
		ret, _, err := registerHotKey.Call(0, HOTKEY_ID, MOD_ALT, VK_Z)
		if ret == 0 {
			registered <- fmt.Errorf("RegisterHotKey (Alt+Z) に失敗しました: %v", err)
			return
		}
		registered <- nil
		defer unregisterHotKey.Call(0, HOTKEY_ID)

		for {
			var m msg
			ret, _, err := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if ret == 0 {
				return
			}
			if ret == ^uintptr(0) {
				reportHotkeyError(fmt.Errorf("GetMessageW に失敗しました: %v", err))
				return
			}
			if m.message == WM_HOTKEY && m.wParam == HOTKEY_ID {
				handleAltZ()
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	return <-registered
}

func stopHotkeyListener() {
	threadID := hotkeyThreadID.Load()
	if threadID == 0 {
		return
	}
	if result, _, err := postThreadMessageW.Call(uintptr(threadID), WM_QUIT, 0, 0); result == 0 {
		log.Printf("PostThreadMessageW failed: %v", err)
	}
}

func reportHotkeyError(err error) {
	log.Printf("Alt+Z shortcut error: %v", err)
	fyne.Do(func() {
		dialog.ShowError(err, mainWindow)
	})
}

func handleAltZ() {
	go func() {
		if !waitForAltRelease() {
			return
		}
		sequence, _, _ := getClipboardSequenceNumber.Call()
		simulateCtrlC()
		if !waitForClipboardChange(sequence) {
			return
		}
		text := readClipboardText()
		if strings.TrimSpace(text) == "" {
			return
		}
		fyne.Do(func() {
			appendRow(text)
		})
	}()
}

func waitForAltRelease() bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		altState, _, _ := getAsyncKeyState.Call(uintptr(VK_MENU))
		zState, _, _ := getAsyncKeyState.Call(uintptr(VK_Z))
		if altState&0x8000 == 0 && zState&0x8000 == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	log.Printf("Alt+Z shortcut cancelled: Alt or Z remained pressed")
	return false
}

func waitForClipboardChange(previousSequence uintptr) bool {
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		sequence, _, _ := getClipboardSequenceNumber.Call()
		if sequence != previousSequence {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func simulateCtrlC() {
	keybdEvent.Call(uintptr(VK_CONTROL), 0, 0, 0)
	keybdEvent.Call(uintptr(VK_C), 0, 0, 0)
	keybdEvent.Call(uintptr(VK_C), 0, KEYEVENTF_KEYUP, 0)
	keybdEvent.Call(uintptr(VK_CONTROL), 0, KEYEVENTF_KEYUP, 0)
}

func readClipboardText() string {
	if ret, _, _ := openClipboard.Call(0); ret == 0 {
		return ""
	}
	defer closeClipboard.Call(0)

	handle, _, _ := getClipboardData.Call(CF_UNICODETEXT)
	if handle == 0 {
		return ""
	}

	ptr, _, _ := globalLock.Call(handle)
	if ptr == 0 {
		return ""
	}
	defer globalUnlock.Call(handle)

	return strings.TrimSpace(windows.UTF16PtrToString((*uint16)(unsafe.Pointer(ptr))))
}

// ---------------------------------------------------------
// Windows のウィンドウスタイル設定
// ---------------------------------------------------------

const (
	WS_CAPTION       = 0x00C00000
	WS_THICKFRAME    = 0x00040000
	WS_MINIMIZEBOX   = 0x00020000
	WS_MAXIMIZEBOX   = 0x00010000
	WS_SYSMENU       = 0x00080000
	GWL_STYLE        = ^uintptr(15)
	HWND_TOPMOST     = ^uintptr(0)
	SWP_NOMOVE       = 0x0002
	SWP_NOSIZE       = 0x0001
	SWP_NOACTIVATE   = 0x0010
	SWP_SHOWWINDOW   = 0x0040
	SWP_FRAMECHANGED = 0x0020
	WM_NCLBUTTONDOWN = 0x00A1
	HTCAPTION        = 2
)

func applyWindowsStyle(w fyne.Window) {
	hwnd := findWindowByTitle(w.Title())
	if hwnd == 0 {
		return
	}
	windowHandle.Store(uintptr(hwnd))

	style, _, _ := getWindowLongW.Call(uintptr(hwnd), uintptr(GWL_STYLE))
	newStyle := style &^ uintptr(WS_CAPTION|WS_MINIMIZEBOX|WS_MAXIMIZEBOX|WS_SYSMENU)
	setWindowLongW.Call(uintptr(hwnd), uintptr(GWL_STYLE), newStyle)

	setWindowPos.Call(
		uintptr(hwnd),
		uintptr(HWND_TOPMOST),
		0,
		0,
		0,
		0,
		uintptr(SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW|SWP_FRAMECHANGED),
	)
}

func findWindowByTitle(title string) uintptr {
	titleUTF16, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(titleUTF16)))
	return ret
}
