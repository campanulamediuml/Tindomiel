package session

import (
	"bytes"
	"testing"
)

func TestOSC7ParserPlainOutputUsesInputDirectly(t *testing.T) {
	parser := &osc7Parser{}
	input := []byte("plain terminal output\r\n")
	output, _, found := parser.feed(input)
	if found || !bytes.Equal(output, input) {
		t.Fatalf("unexpected output %q", output)
	}
	if len(output) > 0 && &output[0] != &input[0] {
		t.Fatal("plain output was copied")
	}
}

func TestOSC7ParserHandlesPrefixAcrossReads(t *testing.T) {
	parser := &osc7Parser{}
	output, _, found := parser.feed([]byte("before\x1b]"))
	if found || string(output) != "before" {
		t.Fatalf("unexpected first chunk %q, found=%v", output, found)
	}
	output, cwd, found := parser.feed([]byte("7;/home/user\aafter"))
	if !found || cwd != "/home/user" || string(output) != "after" {
		t.Fatalf("unexpected second chunk %q, cwd=%q, found=%v", output, cwd, found)
	}
}
