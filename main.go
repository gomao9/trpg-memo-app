//go:build windows

package main

import (
	"image/color"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"
)

type memoRow struct {
	entry *widget.Entry
	del   *widget.Button
}

var (
	rows    []*memoRow
	listBox *fyne.Container
)

const (
	MOD_ALT = 0x0001
	HOTKEY_ID = 1
	VK_Z = 0x5A
	VK_CONTROL = 0x11
	VK_C = 0x43
	WM_HOTKEY = 0x0312
	CF_UNICODETEXT = 13
	KEYEVENTF_KEYUP = 0x0002
)

func main() {
	a := app.New()
	w := a.NewWindow("TRPG Memo")
	w.SetFixedSize(true)
	w.Resize(fyne.NewSize(300, 420))
	w.SetPadded(false)
	w.SetContent(buildUI())
	w.Show()

	if err := registerAltZHotkey(); err != nil {
		_ = err
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		applyWindowsStyle(w)
	}()

	appendRow("")
	a.Run()
}

func buildUI() fyne.CanvasObject {
	bg := canvas.NewRectangle(color.NRGBA{R: 255, G: 244, B: 190, A: 255})
	bg.SetMinSize(fyne.NewSize(300, 420))

	headerBg := canvas.NewRectangle(color.NRGBA{R: 214, G: 182, B: 92, A: 255})
	headerBg.SetMinSize(fyne.NewSize(300, 36))

	title := canvas.NewText("TRPG Memo", color.NRGBA{R: 32, G: 28, B: 14, A: 255})
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	title.TextSize = 14
	header := container.NewStack(headerBg, title)

	listBox = container.NewVBox()
	scroll := container.NewScroll(listBox)
	scroll.SetMinSize(fyne.NewSize(280, 300))

	addBtn := widget.NewButton("+", func() {
		appendRow("")
	})
	addBtn.Importance = widget.MediumImportance

	content := container.NewBorder(header, addBtn, nil, nil, scroll)
	return container.NewStack(bg, content)
}

func appendRow(raw string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = ""
	}

	entry := widget.NewEntry()
	entry.SetText(trimmed)
	entry.SetPlaceHolder("固有名詞")

	del := widget.NewButton("×", func() {
		removeRow(entry)
	})
	del.Importance = widget.DangerImportance

	row := &memoRow{entry: entry, del: del}
	rows = append(rows, row)
	listBox.Add(container.NewHBox(entry, del))
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

	listBox.RemoveAll()
	for _, r := range rows {
		listBox.Add(container.NewHBox(r.entry, r.del))
	}
	listBox.Refresh()
}

// ---------------------------------------------------------
// グローバルホットキー: Alt+Z
// 自動で Ctrl+C を送って、クリップボードをメモへ追加
// ---------------------------------------------------------

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	registerHotKey = user32.NewProc("RegisterHotKey")
	unregisterHotKey = user32.NewProc("UnregisterHotKey")
	getMessageW = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessageW = user32.NewProc("DispatchMessageW")
	keybdEvent = user32.NewProc("keybd_event")
	openClipboard = user32.NewProc("OpenClipboard")
	closeClipboard = user32.NewProc("CloseClipboard")
	getClipboardData = user32.NewProc("GetClipboardData")
	globalLock = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	getWindowLongW = user32.NewProc("GetWindowLongPtrW")
	setWindowLongW = user32.NewProc("SetWindowLongPtrW")
	setWindowPos = user32.NewProc("SetWindowPos")
)

type msg struct {
	hwnd   uintptr
	message uint32
	wParam uintptr
	lParam uintptr
	time   uint32
	pt     struct { x, y int32 }
}

func registerAltZHotkey() error {
	ret, _, err := registerHotKey.Call(0, HOTKEY_ID, MOD_ALT, VK_Z)
	if ret == 0 {
		return err
	}

	go func() {
		for {
			var m msg
			ret, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if ret == 0 {
				break
			}
			if m.message == WM_HOTKEY && m.wParam == HOTKEY_ID {
				handleAltZ()
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	return nil
}

func handleAltZ() {
	go func() {
		simulateCtrlC()
		time.Sleep(80 * time.Millisecond)
		text := readClipboardText()
		if strings.TrimSpace(text) == "" {
			return
		}
		appendRow(text)
	}()
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
	WS_CAPTION     = 0x00C00000
	WS_THICKFRAME  = 0x00040000
	WS_MINIMIZEBOX = 0x00020000
	WS_MAXIMIZEBOX = 0x00010000
	WS_SYSMENU     = 0x00080000
	GWL_STYLE      = -16
	HWND_TOPMOST   = -1
	SWP_NOMOVE     = 0x0002
	SWP_NOSIZE     = 0x0001
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040
)

func applyWindowsStyle(w fyne.Window) {
	hwnd := findWindowByTitle(w.Title())
	if hwnd == 0 {
		return
	}

	style, _, _ := getWindowLongW.Call(uintptr(hwnd), uintptr(GWL_STYLE))
	newStyle := style &^ uintptr(WS_CAPTION|WS_THICKFRAME|WS_MINIMIZEBOX|WS_MAXIMIZEBOX|WS_SYSMENU)
	setWindowLongW.Call(uintptr(hwnd), uintptr(GWL_STYLE), newStyle)

	setWindowPos.Call(
		uintptr(hwnd),
		uintptr(HWND_TOPMOST),
		0,
		0,
		0,
		0,
		uintptr(SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW),
	)
}

func findWindowByTitle(title string) uintptr {
	titleUTF16, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(titleUTF16)))
	return ret
}
