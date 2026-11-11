package configstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	TerminalThemeHomebrew  = "homebrew"
	TerminalThemeClearDark = "clearDark"
	TerminalThemeBoring    = "boring"
)

type AppSettings struct {
	Theme             string            `json:"theme"`
	FontSize          int               `json:"fontSize"`
	CursorBlink       bool              `json:"cursorBlink"`
	Scrollback        int               `json:"scrollback"`
	DownloadDirectory string            `json:"downloadDirectory"`
	Shortcuts         []ShortcutMapping `json:"shortcuts"`
}

type preferences struct {
	EditorApplication   string            `json:"editorApplication"`
	DownloadDirectory   string            `json:"downloadDirectory,omitempty"`
	HiddenProfiles      []string          `json:"hiddenProfiles,omitempty"`
	TerminalTheme       string            `json:"terminalTheme,omitempty"`
	TerminalFontSize    int               `json:"terminalFontSize,omitempty"`
	TerminalCursorBlink *bool             `json:"terminalCursorBlink,omitempty"`
	TerminalScrollback  int               `json:"terminalScrollback,omitempty"`
	GlobalShortcuts     []ShortcutMapping `json:"shortcuts,omitempty"`
}

func (s *Store) AppSettings() (AppSettings, error) {
	value, err := s.readPreferences()
	if err != nil {
		return AppSettings{}, err
	}
	settings := AppSettings{Theme: TerminalThemeHomebrew, FontSize: 13, CursorBlink: true, Scrollback: 10_000, DownloadDirectory: value.DownloadDirectory, Shortcuts: []ShortcutMapping{}}
	if value.TerminalTheme == TerminalThemeHomebrew || value.TerminalTheme == TerminalThemeClearDark || value.TerminalTheme == TerminalThemeBoring {
		settings.Theme = value.TerminalTheme
	}
	if value.TerminalFontSize != 0 {
		settings.FontSize = value.TerminalFontSize
	}
	if value.TerminalCursorBlink != nil {
		settings.CursorBlink = *value.TerminalCursorBlink
	}
	if value.TerminalScrollback != 0 {
		settings.Scrollback = value.TerminalScrollback
	}
	settings.Shortcuts = append(settings.Shortcuts, value.GlobalShortcuts...)
	return settings, nil
}

func (s *Store) SetAppSettings(settings AppSettings) error {
	if settings.Theme == "" {
		settings.Theme = TerminalThemeHomebrew
	}
	if settings.Theme != TerminalThemeHomebrew && settings.Theme != TerminalThemeClearDark && settings.Theme != TerminalThemeBoring {
		return errors.New("unknown terminal theme")
	}
	if settings.FontSize < 10 || settings.FontSize > 24 {
		return errors.New("terminal font size must be between 10 and 24")
	}
	if settings.Scrollback < 1_000 || settings.Scrollback > 50_000 {
		return errors.New("terminal scrollback must be between 1000 and 50000")
	}
	if settings.DownloadDirectory == "" || !filepath.IsAbs(settings.DownloadDirectory) {
		return errors.New("an absolute download directory is required")
	}
	if len(settings.Shortcuts) > 128 {
		return errors.New("too many shortcut mappings")
	}
	for _, shortcut := range settings.Shortcuts {
		if strings.TrimSpace(shortcut.From) == "" || strings.TrimSpace(shortcut.To) == "" {
			return errors.New("shortcut mappings require both from and to")
		}
		if len(shortcut.From) > 128 || len(shortcut.To) > 128 {
			return errors.New("shortcut mapping is too long")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readPreferences()
	if err != nil {
		return err
	}
	value.TerminalTheme = settings.Theme
	value.TerminalFontSize = settings.FontSize
	value.TerminalCursorBlink = &settings.CursorBlink
	value.TerminalScrollback = settings.Scrollback
	value.DownloadDirectory = settings.DownloadDirectory
	value.GlobalShortcuts = append([]ShortcutMapping(nil), settings.Shortcuts...)
	return writePreferences(s.preferencesPath(), value)
}

func (s *Store) EditorApplication() (string, error) {
	value, err := s.readPreferences()
	if err != nil {
		return "", err
	}
	return value.EditorApplication, nil
}

func (s *Store) SetEditorApplication(application string) error {
	if application == "" || !filepath.IsAbs(application) {
		return errors.New("an absolute application path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readPreferences()
	if err != nil {
		return err
	}
	value.EditorApplication = application
	return writePreferences(s.preferencesPath(), value)
}

func (s *Store) DownloadDirectory() (string, error) {
	value, err := s.readPreferences()
	if err != nil {
		return "", err
	}
	return value.DownloadDirectory, nil
}

func (s *Store) SetDownloadDirectory(directory string) error {
	if directory == "" || !filepath.IsAbs(directory) {
		return errors.New("an absolute download directory is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readPreferences()
	if err != nil {
		return err
	}
	value.DownloadDirectory = directory
	return writePreferences(s.preferencesPath(), value)
}

func (s *Store) HideProfile(id string) error {
	if !strings.HasPrefix(id, "openssh:") {
		return errors.New("only OpenSSH profiles can be hidden")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.readPreferences()
	if err != nil {
		return err
	}
	for _, existing := range value.HiddenProfiles {
		if existing == id {
			return nil
		}
	}
	value.HiddenProfiles = append(value.HiddenProfiles, id)
	return writePreferences(s.preferencesPath(), value)
}

func (s *Store) readPreferences() (preferences, error) {
	var value preferences
	data, err := os.ReadFile(s.preferencesPath())
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (s *Store) preferencesPath() string {
	return filepath.Join(filepath.Dir(s.path), "preferences.json")
}

func writePreferences(path string, value preferences) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(path), ".preferences-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
