package fixture

import "testing"

// SQL notice witnesses remain exact. PostgreSQL debug tracing is incidental:
// allocator identities, trace text and loader ordering are not SQL contracts.
func NoticeWitnesses(t *testing.T, postgres bool, records []Diagnostic, want Diagnostic) []Diagnostic {
	t.Helper()
	var witnesses []Diagnostic
	for _, record := range records {
		if record.Code == want.Code && record.Message == want.Message {
			witnesses = append(witnesses, record)
			continue
		}
		if !postgres {
			t.Fatalf("unexpected non-witness diagnostic: %#v (witness %#v)", record, want)
		}
		NoticesInternalDebug(t, record)
	}
	return witnesses
}

// Distinguish internal debug tracing from unexpected SQL notices without
// asserting PostgreSQL implementation text, counts or execution ordering.
func NoticesInternalDebug(t *testing.T, got Diagnostic) {
	t.Helper()
	got.Message = ""
	want := Diagnostic{Severity: "DEBUG", SeverityUnlocalized: "DEBUG", Code: "00000"}
	if got != want {
		t.Fatalf("unexpected non-witness diagnostic: %#v", got)
	}
}
