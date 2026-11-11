package configstore

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type ShortcutMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Options struct {
	Host                 string            `json:"host"`
	Port                 int               `json:"port"`
	User                 string            `json:"user"`
	Auth                 string            `json:"auth"`
	Password             string            `json:"password,omitempty"`
	PrivateKeys          []string          `json:"privateKeys"`
	PrivateKeyPassphrase string            `json:"privateKeyPassphrase,omitempty"`
	Shortcuts            []ShortcutMapping `json:"shortcuts,omitempty"`
}

type Profile struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Remark  string  `json:"remark"`
	Folder  string  `json:"folder"`
	Source  string  `json:"source"`
	Options Options `json:"options"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) List(ctx context.Context) ([]Profile, error) {
	custom, err := s.loadCustom()
	if err != nil {
		return nil, err
	}
	openSSH, err := loadOpenSSH(ctx)
	if err != nil {
		return nil, err
	}
	preferences, err := s.readPreferences()
	if err != nil {
		return nil, err
	}
	hidden := make(map[string]bool, len(preferences.HiddenProfiles))
	for _, id := range preferences.HiddenProfiles {
		hidden[id] = true
	}
	visible := make([]Profile, 0, len(openSSH))
	for _, profile := range openSSH {
		if !hidden[profile.ID] {
			visible = append(visible, profile)
		}
	}
	return append(visible, custom...), nil
}

func (s *Store) Get(ctx context.Context, id string) (Profile, error) {
	if strings.HasPrefix(id, "custom:") {
		profiles, err := s.loadCustom()
		if err != nil {
			return Profile{}, err
		}
		for _, profile := range profiles {
			if profile.ID == id {
				return profile, nil
			}
		}
		return Profile{}, errors.New("profile not found")
	}
	if alias, ok := strings.CutPrefix(id, "openssh:"); ok && alias != "" {
		preferences, err := s.readPreferences()
		if err != nil {
			return Profile{}, err
		}
		for _, hidden := range preferences.HiddenProfiles {
			if hidden == id {
				return Profile{}, errors.New("profile not found")
			}
		}
		return resolveOpenSSH(ctx, alias)
	}
	return Profile{}, errors.New("profile not found")
}

func (s *Store) Create(profile Profile) (Profile, error) {
	if profile.Options.Host == "" || profile.Options.User == "" {
		return Profile{}, errors.New("host and user are required")
	}
	if profile.Options.Port == 0 {
		profile.Options.Port = 22
	}
	if profile.Options.Port < 1 || profile.Options.Port > 65535 {
		return Profile{}, errors.New("invalid port")
	}
	profile.ID = "custom:" + randomID()
	profile.Source = "custom"
	if profile.Options.Auth == "" {
		profile.Options.Auth = "publicKey"
	}
	if profile.Options.Auth != "publicKey" && profile.Options.Auth != "password" {
		return Profile{}, errors.New("auth must be publicKey or password")
	}
	if profile.Options.Auth == "password" && profile.Options.Password == "" {
		return Profile{}, errors.New("password is required for password authentication")
	}
	if profile.Name == "" {
		profile.Name = profile.Options.Host
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.loadCustom()
	if err != nil {
		return Profile{}, err
	}
	profiles = append(profiles, profile)
	if err := s.saveCustom(profiles); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *Store) Delete(id string) error {
	if strings.HasPrefix(id, "openssh:") {
		return s.HideProfile(id)
	}
	if !strings.HasPrefix(id, "custom:") {
		return errors.New("only custom profiles can be deleted")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.loadCustom()
	if err != nil {
		return err
	}
	filtered := profiles[:0]
	found := false
	for _, profile := range profiles {
		if profile.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, profile)
	}
	if !found {
		return errors.New("profile not found")
	}
	return s.saveCustom(filtered)
}

func (s *Store) loadCustom() ([]Profile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("parse profiles: %w", err)
	}
	return profiles, nil
}

func (s *Store) saveCustom(profiles []Profile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".profiles-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

func loadOpenSSH(ctx context.Context) ([]Profile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if errors.Is(err, os.ErrNotExist) {
		return []Profile{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var aliases []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, alias := range fields[1:] {
			if !strings.ContainsAny(alias, "*?!") {
				aliases = append(aliases, alias)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	resolved := make([]*Profile, len(aliases))
	workers := min(4, len(aliases))
	jobs := make(chan int)
	var workersWG sync.WaitGroup
	workersWG.Add(workers)
	for range workers {
		go func() {
			defer workersWG.Done()
			for index := range jobs {
				profile, err := resolveOpenSSH(ctx, aliases[index])
				if err == nil {
					resolved[index] = &profile
				}
			}
		}()
	}
	for index := range aliases {
		jobs <- index
	}
	close(jobs)
	workersWG.Wait()
	profiles := make([]Profile, 0, len(aliases))
	for _, profile := range resolved {
		if profile != nil {
			profiles = append(profiles, *profile)
		}
	}
	return profiles, nil
}

func resolveOpenSSH(ctx context.Context, alias string) (Profile, error) {
	command := exec.CommandContext(ctx, "ssh", "-G", alias)
	output, err := command.Output()
	if err != nil {
		return Profile{}, err
	}
	options := Options{Port: 22, Auth: "publicKey"}
	for line := range strings.SplitSeq(string(output), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "hostname":
			options.Host = value
		case "user":
			options.User = value
		case "port":
			options.Port, _ = strconv.Atoi(value)
		case "identityfile":
			options.PrivateKeys = append(options.PrivateKeys, value)
		}
	}
	if options.Host == "" || options.User == "" {
		return Profile{}, errors.New("incomplete OpenSSH profile")
	}
	return Profile{ID: "openssh:" + alias, Name: alias, Source: "openssh", Options: options}, nil
}

func randomID() string {
	data := make([]byte, 8)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
