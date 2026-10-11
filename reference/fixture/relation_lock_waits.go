package fixture

import (
	"testing"
)

func WaitsDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	want := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if got := ExtractDiagnostic(t, err); got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
	State(t, err, code)
}
