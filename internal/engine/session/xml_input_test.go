package session

import (
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestXMLInputDiagnosticOwnership(t *testing.T) {
	op := &Operation{NoticeThreshold: LevelWarning}
	input := XMLInput{Op: op}
	original := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2200N", Message: "invalid XML content", Detail: "owned source detail"}
	literal := input.XMLLiteral(31)
	got := literal.XMLInputDiagnostic(original)
	if got.Position != "31" || got.Context != "" || original.Position != "" {
		t.Fatal("literal provenance not copied", got, original)
	}
	for _, tc := range []struct {
		empty   bool
		context string
	}{{false, "unnamed portal parameter $2 = '...'"}, {true, "unnamed portal parameter $2 = ''"}} {
		parameter := input.XMLParameter(2, tc.empty)
		got = parameter.XMLInputDiagnostic(original)
		if got.Context != tc.context || got.Position != "" {
			t.Fatal("parameter provenance", got)
		}
		warning := original
		warning.Severity, warning.SeverityUnlocalized, warning.Code = "WARNING", "WARNING", "01000"
		parameter.XMLInputWarning(warning)
		notices := op.DetachNotices()
		if len(notices) != 1 || notices[0].Context != tc.context || warning.Context != "" {
			t.Fatal("warning ownership", notices, warning)
		}
	}
	got = input.XMLBinaryParameter(3).XMLInputDiagnostic(original)
	if got.Context != "unnamed portal parameter $3" {
		t.Fatal(got)
	}
	got = input.XMLInputDiagnostic(original)
	if got != original {
		t.Fatal("runtime input inherited literal/portal provenance", got)
	}
	op.NoticeThreshold = LevelError
	literal.XMLInputWarning(original)
	if len(op.DetachNotices()) != 0 {
		t.Fatal("warning threshold ignored")
	}
}

func TestXMLInputNamedPortal(t *testing.T) {
	op := &Operation{NoticeThreshold: LevelWarning}
	adapter := (XMLInput{Op: op}).WithPortal("xml_portal").XMLParameter(2, false)
	adapter.XMLInputWarning(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "provider event"})
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Context != `portal "xml_portal" parameter $2 = '...'` {
		t.Fatal(notices)
	}
}
