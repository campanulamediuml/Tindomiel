package configstore

import (
	"errors"
	"strings"
)

func (s *Store) Update(profile Profile) (Profile, error) {
	if !strings.HasPrefix(profile.ID, "custom:") {
		return Profile{}, errors.New("only custom profiles can be edited")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.loadCustom()
	if err != nil {
		return Profile{}, err
	}
	for index, existing := range profiles {
		if existing.ID != profile.ID {
			continue
		}
		profile.Source = "custom"
		if profile.Options.Password == "" {
			profile.Options.Password = existing.Options.Password
		}
		if profile.Options.PrivateKeyPassphrase == "" {
			profile.Options.PrivateKeyPassphrase = existing.Options.PrivateKeyPassphrase
		}
		if profile.Options.Auth == "" {
			profile.Options.Auth = "publicKey"
		}
		if profile.Options.Host == "" || profile.Options.User == "" {
			return Profile{}, errors.New("host and user are required")
		}
		if profile.Options.Port < 1 || profile.Options.Port > 65535 {
			return Profile{}, errors.New("invalid port")
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
		profiles[index] = profile
		if err := s.saveCustom(profiles); err != nil {
			return Profile{}, err
		}
		return profile, nil
	}
	return Profile{}, errors.New("profile not found")
}
