package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func copyBinaryOptionOutcome(t *testing.T, name, direction, query string, result copyTextResult, err error, output []byte) {
	t.Helper()
	want, ok := copyBinaryOutcomeRecords["Options/"+name+"_"+direction]
	if !ok {
		t.Fatalf("unobserved option case %s/%s; pinned discovery required", name, direction)
	}
	if want.diagnostic != nil {
		diagnostic := *want.diagnostic
		// Positions come from actual SQL tokens; no fixture-specific global offset.
		token := ""
		switch name {
		case "duplicate_format":
			token = "FORMAT text"
		case "duplicate_header":
			token = "HEADER false"
		case "unknown_option":
			token = "imaginary true"
		case "on_error_stop", "on_error_ignore":
			if direction == "TO" {
				token = "ON_ERROR"
			}
		}
		if token != "" {
			offset := strings.LastIndex(query, token)
			if offset < 0 {
				t.Fatalf("diagnostic token %q absent from %q", token, query)
			}
			diagnostic.Position = strconv.Itoa(offset + 1)
		}
		copyTextDiagnostic(t, result, err, diagnostic)
		if len(output) != 0 {
			t.Errorf("rejected option wrote bytes=%x", output)
		}
		return
	}
	copyTextSuccess(t, result, err, want.count)
	if direction == "TO" && !bytes.Equal(output, copyBinaryStream()) {
		t.Errorf("empty export=%x want=%x", output, copyBinaryStream())
	}
}
