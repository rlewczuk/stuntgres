package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Identity acceptance shares the same SQL/API body across native and PostgreSQL.
func domainIdentityScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	for _, schema := range []string{"di_src", "di_dst", "di_other", "di_missing"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			body(t, p, b, c, r, &notices)
		})
	}
}

func domainIdentityDDL(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, x, r, fixture.Exec(q, 0))
	}
}

// Pinned PostgreSQL18.6 utility diagnostics have empty optional fields and
// arise at Execute, including through actual driver-prepared statements.
func domainIdentityError(t *testing.T, p *fixture.Pair, c *sql.Conn, q, state, message string, prepared bool) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, prepareErr := c.PrepareContext(p.Ctx, q)
		if prepareErr != nil {
			t.Fatal("valid utility failed during Prepare", q, prepareErr)
		}
		defer s.Close()
		result, err = s.ExecContext(p.Ctx)
	} else {
		result, err = c.ExecContext(p.Ctx, q)
	}
	if result != nil {
		t.Fatal("failed utility published result", q)
	}
	want := fixture.ControlsReferenceDiagnostic(state, message)
	fixture.AssertDiagnostic(t, err, &want)
}

func domainIdentitySameOID(t *testing.T, p *fixture.Pair, x fixture.Executor, q string, want int64) {
	t.Helper()
	if got := domainAlterOID(t, p, x, q); got != want {
		t.Fatalf("identity %s=%d want %d", q, got, want)
	}
}

func domainIdentityNotices(t *testing.T, got *[]fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("notices=%#v want %#v", *got, want)
	}
	*got = nil
}
