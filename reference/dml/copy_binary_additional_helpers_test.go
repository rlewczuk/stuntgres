package reference_test

import (
	"strings"
	"testing"
)

func copyBinaryAdditionalOutcome(t *testing.T, result copyTextResult, err error, output []byte, phase ...string) {
	t.Helper()
	key := strings.TrimSuffix(strings.TrimSuffix(t.Name(), "/postgres"), "/memory")
	if len(phase) != 0 {
		key += "/" + phase[0]
	}
	if want, ok := copyBinaryAdditionalDiagnostics[key]; ok {
		copyTextDiagnostic(t, result, err, want)
		return
	}
	switch key {
	case "TestReferenceCopyBinaryAdmission/unquoted_mixed_case",
		"TestReferenceCopyBinaryAdmission/quoted_lower_case",
		"TestReferenceCopyBinaryAdmission/column_acl_from_allowed":
		copyTextSuccess(t, result, err, 1)
	case "TestReferenceCopyBinaryAdmission/column_acl_to_allowed":
		copyTextSuccess(t, result, err, 1)
		want := copyBinaryStream(copyBinaryRow(copyBinary32(99)))
		if string(output) != string(want) {
			t.Fatalf("column privilege binary export=%x want=%x", output, want)
		}
	default:
		t.Fatalf("missing pinned additional expectation %q", key)
	}
}
