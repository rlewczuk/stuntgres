package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

// Resolution/diagnostics/held plain routes from TestQueryAliasesSmoke are
// already covered by QueryAliases*SourceConsumerRoutes, including Unicode/F1/F2.
// This scenario supplies its missing plain utility/view and control routes.
func TestReferenceQueryAliasesControlsSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			run(referenceExec("CREATE VIEW alias_view AS SELECT x.iid AS shown,x.a AS amount FROM t x(iid) WHERE x.b=1 ORDER BY shown", 0))
			s := referenceSelect("SELECT shown,amount FROM alias_view ORDER BY shown", []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			queryDistinctBoth(run, s)
			if b.pg {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/alias-destination")
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			exec := func(s referenceStep) {
				if !s.selectRows {
					s.nonContext = true
					referenceRunStep(t, p.ctx, db, nil, s)
					return
				}
				var rows *sql.Rows
				var err error
				if s.prepared {
					stmt, prepareErr := db.Prepare(s.query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					defer stmt.Close()
					rows, err = stmt.Query(s.args...)
				} else {
					rows, err = db.Query(s.query, s.args...)
				}
				stringCoreRetainedRows(t, rows, err, s.columns, s.types, s.rows, s.bag)
			}
			exec(referenceExec("CREATE TABLE sentinel(id INT)", 0))
			exec(referenceExec("INSERT INTO sentinel VALUES(17)", 1))
			old, err := db.Query("SELECT x.id AS retained FROM sentinel x")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			suffix := "CREATE VIEW invalid_alias AS SELECT t.id FROM t x;\n"
			invalid := append(append([]byte(nil), dump.Bytes()...), suffix...)
			err = stuntgres.Restore(p.ctx, db, bytes.NewReader(invalid))
			want := controlsReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "t"`)
			want.Hint = `Perhaps you meant to reference the table alias "x".`
			want.Position = referencePosition(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(suffix[:strings.Index(suffix, "t.id")]) + 1)
			referenceAssertDiagnostic(t, err, &want)
			check := func() {
				exec(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
			}
			check()
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			stringCoreRetainedRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
			queryDistinctBoth(exec, s)
			retained, err := db.Query(s.query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			stringCoreRetainedRows(t, retained, nil, s.columns, s.types, s.rows, false)
			exec(s)
		})
	}
}
