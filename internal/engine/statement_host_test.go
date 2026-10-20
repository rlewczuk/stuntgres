package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestStatementHostNilScopeIsUntypedNil(t *testing.T) {
	if host := hostOf(nil); host != nil {
		t.Fatalf("hostOf(nil) = %#v, want untyped nil", host)
	}
	if host := (&EvalContext{}).LiveHost(); host != nil {
		t.Fatalf("scope-less EvalContext host = %#v, want untyped nil", host)
	}
	if host := (&querySource{}).LiveHost(); host != nil {
		t.Fatalf("scope-less querySource host = %#v, want untyped nil", host)
	}
	if scope := scopeOf(nil); scope != nil {
		t.Fatalf("scopeOf(nil) = %p, want nil", scope)
	}
}

func TestStatementHostRoundTrip(t *testing.T) {
	scope := &StatementScope{}
	host := hostOf(scope)
	if host == nil {
		t.Fatal("hostOf(scope) = nil")
	}
	if got := scopeOf(host); got != scope {
		t.Fatalf("scopeOf(hostOf(scope)) = %p, want %p", got, scope)
	}
}

// A statement without a live scope keeps the missing-snapshot diagnostics the
// scope formatters report, whichever way the output environment is wired.
func TestStatementHostDetachedPresentation(t *testing.T) {
	cases := []struct {
		name   string
		format func(presentationFormatters) (string, error)
		want   string
	}{
		{"regclass", func(p presentationFormatters) (string, error) { return p.FormatRegclass(1) }, "stuntgres: missing regclass output snapshot"},
		{"regtype", func(p presentationFormatters) (string, error) { return p.FormatRegtype(1) }, "stuntgres: missing regtype output snapshot"},
		{"regproc", func(p presentationFormatters) (string, error) { return p.FormatRegproc(1) }, "stuntgres: missing regproc output snapshot"},
		{"regprocedure", func(p presentationFormatters) (string, error) { return p.FormatRegprocedure(1) }, "stuntgres: missing regprocedure output snapshot"},
		{"regrole", func(p presentationFormatters) (string, error) { return p.FormatRegrole(1) }, "stuntgres: missing regrole output snapshot"},
		{"regoper", func(p presentationFormatters) (string, error) { return p.FormatRegoper(1) }, "stuntgres: missing operator output snapshot"},
		{"regoperator", func(p presentationFormatters) (string, error) { return p.FormatRegoperator(1) }, "stuntgres: missing operator output snapshot"},
		{"regcollation", func(p presentationFormatters) (string, error) { return p.FormatRegcollation(1) }, "stuntgres: missing regcollation output snapshot"},
		{"regnamespace", func(p presentationFormatters) (string, error) { return p.FormatRegnamespace(1) }, "stuntgres: missing regnamespace output snapshot"},
		{"regconfig", func(p presentationFormatters) (string, error) { return p.FormatRegconfig(1) }, "stuntgres: missing text search output snapshot"},
		{"regdictionary", func(p presentationFormatters) (string, error) { return p.FormatRegdictionary(1) }, "stuntgres: missing text search output snapshot"},
		{"aclitem", func(p presentationFormatters) (string, error) { return p.FormatACLRole(&sqltype.ACLAuthority{}, 1) }, "stuntgres: missing ACL output snapshot"},
	}
	formatters := map[string]presentationFormatters{
		"detached":      detachedPresentation{},
		"nil scope":     (*StatementScope)(nil),
		"no snapshot":   &StatementScope{},
		"scope-less ev": (&EvalContext{}).Presentation(),
	}
	for _, tc := range cases {
		for name, p := range formatters {
			if tc.name == "regrole" && name == "no snapshot" {
				continue // An existing scope formats roles from its owner, not a snapshot.
			}
			text, err := tc.format(p)
			if err == nil || err.Error() != tc.want || text != "" {
				t.Errorf("%s via %s = (%q, %v), want error %q", tc.name, name, text, err, tc.want)
			}
		}
	}
}
