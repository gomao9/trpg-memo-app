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
	textField *widget.Entry
	deleteBtn *widget.Button
}

// 画面上のメモ行を管理
var (
	rows     []*memoRow
	listBox  *fyne.Container
	mainWin  fyne.Window
)

func main() {
	a := app.New()
	w := a.NewWindow()
	mainWin = w

	// 基本サイズ
	w.Resize(fyne.NewSize(300, 420))
	w.SetFixedSize(true)
	w.SetPadded(false)

	// 画面端に収まるスタイル
	w.SetContent(buildUI())

	// ドロップ時の処理
	w.SetOnDropped(func(pos fyne.Position, objs []fyne.CanvasObject) {
		for _, obj := range objs {
			if e, ok := obj.(*widget.Entry); ok {
				appendMemo(strings.TrimSpace(e.Text))
				return
			}
			if txt, ok := obj.(*canvas.Text); ok {
				appendMemo(strings.TrimSpace(txt.Text))
				return
			}
		}
	})

	// 画面表示
	w.Show()

	// Windowsネイティブ: タイトルバーなし + 常に最前面
	makeWindowsChrome(w)

	// 最初の1行を生成
	appendMemo("")

	a.Run()
}

func buildUI() fyne.CanvasObject {
	// 背景色
	bg := canvas.NewRectangle(color.NRGBA{R: 255, G: 244, B: 190, A: 255})
	bg.SetMinSize(fyne.NewSize(300, 420))

	// ヘッダー（ドラッグで移動用の領域）
	header := canvas.NewRectangle(color.NRGBA{R: 208, G: 176, B: 92, A: 255})
	header.SetMinSize(fyne.NewSize(300, 32))

	// タイトル文字
	title := canvas.NewText("📝 TRPG Memo", color.NRGBA{R: 40, G: 30, B: 10, A: 255})
	title.TextStyle = fyne.TextStyle{Bold: true}
	title.Alignment = fyne.TextAlignCenter
	title.TextSize = 13

	// ヘッダの中央にタイトル配置
	headerWrap := container.NewStack(header, title)

	// メモリスト本体
	listBox = container.NewVBox()
	list := container.NewScroll(listBox)
	list.SetMinSize(fyne.NewSize(280, 320))

	// 追加ボタン
	addBtn := widget.NewButton("+", func() {
		appendMemo("")
	})
	addBtn.Importance = widget.MediumImportance

	// 上下レイアウト
	content := container.NewBorder(
		headerWrap,
		addBtn,
		nil,
		nil,
		list,
	)

	// 背景に重ねる
	layer := container.NewStack(bg, content)

	return layer
}

func appendMemo(text string) {
	s := strings.TrimSpace(text)

	row := &memoRow{
		textField: widget.NewEntry(),
		deleteBtn: widget.NewButton("×", nil),
	}
	row.textField.SetPlaceHolder("固有名詞")
	row.textField.SetText(s)
	row.deleteBtn.Importance = widget.DangerImportance
	row.deleteBtn.OnTapped = func() {
		removeMemoRow(row)
	}

	listBox.Add(container.NewHBox(
		row.textField,
		row.deleteBtn,
	))
	rows = append(rows, row)
	listBox.Refresh()
}

func removeMemoRow(target *memoRow) {
	for i, r := range rows {
		if r == target {
			rows = append(rows[:i], rows[i+1:]...)
			break
		}
	}

	// 再描画
	rebuildList()
}

func rebuildList() {
	listBox.RemoveAll()
	for _, r := range rows {
		listBox.Add(container.NewHBox(
			r.textField,
			r.deleteBtn,
		))
	}
	listBox.Refresh()
}

// ---------------------------------------------------------
// Windowsネイティブのウィンドウ設定
// ---------------------------------------------------------

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	findWindowW     = user32.NewProc("FindWindowW")
	setWindowPos    = user32.NewProc("SetWindowPos")
	getWindowLongW  = user32.NewProc("GetWindowLongPtrW")
	setWindowLongW  = user32.NewProc("SetWindowLongPtrW")
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

func makeWindowsChrome(w fyne.Window) {
	hwnd := findWindowHandle("trpgmemo.exe")
	if hwnd == 0 {
		// ウィンドウが見つからない場合はスキップ
		return
	}

	// 1) タイトルバーと境界線を除去
	style, _, _ := getWindowLong(hwnd, GWL_STYLE)
	style &^= WS_CAPTION
	style &^= WS_THICKFRAME
	style &^= WS_MINIMIZEBOX
	style &^= WS_MAXIMIZEBOX
	style &^= WS_SYSMENU
	setWindowLong(hwnd, GWL_STYLE, style)

	// 2) 常に最前面
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

func findWindowHandle(className string) uintptr {
	classUTF16, _ := syscall.UTF16PtrFromString(className)
	ret, _, _ := findWindowW.Call(uintptr(unsafe.Pointer(classUTF16)), 0)
	return ret
}

func getWindowLong(hwnd uintptr, index int32) (uintptr, uintptr, error) {
	ret, _, err := getWindowLongW.Call(uintptr(hwnd), uintptr(index))
	return ret, 0, err
}

func setWindowLong(hwnd uintptr, index int32, value uintptr) (uintptr, uintptr, error) {
	ret, _, err := setWindowLongW.Call(uintptr(hwnd), uintptr(index), value)
	return ret, 0, err
}
