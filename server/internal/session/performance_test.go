package session

import (
	"bytes"
	"testing"
)

func BenchmarkOSC7ParserPlainOutput(b *testing.B) {
	payload := bytes.Repeat([]byte("ordinary terminal output without path notifications\r\n"), 512)
	parser := &osc7Parser{}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		clean, _, found := parser.feed(payload)
		if found || len(clean) != len(payload) {
			b.Fatal("plain output changed")
		}
	}
}
