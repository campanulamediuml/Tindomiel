package configstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCustomDoesNotResolveOpenSSH(t *testing.T) {
	home, logPath := fakeSSH(t)
	writeTestFile(t, filepath.Join(home, ".ssh", "config"), "Host alpha\n")
	storePath := filepath.Join(home, "profiles.json")
	writeTestFile(t, storePath, `[{"id":"custom:one","name":"one","source":"custom","options":{"host":"127.0.0.1","port":22,"user":"user","auth":"publicKey","privateKeys":[]}}]`)
	store := New(storePath)
	profile, err := store.Get(context.Background(), "custom:one")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Options.Host != "127.0.0.1" {
		t.Fatalf("unexpected host %q", profile.Options.Host)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("ssh -G was called for a custom profile: %v", err)
	}
}

func TestGetOpenSSHOnlyResolvesRequestedAlias(t *testing.T) {
	home, logPath := fakeSSH(t)
	writeTestFile(t, filepath.Join(home, ".ssh", "config"), "Host alpha beta gamma\n")
	store := New(filepath.Join(home, "profiles.json"))
	profile, err := store.Get(context.Background(), "openssh:beta")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "beta" || profile.Options.Host != "beta.example" {
		t.Fatalf("unexpected profile %+v", profile)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.Fields(string(data)); len(calls) != 1 || calls[0] != "beta" {
		t.Fatalf("unexpected ssh -G calls %q", calls)
	}
}

func TestLoadOpenSSHPreservesAliasOrder(t *testing.T) {
	home, _ := fakeSSH(t)
	writeTestFile(t, filepath.Join(home, ".ssh", "config"), "Host alpha beta gamma delta\n")
	profiles, err := loadOpenSSH(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 4 {
		t.Fatalf("got %d profiles", len(profiles))
	}
	for index, alias := range []string{"alpha", "beta", "gamma", "delta"} {
		if profiles[index].Name != alias {
			t.Fatalf("profile %d is %q, want %q", index, profiles[index].Name, alias)
		}
	}
}

func fakeSSH(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(home, "ssh-g-calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$2\" >> \"$SSH_G_LOG\"\nprintf 'hostname %s.example\\nuser testuser\\nport 22\\n' \"$2\"\n"
	writeTestFile(t, filepath.Join(bin, "ssh"), script)
	if err := os.Chmod(filepath.Join(bin, "ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("SSH_G_LOG", logPath)
	return home, logPath
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
