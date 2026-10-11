package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Alias admission changes the parse of the formerly rejected VALUES suffix.
// Full records and valid aliases were independently observed on pinned PG18.0.
func TestReferenceQueryJoinsAliasKeywords(t *testing.T) {
	p := queryJoinsReferenceSetup(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			const invalid = "INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)"
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "1"`, Position: "50"}
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepare"
				}
				t.Run(name, func(t *testing.T) {
					var err error
					if prepared {
						stmt, prepareErr := backend.DB.PrepareContext(p.Ctx, invalid)
						err = prepareErr
						if stmt != nil {
							stmt.Close()
							t.Fatal("invalid alias list exposed prepared handle")
						}
					} else {
						result, execErr := backend.DB.ExecContext(p.Ctx, invalid)
						err = execErr
						if result != nil {
							t.Fatal("invalid alias list exposed result")
						}
					}
					fixture.AssertDiagnostic(t, err, &want)
				})
			}
			for _, tc := range []struct{ query, column string }{
				{"SELECT values.v FROM t values", "v"},
				{"SELECT values.k FROM t values(k)", "k"},
				{"SELECT values.v FROM t AS values", "v"},
				{`SELECT "values".v FROM t "values"`, "v"},
			} {
				s := fixture.Select(tc.query, []string{tc.column}, []string{"INT4"}, [][]any{{int64(1)}})
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			}
		})
	}
}
