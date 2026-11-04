//go:build windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fynetesting "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestTooltipContentGrowsToShowAllText(t *testing.T) {
	app := fynetesting.NewApp()
	defer app.Quit()

	short := newDetailPopupContent("Short detail", fyne.NewSize(300, 420), func() {}).MinSize()
	long := newDetailPopupContent(strings.Repeat("長い詳細テキスト ", 30), fyne.NewSize(300, 420), func() {}).MinSize()
	if long.Width != short.Width {
		t.Fatalf("long tooltip width = %v, want fixed width %v", long.Width, short.Width)
	}
	if long.Height <= short.Height {
		t.Fatalf("long tooltip height = %v, want greater than short tooltip height %v", long.Height, short.Height)
	}
}

func TestDetailPopupUsesCurrentDetail(t *testing.T) {
	app := fynetesting.NewApp()
	defer app.Quit()

	const detail = "Updated detail"
	popup := newDetailPopupContent(detail, fyne.NewSize(300, 420), func() {})
	content, ok := popup.(*fyne.Container)
	if !ok {
		t.Fatalf("detail popup content type = %T, want *fyne.Container", popup)
	}
	text, ok := content.Objects[0].(*widget.RichText)
	if !ok {
		t.Fatalf("detail popup text type = %T, want *widget.RichText", content.Objects[0])
	}
	if got := text.String(); got != detail {
		t.Fatalf("detail popup text = %q, want %q", got, detail)
	}
}

func TestMemoEntryAllowsTyping(t *testing.T) {
	app := fynetesting.NewApp()
	defer app.Quit()

	entry := widget.NewEntry()
	wrappedEntry := &memoEntry{Entry: entry}
	if entry.Disabled() {
		t.Fatal("memo entry is disabled")
	}
	fynetesting.Type(wrappedEntry, "編集可能")
	if got, want := entry.Text, "編集可能"; got != want {
		t.Fatalf("memo entry text = %q, want %q", got, want)
	}
}

func TestToggleAlwaysOnTopUpdatesButton(t *testing.T) {
	testApp := fynetesting.NewApp()
	defer testApp.Quit()
	defer func() {
		topmostButton = nil
		windowHandle.Store(0)
		alwaysOnTop.Store(true)
	}()

	windowHandle.Store(0)
	alwaysOnTop.Store(true)
	topmostButton = widget.NewButton(topmostButtonText(), nil)
	if err := toggleAlwaysOnTop(); err != nil {
		t.Fatalf("toggleAlwaysOnTop() disabling error = %v", err)
	}
	if alwaysOnTop.Load() {
		t.Fatal("alwaysOnTop remains enabled after disabling")
	}
	if got, want := topmostButton.Text, "最前面 OFF"; got != want {
		t.Fatalf("button text = %q, want %q", got, want)
	}

	if err := toggleAlwaysOnTop(); err != nil {
		t.Fatalf("toggleAlwaysOnTop() enabling error = %v", err)
	}
	if !alwaysOnTop.Load() {
		t.Fatal("alwaysOnTop remains disabled after enabling")
	}
	if got, want := topmostButton.Text, "最前面 ON"; got != want {
		t.Fatalf("button text = %q, want %q", got, want)
	}
}

func TestMemoSetPersistenceRoundTripAndReplace(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	want := []memoSet{
		{Name: defaultMemoSetName, Memos: []memoData{
			{Text: "NPC・一", Detail: "宿屋の主人"},
			{Text: ""},
		}},
		{Name: "探索先", Memos: []memoData{
			{Text: "Ally", Detail: "味方"},
		}},
	}
	if err := writeMemoSets(want, "探索先"); err != nil {
		t.Fatalf("writeMemoSets() error = %v", err)
	}

	got, activeSet, err := loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() error = %v", err)
	}
	if activeSet != "探索先" {
		t.Fatalf("active set = %q, want %q", activeSet, "探索先")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemoSets() = %#v, want %#v", got, want)
	}

	replacement := []memoSet{{Name: "新規", Memos: []memoData{{Text: "replacement", Detail: "更新後の詳細"}}}}
	if err := writeMemoSets(replacement, "新規"); err != nil {
		t.Fatalf("writeMemoSets() replacing existing data error = %v", err)
	}
	got, activeSet, err = loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() after replace error = %v", err)
	}
	if !reflect.DeepEqual(got, replacement) {
		t.Fatalf("loadMemoSets() after replace = %#v, want %#v", got, replacement)
	}
	if activeSet != "新規" {
		t.Fatalf("active set after replace = %q, want %q", activeSet, "新規")
	}
}

func TestLoadMemoSetsMissingFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	got, activeSet, err := loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() for missing file error = %v", err)
	}
	want := []memoSet{{Name: defaultMemoSetName, Memos: []memoData{}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemoSets() for missing file = %#v, want %#v", got, want)
	}
	if activeSet != defaultMemoSetName {
		t.Fatalf("active set for missing file = %q, want %q", activeSet, defaultMemoSetName)
	}
}

func TestLoadMemoSetsMigratesLegacyStringArray(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	path, err := memoFilePath()
	if err != nil {
		t.Fatalf("memoFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`["NPC・一","Ally"]`), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, activeSet, err := loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() for legacy data error = %v", err)
	}
	want := []memoSet{{Name: defaultMemoSetName, Memos: []memoData{{Text: "NPC・一"}, {Text: "Ally"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemoSets() for legacy data = %#v, want %#v", got, want)
	}
	if activeSet != defaultMemoSetName {
		t.Fatalf("active set for legacy data = %q, want %q", activeSet, defaultMemoSetName)
	}
}

func TestLoadMemoSetsMigratesVersionTwoFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	path, err := memoFilePath()
	if err != nil {
		t.Fatalf("memoFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"memos":[{"text":"旅館","detail":"入口"}]}`), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, activeSet, err := loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() for version 2 data error = %v", err)
	}
	want := []memoSet{{Name: defaultMemoSetName, Memos: []memoData{{Text: "旅館", Detail: "入口"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemoSets() for version 2 data = %#v, want %#v", got, want)
	}
	if activeSet != defaultMemoSetName {
		t.Fatalf("active set for version 2 data = %q, want %q", activeSet, defaultMemoSetName)
	}
}

func TestLoadMemoSetsRejectsInvalidData(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	path, err := memoFilePath()
	if err != nil {
		t.Fatalf("memoFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"not":"an array"}`), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, _, err := loadMemoSets(); err == nil {
		t.Fatal("loadMemoSets() error = nil, want invalid-data error")
	}
}

func TestSwitchMemoSetsPreservesEachList(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := fynetesting.NewApp()
	defer app.Quit()
	defer func() {
		rows = nil
		listBox = nil
		setSelector = nil
		memoSets = nil
		activeSetName = ""
		mainWindow = nil
		persistenceDisabled = false
		persistenceErrorShown = false
	}()

	memoSets = []memoSet{
		{Name: defaultMemoSetName, Memos: []memoData{{Text: "最初の単語"}}},
		{Name: "別セット", Memos: []memoData{{Text: "別の単語"}}},
	}
	activeSetName = defaultMemoSetName
	persistenceDisabled = false
	mainWindow = app.NewWindow("test")
	mainWindow.SetContent(buildUI(mainWindow))
	displayMemoSet(selectedSetMemos())
	rows[0].entry.SetText("編集した単語")

	if err := switchMemoSet("別セット"); err != nil {
		t.Fatalf("switchMemoSet() error = %v", err)
	}
	if got, want := rows[0].entry.Text, "別の単語"; got != want {
		t.Fatalf("selected set text = %q, want %q", got, want)
	}
	if err := switchMemoSet(defaultMemoSetName); err != nil {
		t.Fatalf("switchMemoSet() back error = %v", err)
	}
	if got, want := rows[0].entry.Text, "編集した単語"; got != want {
		t.Fatalf("restored set text = %q, want %q", got, want)
	}
}

func TestRenameMemoSetPreservesMemosAndSelection(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := fynetesting.NewApp()
	defer app.Quit()
	defer func() {
		rows = nil
		setSelector = nil
		memoSets = nil
		activeSetName = ""
		persistenceDisabled = false
		persistenceErrorShown = false
	}()

	entry := widget.NewEntry()
	entry.SetText("編集済みの単語")
	rows = []*memoRow{{entry: entry, detail: "詳細"}}
	memoSets = []memoSet{
		{Name: defaultMemoSetName, Memos: []memoData{}},
		{Name: "別セット", Memos: []memoData{{Text: "保持する単語"}}},
	}
	activeSetName = defaultMemoSetName
	persistenceDisabled = false

	if err := renameMemoSet("新しい名前"); err != nil {
		t.Fatalf("renameMemoSet() error = %v", err)
	}
	if activeSetName != "新しい名前" {
		t.Fatalf("active set = %q, want %q", activeSetName, "新しい名前")
	}
	got, activeSet, err := loadMemoSets()
	if err != nil {
		t.Fatalf("loadMemoSets() error = %v", err)
	}
	want := []memoSet{
		{Name: "新しい名前", Memos: []memoData{{Text: "編集済みの単語", Detail: "詳細"}}},
		{Name: "別セット", Memos: []memoData{{Text: "保持する単語"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemoSets() = %#v, want %#v", got, want)
	}
	if activeSet != "新しい名前" {
		t.Fatalf("stored active set = %q, want %q", activeSet, "新しい名前")
	}
}

func TestRenameMemoSetRejectsBlankOrDuplicateName(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	defer func() {
		rows = nil
		memoSets = nil
		activeSetName = ""
		persistenceDisabled = false
	}()

	rows = []*memoRow{}
	memoSets = []memoSet{
		{Name: defaultMemoSetName, Memos: []memoData{}},
		{Name: "別セット", Memos: []memoData{}},
	}
	activeSetName = defaultMemoSetName
	persistenceDisabled = false
	for _, name := range []string{"  ", "別セット"} {
		if err := renameMemoSet(name); err == nil {
			t.Errorf("renameMemoSet(%q) error = nil, want validation error", name)
		}
		if activeSetName != defaultMemoSetName {
			t.Errorf("active set after renameMemoSet(%q) = %q, want unchanged", name, activeSetName)
		}
	}
}

func TestReorderMemoRows(t *testing.T) {
	first := &memoRow{}
	second := &memoRow{}
	third := &memoRow{}
	current := []*memoRow{first, second, third}

	got, ok := reorderMemoRows(current, 2, 0)
	if !ok {
		t.Fatal("reorderMemoRows() reported no change")
	}
	want := []*memoRow{third, first, second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reorderMemoRows() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(current, []*memoRow{first, second, third}) {
		t.Fatal("reorderMemoRows() modified its input")
	}
}

func TestReorderMemoRowsRejectsOutOfBounds(t *testing.T) {
	current := []*memoRow{{}, {}}
	got, ok := reorderMemoRows(current, 0, 2)
	if ok {
		t.Fatal("reorderMemoRows() reported a change for an out-of-bounds target")
	}
	if !reflect.DeepEqual(got, current) {
		t.Fatalf("reorderMemoRows() = %#v, want unchanged rows %#v", got, current)
	}
}
