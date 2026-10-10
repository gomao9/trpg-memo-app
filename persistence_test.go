//go:build windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMemoPersistenceRoundTripAndReplace(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	want := []string{"NPC・一", "", "Ally"}
	if err := writeMemos(want); err != nil {
		t.Fatalf("writeMemos() error = %v", err)
	}

	got, err := loadMemos()
	if err != nil {
		t.Fatalf("loadMemos() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadMemos() = %#v, want %#v", got, want)
	}

	replacement := []string{"replacement"}
	if err := writeMemos(replacement); err != nil {
		t.Fatalf("writeMemos() replacing existing data error = %v", err)
	}
	got, err = loadMemos()
	if err != nil {
		t.Fatalf("loadMemos() after replace error = %v", err)
	}
	if !reflect.DeepEqual(got, replacement) {
		t.Fatalf("loadMemos() after replace = %#v, want %#v", got, replacement)
	}
}

func TestLoadMemosMissingFile(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	got, err := loadMemos()
	if err != nil {
		t.Fatalf("loadMemos() for missing file error = %v", err)
	}
	if got != nil {
		t.Fatalf("loadMemos() for missing file = %#v, want nil", got)
	}
}

func TestLoadMemosRejectsInvalidData(t *testing.T) {
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

	if _, err := loadMemos(); err == nil {
		t.Fatal("loadMemos() error = nil, want invalid-data error")
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
