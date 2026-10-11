package fixture

import (
	"testing"
)

func DomainReferenceNotice(t *testing.T, records *[]Diagnostic, message, detail string) {
	t.Helper()
	want := Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail}
	if len(*records) != 1 || (*records)[0] != want {
		t.Fatalf("domain notice got %#v want %#v", *records, want)
	}
	*records = nil
}
