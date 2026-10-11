//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryJoinsScopeDiagnosticsOracle(t *testing.T) { queryJoinsScopeDiagnostics(t, true) }
func TestReferenceQueryJoinsScopeDiagnostics(t *testing.T)       { queryJoinsScopeDiagnostics(t, false) }

func queryJoinsScopeDiagnostics(t *testing.T, oracle bool) {
	p := fixture.NewPair(t)
	queryJoinsReferenceSeed(t, p)
	// Independently observed pinned PostgreSQL records confirm that an alias
	// Hint requires a differing alias still visible in this namespace.
	for _, tc := range []struct{ query, label, entry, pos string }{
		{"SELECT a.k FROM (a AS x JOIN b USING(k)) j", "a", "x", "8"},
		{"SELECT x.k FROM (a AS x JOIN b USING(k)) j", "x", "x", "8"},
		{"SELECT * FROM a AS x, b JOIN c ON a.k=c.k", "a", "x", "35"},
		{"SELECT public.a.k FROM a AS a CROSS JOIN b", "a", "a", "8"},
	} {
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "` + tc.label + `"`, Detail: `There is an entry for table "` + tc.entry + `", but it cannot be referenced from this part of the query.`, Position: tc.pos}
		t.Run(tc.query, func(t *testing.T) {
			if !oracle {
				s := fixture.ImplicitError(tc.query, want)
				s.SelectRows, s.ErrorStage = true, "execute"
				p.Step(t, s)
				s.ErrorStage = "prepare"
				p.Step(t, fixture.Prepared(s))
				return
			}
			for _, prepared := range []bool{false, true} {
				name := "query"
				if prepared {
					name = "prepare"
				}
				t.Run(name, func(t *testing.T) {
					var err error
					if prepared {
						stmt, prepareErr := p.Postgres.PrepareContext(p.Ctx, tc.query)
						err = prepareErr
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid scope exposed prepared handle")
						}
					} else {
						rows, queryErr := p.Postgres.QueryContext(p.Ctx, tc.query)
						err = queryErr
						if rows != nil {
							rows.Close()
							t.Fatal("invalid scope exposed rows")
						}
					}
					if err == nil {
						t.Fatal("invalid scope accepted")
					}
					diagnostic := fixture.ExtractDiagnostic(t, err)
					t.Logf("JOIN SCOPE ORACLE %s %q: %#v", name, tc.query, diagnostic)
					fixture.AssertDiagnostic(t, err, &want)
				})
			}
		})
	}
}
