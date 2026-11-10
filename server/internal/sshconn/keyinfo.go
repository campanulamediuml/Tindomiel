package sshconn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

type KeyInfo struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Length int    `json:"length"`
}

func InspectPrivateKey(name string) (KeyInfo, error) {
	path, err := expandHome(name)
	if err != nil {
		return KeyInfo{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return KeyInfo{}, err
	}
	var publicKey ssh.PublicKey
	if signer, err := ssh.ParsePrivateKey(data); err == nil {
		publicKey = signer.PublicKey()
	} else {
		publicData, publicErr := os.ReadFile(path + ".pub")
		if publicErr != nil {
			return KeyInfo{}, fmt.Errorf("inspect private key: %w", err)
		}
		parsed, _, _, _, parseErr := ssh.ParseAuthorizedKey(publicData)
		if parseErr != nil {
			return KeyInfo{}, fmt.Errorf("inspect public key: %w", parseErr)
		}
		publicKey = parsed
	}
	return KeyInfo{
		Path:   path,
		Name:   filepath.Base(path),
		Type:   strings.ToUpper(strings.TrimPrefix(publicKey.Type(), "ssh-")),
		Length: len(publicKey.Marshal()),
	}, nil
}

func DefaultPrivateKeys() []KeyInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		return []KeyInfo{}
	}
	result := make([]KeyInfo, 0, 3)
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if info, err := InspectPrivateKey(path); err == nil {
			result = append(result, info)
		}
	}
	return result
}
