package sshconn

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tindomiel/internal/configstore"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type authConfig struct {
	methods     []ssh.AuthMethod
	closeAgent  func() error
	diagnostics error
}

func Dial(profile configstore.Profile, hostKeys *HostKeys) (*ssh.Client, error) {
	address := net.JoinHostPort(profile.Options.Host, strconv.Itoa(profile.Options.Port))
	slog.Info("ssh_dial_start", "address", address, "user", profile.Options.User, "auth", profile.Options.Auth, "profile", profile.Name)
	auth := authMethods(profile.Options)
	if len(auth.methods) == 0 {
		slog.Error("ssh_auth_methods_missing", "address", address, "error", auth.diagnostics)
		return nil, auth.diagnostics
	}
	if auth.closeAgent != nil {
		defer auth.closeAgent()
	}
	config := &ssh.ClientConfig{
		User:            profile.Options.User,
		Auth:            auth.methods,
		HostKeyCallback: hostKeys.Verify,
		Timeout:         12 * time.Second,
	}
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		slog.Error("ssh_dial_failed", "address", address, "error", err)
		if auth.diagnostics != nil {
			return nil, fmt.Errorf("connect %s: %w; private key diagnostics: %v", address, err, auth.diagnostics)
		}
		return nil, fmt.Errorf("connect %s: %w", address, err)
	}
	slog.Info("ssh_dial_success", "address", address)
	return client, nil
}

func authMethods(options configstore.Options) authConfig {
	var methods []ssh.AuthMethod
	var agentConnection net.Conn
	var signers []ssh.Signer
	var keyErrors []error
	if options.Auth == "publicKey" {
		if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
			connection, err := net.DialTimeout("unix", socket, 2*time.Second)
			if err == nil {
				client := agent.NewClient(connection)
				agentSigners, signersErr := client.Signers()
				if signersErr == nil && len(agentSigners) > 0 {
					agentConnection = connection
					methods = append(methods, ssh.PublicKeysCallback(client.Signers))
				} else {
					connection.Close()
				}
			}
		}
		for _, name := range options.PrivateKeys {
			path, err := expandHome(name)
			if err != nil {
				keyErrors = append(keyErrors, err)
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				keyErrors = append(keyErrors, err)
				continue
			}
			var signer ssh.Signer
			if options.PrivateKeyPassphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(options.PrivateKeyPassphrase))
			} else {
				signer, err = ssh.ParsePrivateKey(data)
			}
			if err != nil {
				keyErrors = append(keyErrors, fmt.Errorf("parse %s: %w", path, err))
				continue
			}
			signers = append(signers, signer)
		}
		if len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
	}
	if options.Password != "" {
		methods = append(methods, ssh.Password(options.Password), ssh.KeyboardInteractive(passwordAnswers(options.Password)))
	}
	diagnostics := errors.Join(keyErrors...)
	if len(methods) == 0 {
		if agentConnection != nil {
			agentConnection.Close()
		}
		if diagnostics != nil {
			return authConfig{diagnostics: fmt.Errorf("no usable SSH authentication: %w", diagnostics)}
		}
		return authConfig{diagnostics: errors.New("no SSH agent, private key, or password available")}
	}
	auth := authConfig{methods: methods, diagnostics: diagnostics}
	if agentConnection != nil {
		auth.closeAgent = agentConnection.Close
	}
	return auth
}

func passwordAnswers(password string) ssh.KeyboardInteractiveChallenge {
	return func(_, _ string, questions []string, _ []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for index := range answers {
			answers[index] = password
		}
		return answers, nil
	}
}

func expandHome(name string) (string, error) {
	if name == "~" || strings.HasPrefix(name, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(name, "~/")), nil
	}
	return name, nil
}
