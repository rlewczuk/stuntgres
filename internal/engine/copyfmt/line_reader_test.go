package copyfmt

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestCopyTextEscapeUnits(t *testing.T) {
	raw := []byte(`\141\x62\q\12\x9`)
	if got := string(decodeField(nil, raw)); got != "abq\n\t" {
		t.Fatalf("decoded=%q", got)
	}
	if got := string(AppendText(nil, "x\ty\nz\r\\", '|')); got != `x\ty\nz\r\\` {
		t.Fatalf("escaped=%q", got)
	}
	if err := sqltype.ValidateTextInput("\x00"); err == nil {
		t.Fatal("NUL accepted")
	}
}
