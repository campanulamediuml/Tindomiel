package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

func runShellCommand(ctx context.Context, client *ssh.Client, command string) ([]byte, error) {
	remote, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer remote.Close()
	stdin, err := remote.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := remote.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := remote.StderrPipe()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 0, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := remote.RequestPty("dumb", 32, 120, modes); err != nil {
		return nil, err
	}
	if err := remote.Shell(); err != nil {
		return nil, err
	}
	marker := sessionID()
	slog.Debug("shell_command_ready", "marker", marker)
	payload := "function prompt { '' }\recho __TINDOMIEL_BEGIN_" + marker + "\r" + command + "\recho __TINDOMIEL_END_" + marker + "\rexit\r"
	if _, err := stdin.Write([]byte(payload)); err != nil {
		return nil, err
	}
	slog.Debug("shell_command_payload_written", "marker", marker)

	var stdoutData, stderrData bytes.Buffer
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		_, _ = io.Copy(&stdoutData, stdout)
	}()
	go func() {
		defer readers.Done()
		_, _ = io.Copy(&stderrData, stderr)
	}()
	done := make(chan error, 1)
	go func() { done <- remote.Wait() }()
	select {
	case <-ctx.Done():
		_ = remote.Close()
		return nil, ctx.Err()
	case err := <-done:
		readers.Wait()
		if err != nil {
			return nil, err
		}
	}
	slog.Debug("shell_command_finished", "marker", marker)
	output := stripANSISequences(append(stdoutData.Bytes(), stderrData.Bytes()...))
	slog.Debug("shell_command_output_preview", "marker", marker, "output", fmt.Sprintf("%q", output[:min(200, len(output))]))
	tail := max(0, len(output)-500)
	slog.Debug("shell_command_output_tail", "marker", marker, "output", fmt.Sprintf("%q", output[tail:]))
	startMarker := []byte("__TINDOMIEL_BEGIN_" + marker + "\r\n")
	start := bytes.LastIndex(output, startMarker)
	if start < 0 {
		startMarker = []byte("__TINDOMIEL_BEGIN_" + marker + "\n")
		start = bytes.LastIndex(output, startMarker)
	}
	if start < 0 {
		return nil, errors.New("interactive command start marker not found")
	}
	start += len(startMarker)
	endMarker := []byte("__TINDOMIEL_END_" + marker)
	end := bytes.Index(output[start:], endMarker)
	if end < 0 {
		return nil, errors.New("interactive command end marker not found")
	}
	result := bytes.TrimSpace(output[start : start+end])
	slog.Debug("shell_command_result_preview", "marker", marker, "output", fmt.Sprintf("%q", result[:min(200, len(result))]))
	return result, nil
}

var base64ResultPattern = regexp.MustCompile(`(?m)^__TINDOMIEL_B64__([0-9]+)/([0-9]+)\|([A-Za-z0-9+/=]+)\r?$`)

func runPowerShellEncodedResult(ctx context.Context, client *ssh.Client, script string) ([]byte, error) {
	wrapped := `$text = (& { ` + script + ` } | Out-String -Width 4096);$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($text));$size = 80;$count = [int][Math]::Ceiling($encoded.Length / $size);for ($index = 0; $index -lt $count; $index++) { $start = $index * $size;$length = [Math]::Min($size, $encoded.Length - $start);"__TINDOMIEL_B64__$index/$count|$($encoded.Substring($start, $length))" }`
	output, nativeErr := runShellCommand(ctx, client, wrapped)
	if nativeErr == nil {
		data, err := decodePowerShellResult(output)
		if err == nil {
			return data, nil
		}
		nativeErr = err
	}
	output, err := runShellCommand(ctx, client, powershellCommand(wrapped))
	if err != nil {
		return nil, fmt.Errorf("PowerShell shell: %v; cmd shell: %w", nativeErr, err)
	}
	data, err := decodePowerShellResult(output)
	if err != nil {
		return nil, fmt.Errorf("PowerShell shell: %v; cmd result: %w", nativeErr, err)
	}
	return data, nil
}

func decodePowerShellResult(output []byte) ([]byte, error) {
	matches := base64ResultPattern.FindAllSubmatch(output, -1)
	if len(matches) == 0 {
		return nil, errors.New("PowerShell returned no base64 result")
	}
	total := -1
	chunks := make(map[int][]byte, len(matches))
	for _, match := range matches {
		index, indexErr := strconv.Atoi(string(match[1]))
		count, countErr := strconv.Atoi(string(match[2]))
		if indexErr != nil || countErr != nil || count <= 0 || index < 0 || index >= count || (total >= 0 && count != total) {
			return nil, errors.New("invalid PowerShell result chunk numbering")
		}
		if _, exists := chunks[index]; exists {
			return nil, errors.New("duplicate PowerShell result chunk")
		}
		total = count
		chunks[index] = match[3]
	}
	if total == 0 || len(chunks) != total {
		return nil, errors.New("incomplete PowerShell result chunks")
	}
	var encoded bytes.Buffer
	for index := 0; index < total; index++ {
		chunk, exists := chunks[index]
		if !exists {
			return nil, errors.New("missing PowerShell result chunk")
		}
		encoded.Write(chunk)
	}
	data, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		return nil, fmt.Errorf("invalid base64 PowerShell result: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("PowerShell result is not valid UTF-8")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	slog.Debug("powershell_result_decoded", "chunks", total, "bytes", len(data))
	return stripControlBytes(data), nil
}

func stripControlBytes(data []byte) []byte {
	output := make([]byte, 0, len(data))
	for _, value := range data {
		if value < 0x20 && value != '\t' && value != '\r' && value != '\n' {
			continue
		}
		output = append(output, value)
	}
	return output
}

func stripANSISequences(data []byte) []byte {
	output := make([]byte, 0, len(data))
	for index := 0; index < len(data); {
		if data[index] != 0x1b {
			output = append(output, data[index])
			index++
			continue
		}
		index++
		if index >= len(data) {
			break
		}
		switch data[index] {
		case '[':
			index++
			for index < len(data) {
				value := data[index]
				index++
				if value >= 0x40 && value <= 0x7e {
					break
				}
			}
		case ']':
			index++
			for index < len(data) {
				if data[index] == 0x07 {
					index++
					break
				}
				if data[index] == 0x1b && index+1 < len(data) && data[index+1] == '\\' {
					index += 2
					break
				}
				index++
			}
		}
	}
	return output
}
