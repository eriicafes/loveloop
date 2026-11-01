package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplit(t *testing.T) {
	text := strings.Repeat("love 💞 ", 1500)
	chunks := Split(text, MaxMessageLength)
	if strings.Join(chunks, "") != text {
		t.Fatal("chunks do not add up to the text")
	}
	for _, chunk := range chunks {
		if len(chunk) > MaxMessageLength || !utf8.ValidString(chunk) {
			t.Fatalf("bad chunk of %d bytes", len(chunk))
		}
	}
}
