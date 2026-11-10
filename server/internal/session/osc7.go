package session

import (
	"bytes"
)

var osc7Prefix = []byte{0x1b, ']', '7', ';'}

type osc7Parser struct {
	buffer []byte
}

func (p *osc7Parser) feed(data []byte) ([]byte, string, bool) {
	p.buffer = append(p.buffer, data...)
	output := make([]byte, 0, len(data))
	var cwd string
	var found bool
	for {
		start := bytes.Index(p.buffer, osc7Prefix)
		if start < 0 {
			keep := suffixPrefixLength(p.buffer, osc7Prefix)
			output = append(output, p.buffer[:len(p.buffer)-keep]...)
			p.buffer = append(p.buffer[:0], p.buffer[len(p.buffer)-keep:]...)
			return output, cwd, found
		}
		output = append(output, p.buffer[:start]...)
		end := bytes.IndexByte(p.buffer[start+len(osc7Prefix):], 0x07)
		if end < 0 {
			p.buffer = append(p.buffer[:0], p.buffer[start:]...)
			if len(p.buffer) > 8192 {
				p.buffer = p.buffer[1:]
			}
			return output, cwd, found
		}
		end += start + len(osc7Prefix)
		cwd = string(p.buffer[start+len(osc7Prefix) : end])
		found = true
		p.buffer = p.buffer[end+1:]
	}
}

func suffixPrefixLength(data, prefix []byte) int {
	maximum := min(len(prefix)-1, len(data))
	for size := maximum; size > 0; size-- {
		if bytes.Equal(data[len(data)-size:], prefix[:size]) {
			return size
		}
	}
	return 0
}

func shellIntegration() string {
	return `case "$SHELL" in *bash) PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }printf '\033]7;%s\007' \"\$PWD\"" ;; *zsh) precmd() { printf '\033]7;%s\007' "$PWD"; } ;; esac
stty echo
`
}
