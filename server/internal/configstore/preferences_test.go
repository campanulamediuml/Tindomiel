package configstore

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAppSettingsDefaultsAndPersistence(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "profiles.json"))
	settings, err := store.AppSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != TerminalThemeHomebrew || settings.FontSize != 13 || !settings.CursorBlink || settings.Scrollback != 10_000 || len(settings.Shortcuts) != 0 {
		t.Fatalf("unexpected defaults %+v", settings)
	}

	if err := store.SetEditorApplication("/Applications/Test.app"); err != nil {
		t.Fatal(err)
	}
	settings.Theme = TerminalThemeClearDark
	settings.FontSize = 15
	settings.CursorBlink = false
	settings.Scrollback = 2_000
	settings.DownloadDirectory = "/tmp"
	settings.Shortcuts = []ShortcutMapping{{From: "Ctrl+Shift+P", To: "Ctrl+A"}, {From: "F2", To: "Ctrl+B"}}
	if err := store.SetAppSettings(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.AppSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, settings) {
		t.Fatalf("settings changed after reload: got %+v want %+v", reloaded, settings)
	}
	application, err := store.EditorApplication()
	if err != nil {
		t.Fatal(err)
	}
	if application != "/Applications/Test.app" {
		t.Fatalf("unrelated preference was lost: %q", application)
	}
}

func TestSetAppSettingsRejectsInvalidValues(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "profiles.json"))
	valid := AppSettings{Theme: TerminalThemeHomebrew, FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "/tmp", Shortcuts: []ShortcutMapping{}}
	for _, settings := range []AppSettings{
		{Theme: "unknown", FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "/tmp"},
		{FontSize: 9, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "/tmp"},
		{FontSize: 13, CursorBlink: true, Scrollback: 999, DownloadDirectory: "/tmp"},
		{FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "relative"},
		{FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "/tmp", Shortcuts: []ShortcutMapping{{From: "Ctrl+A"}}},
	} {
		if err := store.SetAppSettings(settings); err == nil {
			t.Fatalf("expected invalid settings to fail: %+v", settings)
		}
	}
	if err := store.SetAppSettings(valid); err != nil {
		t.Fatal(err)
	}
}

func TestSetAppSettingsWithoutThemeUsesHomebrew(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "profiles.json"))
	settings := AppSettings{FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: "/tmp", Shortcuts: []ShortcutMapping{}}
	if err := store.SetAppSettings(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.AppSettings()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Theme != TerminalThemeHomebrew {
		t.Fatalf("got theme %q, want %q", reloaded.Theme, TerminalThemeHomebrew)
	}
}
