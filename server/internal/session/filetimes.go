package session

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Session) FileCreationTimes(remotePaths []string) (map[string]int64, error) {
	result := make(map[string]int64, len(remotePaths))
	if len(remotePaths) == 0 {
		return result, nil
	}
	client, err := s.Client()
	if err != nil {
		return nil, err
	}
	s.fileTimesMu.RLock()
	osName := s.fileTimesOS
	s.fileTimesMu.RUnlock()
	windows := osName == "windows" || (osName == "" && bytes.Contains(client.Conn.ServerVersion(), []byte("OpenSSH_for_Windows")))
	darwin := osName == "darwin"
	if osName == "" && !windows {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		output, unameErr := s.exec(ctx, "uname -s")
		cancel()
		system := bytes.ToLower(output)
		darwin = unameErr == nil && bytes.Contains(system, []byte("darwin"))
		if unameErr == nil {
			switch {
			case darwin:
				osName = "darwin"
			case bytes.Contains(system, []byte("linux")):
				osName = "linux"
			}
			if osName != "" {
				s.fileTimesMu.Lock()
				if s.fileTimesOS == "" {
					s.fileTimesOS = osName
				}
				s.fileTimesMu.Unlock()
			}
		}
	}
	for start := 0; start < len(remotePaths); start += 50 {
		end := min(start+50, len(remotePaths))
		batch := remotePaths[start:end]
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var output []byte
		switch {
		case windows:
			output, err = s.windowsCreationTimes(ctx, batch)
		case darwin:
			output, err = s.darwinCreationTimes(ctx, batch)
		default:
			output, err = s.linuxCreationTimes(ctx, batch)
		}
		cancel()
		if err != nil {
			return nil, err
		}
		values := numericValues(output)
		if len(values) != len(batch) {
			return nil, fmt.Errorf("unexpected creation time response: got %d values for %d files", len(values), len(batch))
		}
		for index, seconds := range values {
			if seconds > 0 {
				result[batch[index]] = seconds
			}
		}
	}
	return result, nil
}

func (s *Session) linuxCreationTimes(ctx context.Context, remotePaths []string) ([]byte, error) {
	return s.exec(ctx, `stat --printf='%W\0' -- `+quoteRemotePaths(remotePaths, " "))
}

func (s *Session) darwinCreationTimes(ctx context.Context, remotePaths []string) ([]byte, error) {
	return s.exec(ctx, `stat -f '%B' -- `+quoteRemotePaths(remotePaths, " ")+` | tr '\n' '\0'`)
}

func (s *Session) windowsCreationTimes(ctx context.Context, remotePaths []string) ([]byte, error) {
	nativePaths := make([]string, len(remotePaths))
	for index, remotePath := range remotePaths {
		nativePaths[index] = powerShellNativePath(remotePath)
	}
	script := `[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false);$paths=@(` + quoteRemotePaths(nativePaths, ",") + `);foreach($remotePath in $paths){$item=Get-Item -LiteralPath $remotePath -ErrorAction SilentlyContinue;if($null -eq $item){[Console]::Write('0'+[char]0)}else{[int64]([DateTimeOffset]$item.CreationTimeUtc).ToUnixTimeSeconds();[Console]::Write([char]0)}}`
	return s.exec(ctx, powershellCommand(script))
}

func numericValues(output []byte) []int64 {
	var result []int64
	for _, value := range bytes.Split(output, []byte{0}) {
		text := strings.TrimSpace(string(value))
		if text == "" {
			continue
		}
		valid := true
		for _, r := range text {
			if r < '0' || r > '9' {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		seconds, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			result = append(result, seconds)
		}
	}
	return result
}

func quoteRemotePaths(remotePaths []string, separator string) string {
	quoted := make([]string, len(remotePaths))
	for index, remotePath := range remotePaths {
		quoted[index] = `'` + strings.ReplaceAll(remotePath, `'`, `'"'"'`) + `'`
	}
	return strings.Join(quoted, separator)
}
