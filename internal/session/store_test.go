package session

import (
	"path/filepath"
	"testing"

	"github.com/yuzu-ux/ycode/internal/provider"
)

func TestSaveLoadAndList(t *testing.T) {
	root := t.TempDir()
	store := &Store{root: root, baseDir: filepath.Join(t.TempDir(), "sessions")}
	state, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	state.Messages = []provider.Message{{Role: "user", Content: "hello"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "hello" {
		t.Fatalf("loaded state = %+v", loaded)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != state.ID {
		t.Fatalf("entries = %+v", entries)
	}
	latest, err := store.Load("latest")
	if err != nil || latest.ID != state.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
