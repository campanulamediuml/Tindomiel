package configstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestShortcutsSurviveCreateAndUpdate(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "profiles.json"))
	created, err := store.Create(Profile{
		Name:   "test",
		Source: "custom",
		Options: Options{
			Host: "127.0.0.1", Port: 22, User: "user", Auth: "publicKey",
			Shortcuts: []ShortcutMapping{{From: "Ctrl+Shift+P", To: "Ctrl+A"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Options.Shortcuts = []ShortcutMapping{{From: "F1", To: "Ctrl+A"}, {From: "Alt+B", To: "ArrowLeft"}}
	if _, err := store.Update(created); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Options.Shortcuts) != 2 {
		t.Fatalf("got %d shortcuts", len(loaded.Options.Shortcuts))
	}
	if loaded.Options.Shortcuts[0].From != "F1" || loaded.Options.Shortcuts[0].To != "Ctrl+A" || loaded.Options.Shortcuts[1].To != "ArrowLeft" {
		t.Fatalf("unexpected shortcuts %+v", loaded.Options.Shortcuts)
	}
}
