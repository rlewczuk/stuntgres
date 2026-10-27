package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablefuncCrosstabAuthority(t *testing.T) {
	t.Run("caller", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			crosstabSeed(t, p, b, c)
			run := func(s fixture.Step) {
				t.Helper()
				defer func() {
					if t.Failed() {
						t.Logf("authority query %q args %#v prepared=%v", s.Query, s.Args, s.Prepared)
					}
				}()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			run(fixture.Exec("CREATE ROLE ct_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ct_reader", 0))
			run(fixture.Exec("CREATE TABLE ct_secret(seq integer,k text,category text,v text)", 0))
			run(fixture.Exec("INSERT INTO ct_secret VALUES(1,'secret','x','private')", 1))
			q := "SELECT * FROM crosstab($1::text) AS t(k text,a text,b text)"
			source := "SELECT k,category,v FROM ct_secret ORDER BY seq"
			h := crosstabHeld(t, p, c, q)
			want := fixture.Select(q, []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"secret", "private", nil}}, source)
			run(fixture.Exec("SET ROLE ct_reader", 0))
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ct_secret")
			d.Context = "SQL statement \"" + source + "\""
			crosstabFailure(t, p, b, c, q, d, "execute", source)
			s := fixture.ErrorDiagnostic(q, d, source)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON ct_secret TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			run(want)
			run(fixture.Prepared(want))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE SELECT ON ct_secret FROM ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT SELECT ON ct_secret TO ct_reader", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION crosstab(text) FROM PUBLIC", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			execDenied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function crosstab")
			crosstabFailure(t, p, b, c, q, execDenied, "execute", nil)
			crosstabFailure(t, p, b, c, q, execDenied, "execute", source)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION crosstab(text) TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			run(fixture.Exec("RESET ROLE", 0))
			// Caller search_path, rather than installer search_path, selects inner SQL.
			run(fixture.Exec("CREATE SCHEMA ct_path", 0))
			run(fixture.Exec("CREATE TABLE ct_path.ct_secret(seq integer,k text,category text,v text)", 0))
			run(fixture.Exec("INSERT INTO ct_path.ct_secret VALUES(1,'path','x','caller')", 1))
			run(fixture.Exec("GRANT USAGE ON SCHEMA ct_path TO ct_reader", 0))
			run(fixture.Exec("GRANT SELECT ON ct_path.ct_secret TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			run(fixture.Exec("SET search_path TO ct_path,public", 0))
			pathWant := want
			pathWant.Rows = [][]any{{"path", "caller", nil}}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, pathWant)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET search_path TO public", 0))
			// Current transaction writes are visible and rollback does not cache them.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO ct_secret VALUES(2,'secret','y','transaction')", 1))
			txWant := want
			txWant.Rows = [][]any{{"secret", "private", "transaction"}}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, txWant)
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			run(fixture.Exec("UPDATE ct_secret SET v='new' WHERE seq=1", 1))
			want.Rows = [][]any{{"secret", "new", nil}}
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			// A source failure participates in the ordinary transaction error state.
			run(fixture.Exec("BEGIN", 0))
			bad := fixture.ControlsReferenceDiagnostic("42703", "column \"missing_ct\" does not exist")
			bad.InternalQuery, bad.InternalPosition = "SELECT missing_ct", fixture.Position(8)
			s = fixture.ErrorDiagnostic(q, bad, "SELECT missing_ct")
			s.SelectRows = true
			run(s)
			aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			s = fixture.ErrorDiagnostic("SELECT 1", aborted)
			s.SelectRows = true
			run(s)
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			// The ignored category may itself wait: observe inner execution before
			// canceling, join the worker, and prove the caller activation is usable.
			sleepSource := "SELECT k,pg_sleep(600),v FROM ct_secret ORDER BY seq"
			sleepQuery := "SELECT * FROM crosstab(" + crosstabQuote(sleepSource) + ") AS t(k text,a text,b text)"
			sleepHeld := crosstabHeld(t, p, c, sleepQuery)
			for _, prepared := range []bool{false, true} {
				fixture.SleepActiveCancelWithContext(t, p, b, c, sleepQuery, "SQL statement \""+sleepSource+"\"", nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = sleepHeld.Statement.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, sleepQuery)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			}
		})
	})
	t.Run("schema", func(t *testing.T) {
		crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) {
				t.Helper()
				defer func() {
					if t.Failed() {
						t.Logf("schema authority query %q args %#v prepared=%v", s.Query, s.Args, s.Prepared)
					}
				}()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			// Initial alternate placement exercises schema admission without
			// requiring unrelated extension relocation DDL.
			run(fixture.Exec("CREATE ROLE ct_reader", 0))
			run(fixture.Exec("CREATE SCHEMA ct_addon", 0))
			crosstabSeedInSchema(t, p, b, c, "ct_addon")
			run(fixture.Exec("SET ROLE ct_reader", 0))
			schemaDenied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ct_addon")
			schemaDenied.Position = fixture.Position(15)
			crosstabFailure(t, p, b, c, "SELECT * FROM ct_addon.crosstab(NULL::text) AS t(k text,a text)", schemaDenied, "prepare")
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA ct_addon TO ct_reader", 0))
			run(fixture.Exec("SET ROLE ct_reader", 0))
			run(fixture.Select("SELECT * FROM ct_addon.crosstab(NULL::text) AS t(k text,a text)", []string{"k", "a"}, []string{"TEXT", "TEXT"}, nil))
			run(fixture.Exec("RESET ROLE", 0))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := c.QueryContext(ctx, "SELECT * FROM ct_addon.crosstab2($1::text)", crosstabSource)
				if rows != nil {
					rows.Close()
					t.Error("pre-canceled query exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled query", err)
				}
			}
		})
	})
}
