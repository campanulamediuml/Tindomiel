package session

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

func powershellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for index, value := range encoded {
		data[index*2] = byte(value)
		data[index*2+1] = byte(value >> 8)
	}
	return "powershell -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(data)
}

func powerShellScript(command string) (string, bool) {
	const prefix = "powershell -NoProfile -NonInteractive -EncodedCommand "
	if !strings.HasPrefix(command, prefix) {
		return "", false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(command, prefix))
	if err != nil || len(data)%2 != 0 {
		return "", false
	}
	encoded := make([]uint16, len(data)/2)
	for index := range encoded {
		encoded[index] = uint16(data[index*2]) | uint16(data[index*2+1])<<8
	}
	return string(utf16.Decode(encoded)), true
}

func powerShellNativePath(remotePath string) string {
	if len(remotePath) >= 3 && remotePath[0] == '/' && remotePath[2] == ':' {
		return remotePath[1:]
	}
	return remotePath
}
