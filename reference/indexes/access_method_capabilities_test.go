package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessMethodCapabilities(t *testing.T) {
	amBackends(t, []string{"am_cap_hash", "am_cap_gist"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec(`CREATE ACCESS METHOD am_cap_hash TYPE INDEX HANDLER pg_catalog.hashhandler`, 0))
		run(fixture.Prepared(fixture.Exec(`CREATE ACCESS METHOD am_cap_gist TYPE INDEX HANDLER pg_catalog.gisthandler`, 0)))
		run(fixture.Exec(`CREATE TABLE am_cap_rows(n integer, payload integer)`, 0))
		run(fixture.Exec(`INSERT INTO am_cap_rows VALUES(7,8)`, 1))
		for _, name := range []string{"am_cap_hash", "am_cap_gist"} {
			oid := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_am WHERE amname=$1`, name)
			run(amCount(`SELECT count(*) FROM pg_opclass WHERE opcmethod=$1::oid`, 0, oid))
		}
		// No method-specific class exists. Native preserves its early ordering-
		// capability boundary; PG resolves the missing class before GiST options.
		// Both diagnostics must retain the declared, not physical, method name.
		for _, tc := range []struct {
			q, message       string
			pgMissingDefault bool
		}{
			{`CREATE UNIQUE INDEX am_cap_failed ON am_cap_rows USING am_cap_hash(n)`, `access method "am_cap_hash" does not support unique indexes`, false},
			{`CREATE INDEX am_cap_failed ON am_cap_rows USING am_cap_hash(n) INCLUDE(payload)`, `access method "am_cap_hash" does not support included columns`, false},
			{`CREATE INDEX am_cap_failed ON am_cap_rows USING am_cap_hash(n,payload)`, `access method "am_cap_hash" does not support multicolumn indexes`, false},
			{`CREATE UNIQUE INDEX am_cap_failed ON am_cap_rows USING am_cap_gist(n)`, `access method "am_cap_gist" does not support unique indexes`, false},
			{`CREATE INDEX am_cap_failed ON am_cap_rows USING am_cap_gist(n DESC)`, `access method "am_cap_gist" does not support ASC/DESC options`, true},
			{`CREATE INDEX am_cap_failed ON am_cap_rows USING am_cap_gist(n NULLS FIRST)`, `access method "am_cap_gist" does not support NULLS FIRST/LAST options`, true},
		} {
			t.Run(tc.q, func(t *testing.T) {
				run := func(s fixture.Step) {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: tc.message})
					if tc.pgMissingDefault {
						s.PostgresDiagnostic = &fixture.Diagnostic{
							Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704",
							Message: `data type integer has no default operator class for access method "am_cap_gist"`,
							Hint:    "You must specify an operator class for the index or define a default operator class for the data type.",
						}
						if b.PG {
							s.State = s.PostgresDiagnostic.Code
						}
					}
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_cap_failed'`, 0))
					run(amCount(`SELECT count(*) FROM pg_am WHERE amname IN ('am_cap_hash','am_cap_gist')`, 2))
					run(fixture.Select(`SELECT n,payload FROM am_cap_rows`, []string{"n", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				}
			})
		}
	})
}
