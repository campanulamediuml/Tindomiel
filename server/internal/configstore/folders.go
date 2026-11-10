package configstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Store) ListFolders() ([]Folder, error) {
	data, err := os.ReadFile(s.foldersPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Folder{}, nil
	}
	if err != nil {
		return nil, err
	}
	var folders []Folder
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

func (s *Store) CreateFolder(name string) (Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Folder{}, errors.New("folder name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	folders, err := s.ListFolders()
	if err != nil {
		return Folder{}, err
	}
	for _, folder := range folders {
		if folder.Name == name {
			return Folder{}, errors.New("folder already exists")
		}
	}
	folder := Folder{ID: "folder:" + randomID(), Name: name}
	folders = append(folders, folder)
	if err := writeFolders(s.foldersPath(), folders); err != nil {
		return Folder{}, err
	}
	return folder, nil
}

func (s *Store) foldersPath() string {
	return filepath.Join(filepath.Dir(s.path), "folders.json")
}

func writeFolders(path string, folders []Folder) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(folders, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(path), ".folders-*")
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
