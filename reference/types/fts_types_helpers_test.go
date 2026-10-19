package reference_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsTypesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func ftsTypesQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ftsTypesScalar asserts the public raw-value and canonical-text contracts.
func ftsTypesScalar(t *testing.T, p *fixture.Pair, b fixture.Backend, typ, input string, want string) {
	t.Helper()
	var raw any
	err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::"+typ, input).Scan(&raw)
	if err != nil {
		t.Fatal(ftsTypesObserveError(t, err))
	}
	fixture.AssertRawValue(t, b.PG, strings.ToUpper(typ), raw)
	if got := fixture.Normalize(raw); got != want {
		t.Fatalf("%s input %q: output=%q want=%q", typ, input, got, want)
	}
}

func ftsTypesObserveError(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return "success"
	}
	var state interface{ SQLState() string }
	if !errors.As(err, &state) {
		return fmt.Sprintf("ordinary transport/adapter error %T: %q", err, err.Error())
	}
	return fmt.Sprintf("%#v", fixture.ExtractDiagnostic(t, err))
}
