package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"tindomiel/internal/configstore"
	"tindomiel/internal/httpapi"
	"tindomiel/internal/logging"
	"tindomiel/internal/session"
	"tindomiel/internal/sshconn"
)

func main() {
	defaultAddress := envOr("TINDOMIEL_ADDR", "127.0.0.1:0")
	address := flag.String("addr", defaultAddress, "loopback listen address")
	staticDir := flag.String("static", os.Getenv("TINDOMIEL_STATIC"), "directory containing the built web frontend")
	flag.Parse()

	token := os.Getenv("TINDOMIEL_TOKEN")
	if token == "" {
		token = randomToken()
	}
	dataPath := envOr("TINDOMIEL_DATA", filepath.Join("data", "profiles.json"))
	logPath := envOr("TINDOMIEL_LOG", filepath.Join(filepath.Dir(dataPath), "logs", "tindomiel.log"))
	logger, closeLog, err := logging.Setup(logPath, os.Getenv("TINDOMIEL_LOG_LEVEL"))
	if err != nil {
		log.Fatal(err)
	}
	slog.SetDefault(logger)
	defer closeLog()

	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("user_home_failed", "error", err)
		return
	}
	knownHosts := envOr("TINDOMIEL_KNOWN_HOSTS", filepath.Join(home, ".ssh", "known_hosts"))
	hostKeys, err := sshconn.NewHostKeys(knownHosts)
	if err != nil {
		slog.Error("known_hosts_load_failed", "error", err)
		return
	}
	profiles := configstore.New(dataPath)
	manager := session.NewManager(profiles, hostKeys)
	api := httpapi.New(token, *staticDir, profiles, manager, hostKeys, logPath)

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		slog.Error("listen_failed", "address", *address, "error", err)
		return
	}
	server := &http.Server{
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	slog.Info("server_listening", "address", listener.Addr().String(), "logPath", logPath)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"url":   "http://" + listener.Addr().String(),
		"token": token,
	})

	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		slog.Info("server_stopping")
		manager.CloseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server_failed", "error", err)
		return
	}
	slog.Info("server_stopped")
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func randomToken() string {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
