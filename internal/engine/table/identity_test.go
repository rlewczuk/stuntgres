package table

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOwnedSequenceNameTruncatesToValidUTF8(t *testing.T) {
	name := ownedSequenceName(strings.Repeat("界", 21), strings.Repeat("列", 21), func(string) bool { return false })
	if len(name) > 63 || !utf8.ValidString(name) {
		t.Fatal("invalid UTF8 sequence name", name)
	}
}
