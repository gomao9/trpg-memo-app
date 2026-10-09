//go:build windows

package main

import (
    "image/color"
    "strings"
    "syscall"
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

func main() {
    a := app.New()
    w := a.NewWindow("TRPG Memo")
    w.SetFixedSize(true)
    w.Resize(fyne.NewSize(300, 420))
    w.SetPadded(false)
    w.SetContent(buildUI())
    w.Show()

    // Windows での見た目調整（タイトルバーなし + 常に最前面）
    applyWindowsWindowStyle(w)

    // 最初の1行を追加
    appendRow("")

    a.Run()
}

func buildUI() fyne.CanvasObject {
    bg := canvas.NewRectangle(color.NRGBA{R: 255, G: 244, B: 190, A: 255})
    bg.SetMinSize(fyne.NewSize(300, 420))

    headerBg := canvas.NewRectangle(color.NRGBA{R: 214, G: 182, B: 92, A: 255})
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

    footer := container.NewHBox(addBtn)
    content := container.NewBorder(header, footer, nil, nil, scroll)

    return container.NewStack(bg, content)
}

func appendRow(raw string) {
    trimmed := strings.TrimSpace(raw)

    entry := widget.NewEntry()
    entry.SetPlaceHolder("固有名詞")
    entry.SetText(trimmed)

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
// Windows のウィンドウスタイル設定
// ---------------------------------------------------------

var (
    user32 = windows.NewLazySystemDLL("user32.dll")

    findWindowW    = user32.NewProc("FindWindowW")
    setWindowPos   = user32.NewProc("SetWindowPos")
    getWindowLongW = user32.NewProc("GetWindowLongPtrW")
    setWindowLongW = user32.NewProc("SetWindowLongPtrW")
)

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

func applyWindowsWindowStyle(w fyne.Window) {
    // Fyne のウィンドウタイトル名から HWND を取得
    hwnd := findWindowHandleByTitle(w.Title())
    if hwnd == 0 {
        return
    }

    // タイトルバーを消す
    style, _, _ := getWindowLongPtr(hwnd, GWL_STYLE)
    style &^= WS_CAPTION
    style &^= WS_THICKFRAME
    style &^= WS_MINIMIZEBOX
    style &^= WS_MAXIMIZEBOX
    style &^= WS_SYSMENU
    setWindowLongPtr(hwnd, GWL_STYLE, style)

    // 常に最前面にする
    _, _, _ = setWindowPos.Call(
        uintptr(hwnd),
        uintptr(HWND_TOPMOST),
        0,
        0,
        0,
        0,
        uintptr(SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW),
    )
}

func findWindowHandleByTitle(title string) uintptr {
    titleUTF16, _ := syscall.UTF16PtrFromString(title)
    ret, _, _ := findWindowW.Call(0, uintptr(unsafe.Pointer(titleUTF16)))
    return ret
}

func getWindowLongPtr(hwnd uintptr, index int32) (uintptr, uintptr, error) {
    ret, _, err := getWindowLongW.Call(uintptr(hwnd), uintptr(index))
    return ret, 0, err
}

func setWindowLongPtr(hwnd uintptr, index int32, value uintptr) (uintptr, uintptr, error) {
    ret, _, err := setWindowLongW.Call(uintptr(hwnd), uintptr(index), value)
    return ret, 0, err
}
