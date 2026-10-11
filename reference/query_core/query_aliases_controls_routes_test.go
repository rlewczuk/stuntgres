package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Resolution/diagnostics/held plain routes from TestQueryAliasesSmoke are
// already covered by QueryAliases*SourceConsumerRoutes, including Unicode/F1/F2.
// This scenario supplies its missing plain utility/view and control routes.
func TestReferenceQueryAliasesControlsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			run(fixture.Exec("CREATE VIEW alias_view AS SELECT x.iid AS shown,x.a AS amount FROM t x(iid) WHERE x.b=1 ORDER BY shown", 0))
			s := fixture.Select("SELECT shown,amount FROM alias_view ORDER BY shown", []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			fixture.QueryDistinctBoth(run, s)
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/alias-destination")
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			exec := func(s fixture.Step) {
				if !s.SelectRows {
					s.NonContext = true
					fixture.RunStep(t, p.Ctx, db, nil, s)
					return
				}
				var rows *sql.Rows
				var err error
				if s.Prepared {
					stmt, prepareErr := db.Prepare(s.Query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					defer stmt.Close()
					rows, err = stmt.Query(s.Args...)
				} else {
					rows, err = db.Query(s.Query, s.Args...)
				}
				fixture.StringCoreRetainedRows(t, rows, err, s.Columns, s.Types, s.Rows, s.Bag)
			}
			exec(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
			exec(fixture.Exec("INSERT INTO sentinel VALUES(17)", 1))
			old, err := db.Query("SELECT x.id AS retained FROM sentinel x")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			suffix := "CREATE VIEW invalid_alias AS SELECT t.id FROM t x;\n"
			invalid := append(append([]byte(nil), dump.Bytes()...), suffix...)
			err = stuntgres.Restore(p.Ctx, db, bytes.NewReader(invalid))
			want := fixture.ControlsReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "t"`)
			want.Hint = `Perhaps you meant to reference the table alias "x".`
			want.Position = fixture.Position(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "t.id")]) + 1)
			fixture.AssertDiagnostic(t, err, &want)
			check := func() {
				exec(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
			}
			check()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.StringCoreRetainedRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
			fixture.QueryDistinctBoth(exec, s)
			retained, err := db.Query(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.Ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.StringCoreRetainedRows(t, retained, nil, s.Columns, s.Types, s.Rows, false)
			exec(s)
		})
	}
}
