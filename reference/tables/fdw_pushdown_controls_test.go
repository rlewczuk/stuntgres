package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownControls(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		q := "SELECT l.k,count(*) AS n FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k ORDER BY l.k LIMIT 1"
		fdwPushdownValue(run, q, []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false)
		fdwSourceExec(t, p, b, r, run, `CREATE TABLE fdw_source.push_bytes(t text COLLATE "C")`, 0)
		fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source.push_bytes VALUES('alpha'),('beta')`, 2)
		run(fixture.Exec(`CREATE FOREIGN TABLE fdw_push_bytes(t text COLLATE "C") SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'push_bytes')`, 0))
		textQuery := "SELECT t FROM fdw_push_bytes ORDER BY t LIMIT 2"
		fdwPushdownValue(run, textQuery, []string{"t"}, []string{"TEXT"}, [][]any{{"alpha"}, {"beta"}}, false)
		var dump bytes.Buffer
		var held fdwHeldResult
		if !b.PG {
			held = fdwHeld(t, p, b, c, q)
			releaseBytes := fdwBorrowedBytes(r)
			textHeld := fdwHeld(t, p, b, c, textQuery)
			releaseBytes()
			before := r.snapshot()
			r.setHook(func(context.Context, string) error { return errors.New("persistence invoked provider") })
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			r.setHook(nil)
			if dump.Len() == 0 || before != r.snapshot() {
				t.Fatal("Dump invoked execution or emitted no definitions")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(q, []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}))
			fixture.RunStep(t, p.Ctx, textHeld, textHeld.recorder, fixture.Select(textQuery, []string{"t"}, []string{"TEXT"}, [][]any{{"alpha"}, {"beta"}}))
		}
		fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source."Left input" VALUES(5,50)`, 1)
		fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left", []string{"count"}, []string{"INT8"}, [][]any{{int64(6)}}, false)
		fdwPushdownValue(run, q, []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false)
		if b.PG {
			return
		}
		// A destination may have only mandatory scan methods. Restore persists
		// declarations, not the source's optional plans or registered Go objects.
		d := stuntgres.NewDriver()
		for _, name := range []string{"fdw_sql_handler", "fdw_scan_only_handler"} {
			if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: name}, Wrapper: fdwScanOnly{r}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_sql_validator"}, Validate: r.validate}); err != nil {
			t.Fatal(err)
		}
		connector, err := d.OpenConnector("fdw-push-restored")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		var n int64
		if err := destination.QueryRowContext(p.Ctx, "SELECT count(*) FROM fdw_push_left").Scan(&n); err != nil || n != 6 {
			t.Fatal("restore serialized remote rows or optional execution", n, err)
		}
		fdwConfiguredNamespaceControls(t, p.Ctx, r)
	})
}
