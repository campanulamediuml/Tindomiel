package configstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type preferences struct {
	EditorApplication string   `json:"editorApplication"`
	DownloadDirectory string   `json:"downloadDirectory,omitempty"`
	HiddenProfiles    []string `json:"hiddenProfiles,omitempty"`
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
