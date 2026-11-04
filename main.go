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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/sys/windows"
)

type memoRow struct {
	entry        *widget.Entry
	del          *widget.Button
	view         fyne.CanvasObject
	handle       *reorderHandle
	dragOverlay  *canvas.Rectangle
	detail       string
	detailPopup  *widget.PopUp
	detailEditor fyne.Window
	dragging     bool
}

type memoData struct {
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
}

type memoSet struct {
	Name  string     `json:"name"`
	Memos []memoData `json:"memos"`
}

type storedMemoFile struct {
	Version   int       `json:"version"`
	ActiveSet string    `json:"activeSet"`
	Sets      []memoSet `json:"sets"`
}

var (
	rows           []*memoRow
	listBox        *fyne.Container
	setSelector    *widget.Select
	memoSets       []memoSet
	activeSetName  string
	windowHandle   atomic.Uintptr
	hotkeyThreadID atomic.Uint32
	mainWindow     fyne.Window

	loadingMemos          bool
	updatingSetSelector   bool
	persistenceDisabled   bool
	persistenceErrorShown bool
)

const defaultMemoSetName = "単語セット 1"

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
	loadedSets, selectedSet, err := loadMemoSets()
	if err != nil {
		persistenceDisabled = true
		loadedSets = []memoSet{{Name: defaultMemoSetName, Memos: []memoData{}}}
		selectedSet = defaultMemoSetName
	}
	if len(loadedSets) == 0 {
		loadedSets = []memoSet{{Name: defaultMemoSetName, Memos: []memoData{}}}
		selectedSet = defaultMemoSetName
	}
	memoSets = loadedSets
	activeSetName = selectedSet
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

	displayMemoSet(selectedSetMemos())
	if err != nil {
		reportPersistenceError(fmt.Errorf("保存済みメモを読み込めませんでした: %w", err))
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
	helpButton := widget.NewButton("?", func() {
		dialog.ShowInformation("簡易ヘルプ", "Alt+Z: 選択中の文字列をコピーして単語に追加\n「+」: 空欄を追加", w)
	})
	helpButton.Importance = widget.LowImportance
	closeButton := widget.NewButton("×", w.Close)
	closeButton.Importance = widget.LowImportance
	headerActions := container.NewHBox(helpButton, closeButton)
	headerContent := container.NewBorder(nil, nil, nil, headerActions, container.NewStack(title, newDragHandle()))
	header := container.NewStack(headerBg, headerContent)

	listBox = container.NewVBox()
	scroll := container.NewScroll(listBox)
	scroll.SetMinSize(fyne.NewSize(220, 140))

	addBtn := widget.NewButton("+", func() {
		appendRow("")
	})
	addBtn.Importance = widget.MediumImportance

	setSelector = widget.NewSelect(memoSetNames(), func(name string) {
		if updatingSetSelector {
			return
		}
		if err := switchMemoSet(name); err != nil {
			reportPersistenceError(err)
			refreshSetSelector()
		}
	})
	updatingSetSelector = true
	setSelector.SetSelected(activeSetName)
	updatingSetSelector = false
	addSetBtn := widget.NewButton("+セット", func() {
		nameDialog := dialog.NewEntryDialog("単語セットを追加", "セット名を入力してください", func(name string) {
			if err := createMemoSet(name); err != nil {
				dialog.ShowError(err, w)
			}
		}, w)
		nameDialog.Resize(fyne.NewSize(440, 160))
		nameDialog.Show()
	})
	renameSetBtn := widget.NewButton("名前変更", func() {
		nameDialog := dialog.NewEntryDialog("単語セット名を変更", "新しいセット名を入力してください", func(name string) {
			if err := renameMemoSet(name); err != nil {
				dialog.ShowError(err, w)
			}
		}, w)
		nameDialog.SetText(activeSetName)
		nameDialog.Resize(fyne.NewSize(440, 160))
		nameDialog.Show()
	})
	setActions := container.NewHBox(renameSetBtn, addSetBtn)
	setBar := container.NewBorder(nil, nil, nil, setActions, setSelector)
	body := container.NewBorder(setBar, nil, nil, nil, scroll)
	content := container.NewBorder(header, addBtn, nil, nil, body)
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
	appendMemo(memoData{Text: raw})
}

func appendMemo(memo memoData) {
	memo.Text = strings.TrimSpace(memo.Text)
	entry := widget.NewEntry()
	entry.Enable()
	entry.SetText(memo.Text)
	entry.SetPlaceHolder("固有名詞")
	entry.OnChanged = func(string) {
		saveMemos()
	}

	del := widget.NewButton("×", func() {
		removeRow(entry)
	})
	del.Importance = widget.DangerImportance

	row := &memoRow{entry: entry, del: del, detail: memo.Detail}
	row.dragOverlay = canvas.NewRectangle(color.NRGBA{R: 255, G: 216, B: 112, A: 105})
	row.dragOverlay.StrokeColor = color.NRGBA{R: 174, G: 111, B: 27, A: 235}
	row.dragOverlay.StrokeWidth = 2
	row.dragOverlay.CornerRadius = 4
	row.dragOverlay.Hide()
	row.handle = newReorderHandle(row)
	rowContent := container.NewBorder(nil, nil, row.handle, row.del, &memoEntry{Entry: entry, row: row})
	row.view = container.NewStack(rowContent, row.dragOverlay)
	rows = append(rows, row)
	listBox.Add(row.view)
	listBox.Refresh()
	saveMemos()
}

func memoSetNames() []string {
	names := make([]string, 0, len(memoSets))
	for _, set := range memoSets {
		names = append(names, set.Name)
	}
	return names
}

func refreshSetSelector() {
	if setSelector == nil {
		return
	}
	updatingSetSelector = true
	setSelector.Options = memoSetNames()
	setSelector.SetSelected(activeSetName)
	setSelector.Refresh()
	updatingSetSelector = false
}

func selectedSetMemos() []memoData {
	for _, set := range memoSets {
		if set.Name == activeSetName {
			return set.Memos
		}
	}
	return nil
}

func displayMemoSet(memos []memoData) {
	loadingMemos = true
	rows = nil
	listBox.RemoveAll()
	for _, memo := range memos {
		appendMemo(memo)
	}
	if len(memos) == 0 {
		appendMemo(memoData{})
	}
	loadingMemos = false
	listBox.Refresh()
	saveMemos()
}

func switchMemoSet(name string) error {
	if name == activeSetName {
		return nil
	}
	found := false
	for _, set := range memoSets {
		if set.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("単語セット %q が見つかりません", name)
	}
	if err := persistMemos(); err != nil {
		disablePersistence(err)
		return err
	}
	activeSetName = name
	refreshSetSelector()
	displayMemoSet(selectedSetMemos())
	return nil
}

func createMemoSet(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("セット名を入力してください")
	}
	for _, set := range memoSets {
		if strings.EqualFold(set.Name, name) {
			return fmt.Errorf("単語セット %q はすでに存在します", name)
		}
	}
	if err := persistMemos(); err != nil {
		disablePersistence(err)
		return err
	}
	memoSets = append(memoSets, memoSet{Name: name, Memos: []memoData{}})
	activeSetName = name
	refreshSetSelector()
	displayMemoSet(nil)
	return nil
}

func renameMemoSet(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("セット名を入力してください")
	}
	if name == activeSetName {
		return nil
	}
	for _, set := range memoSets {
		if strings.EqualFold(set.Name, name) {
			return fmt.Errorf("単語セット %q はすでに存在します", name)
		}
	}
	if err := persistMemos(); err != nil {
		disablePersistence(err)
		return err
	}

	oldName := activeSetName
	found := false
	for i := range memoSets {
		if memoSets[i].Name == oldName {
			memoSets[i].Name = name
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("単語セット %q が見つかりません", oldName)
	}
	activeSetName = name
	if err := writeMemoSets(memoSets, activeSetName); err != nil {
		activeSetName = oldName
		for i := range memoSets {
			if memoSets[i].Name == name {
				memoSets[i].Name = oldName
				break
			}
		}
		disablePersistence(err)
		return err
	}
	refreshSetSelector()
	return nil
}

type memoEntry struct {
	*widget.Entry
	row *memoRow
}

func (e *memoEntry) TappedSecondary(event *fyne.PointEvent) {
	e.row.showDetail(event.AbsolutePosition)
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

func (h *reorderHandle) TappedSecondary(event *fyne.PointEvent) {
	h.row.showDetail(event.AbsolutePosition)
}

func (r *memoRow) showDetail(position fyne.Position) {
	text := strings.TrimSpace(r.detail)
	if text == "" {
		text = "詳細は未登録です"
	}
	if r.detailPopup != nil {
		r.detailPopup.Hide()
	}
	r.detailPopup = widget.NewPopUp(newDetailPopupContent(text, mainWindow.Canvas().Size(), func() {
		r.detailPopup.Hide()
		editMemoDetail(r)
	}), mainWindow.Canvas())
	popupSize := r.detailPopup.MinSize()
	canvasSize := mainWindow.Canvas().Size()
	x := position.X
	y := position.Y
	if x+popupSize.Width > canvasSize.Width {
		x = canvasSize.Width - popupSize.Width
	}
	if x < 0 {
		x = 0
	}
	if y+popupSize.Height > canvasSize.Height {
		y = canvasSize.Height - popupSize.Height
	}
	if y < 0 {
		y = 0
	}
	r.detailPopup.ShowAtPosition(fyne.NewPos(x, y))
}

type detailPopupLayout struct {
	size         fyne.Size
	padding      float32
	buttonHeight float32
}

func (l detailPopupLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return l.size
}

func (l detailPopupLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	objects[0].Move(fyne.NewPos(l.padding, l.padding))
	objects[0].Resize(fyne.NewSize(size.Width-2*l.padding, size.Height-3*l.padding-l.buttonHeight))
	objects[1].Move(fyne.NewPos(size.Width-l.padding-objects[1].MinSize().Width, size.Height-l.padding-l.buttonHeight))
	objects[1].Resize(fyne.NewSize(objects[1].MinSize().Width, l.buttonHeight))
}

func newDetailPopupContent(text string, canvasSize fyne.Size, onEdit func()) fyne.CanvasObject {
	width := float32(360)
	if canvasSize.Width > 0 && width > canvasSize.Width-24 {
		width = canvasSize.Width - 24
	}
	if width < 180 {
		width = 180
	}
	padding := theme.Padding()
	label := widget.NewRichTextWithText(text)
	label.Wrapping = fyne.TextWrapWord
	label.Resize(fyne.NewSize(width-2*padding, 10000))
	editButton := widget.NewButton("編集", onEdit)
	buttonHeight := editButton.MinSize().Height
	height := label.MinSize().Height + 3*padding + buttonHeight
	if height < 2*padding+theme.TextSize() {
		height = 2*padding + theme.TextSize()
	}
	size := fyne.NewSize(width, height)
	return container.New(detailPopupLayout{size: size, padding: padding, buttonHeight: buttonHeight}, label, editButton)
}

func editMemoDetail(row *memoRow) {
	if row.detailEditor != nil {
		row.detailEditor.Show()
		return
	}

	editor := widget.NewMultiLineEntry()
	editor.SetText(row.detail)
	editor.SetPlaceHolder("このメモの詳細")
	editor.SetMinRowsVisible(5)
	editorWindow := fyne.CurrentApp().NewWindow("詳細を編集")
	row.detailEditor = editorWindow
	editorWindow.SetOnClosed(func() {
		row.detailEditor = nil
	})
	cancelButton := widget.NewButton("キャンセル", editorWindow.Close)
	saveButton := widget.NewButton("保存", func() {
		row.detail = strings.TrimSpace(editor.Text)
		saveMemos()
		editorWindow.Close()
	})
	buttons := container.NewHBox(layout.NewSpacer(), cancelButton, saveButton)
	editorWindow.SetContent(container.NewBorder(nil, buttons, nil, nil, editor))
	editorWindow.Resize(fyne.NewSize(460, 320))
	editorWindow.Show()
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

func loadMemoSets() ([]memoSet, string, error) {
	path, err := memoFilePath()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []memoSet{{Name: defaultMemoSetName, Memos: []memoData{}}}, defaultMemoSetName, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("%s を読み込めません: %w", path, err)
	}

	var legacyMemos []string
	if err := json.Unmarshal(data, &legacyMemos); err == nil && legacyMemos != nil {
		memos := make([]memoData, 0, len(legacyMemos))
		for _, text := range legacyMemos {
			memos = append(memos, memoData{Text: text})
		}
		return []memoSet{{Name: defaultMemoSetName, Memos: memos}}, defaultMemoSetName, nil
	}

	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, "", fmt.Errorf("%s の形式が不正です: %w", path, err)
	}
	if header.Version == 2 {
		var legacy struct {
			Version int        `json:"version"`
			Memos   []memoData `json:"memos"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return nil, "", fmt.Errorf("%s の形式が不正です: %w", path, err)
		}
		if legacy.Memos == nil {
			return nil, "", fmt.Errorf("%s の形式が不正です: メモ一覧がありません", path)
		}
		return []memoSet{{Name: defaultMemoSetName, Memos: legacy.Memos}}, defaultMemoSetName, nil
	}
	if header.Version != 3 {
		return nil, "", fmt.Errorf("%s の形式が不正です: 未対応のデータバージョン %d", path, header.Version)
	}
	var stored storedMemoFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, "", fmt.Errorf("%s の形式が不正です: %w", path, err)
	}
	if stored.Sets == nil {
		return nil, "", fmt.Errorf("%s の形式が不正です: 単語セット一覧がありません", path)
	}
	if err := validateMemoSets(stored.Sets, stored.ActiveSet); err != nil {
		return nil, "", fmt.Errorf("%s の形式が不正です: %w", path, err)
	}
	return stored.Sets, stored.ActiveSet, nil
}

func saveMemos() {
	if loadingMemos || persistenceDisabled {
		return
	}

	if err := persistMemos(); err != nil {
		disablePersistence(err)
	}
}

func persistMemos() error {
	memos := make([]memoData, 0, len(rows))
	for _, row := range rows {
		memos = append(memos, memoData{Text: row.entry.Text, Detail: row.detail})
	}
	for i := range memoSets {
		if memoSets[i].Name == activeSetName {
			memoSets[i].Memos = memos
			return writeMemoSets(memoSets, activeSetName)
		}
	}
	return fmt.Errorf("選択中の単語セット %q が見つかりません", activeSetName)
}

func validateMemoSets(sets []memoSet, activeSet string) error {
	if len(sets) == 0 {
		return errors.New("単語セットがありません")
	}
	foundActiveSet := false
	names := make(map[string]struct{}, len(sets))
	for i := range sets {
		name := strings.TrimSpace(sets[i].Name)
		if name == "" {
			return fmt.Errorf("%d 番目の単語セット名が空です", i+1)
		}
		key := strings.ToLower(name)
		if _, exists := names[key]; exists {
			return fmt.Errorf("単語セット名 %q が重複しています", name)
		}
		names[key] = struct{}{}
		if sets[i].Memos == nil {
			return fmt.Errorf("単語セット %q のメモ一覧がありません", name)
		}
		if name == activeSet {
			foundActiveSet = true
		}
	}
	if !foundActiveSet {
		return fmt.Errorf("選択中の単語セット %q がありません", activeSet)
	}
	return nil
}

func writeMemoSets(sets []memoSet, activeSet string) error {
	if err := validateMemoSets(sets, activeSet); err != nil {
		return err
	}
	path, err := memoFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("%s を作成できません: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(storedMemoFile{Version: 3, ActiveSet: activeSet, Sets: sets}, "", "  ")
	if err != nil {
		return fmt.Errorf("単語セットを JSON に変換できません: %w", err)
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
