package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeLogicalSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB
			run := func(x fixture.Executor, step fixture.Step) {
				t.Helper()
				var retained []*sql.ColumnType
				if step.SelectRows && step.Diagnostic == nil {
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						var err error
						retained, err = rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						fixture.ScalarAssertMetadata(t, backend.PG, retained)
						for i, typ := range step.Types {
							if typ == "BOOL" && step.Columns[i] == "?column?" {
								if _, known := retained[i].Length(); known {
									t.Fatal("logical length")
								}
								if _, _, known := retained[i].DecimalSize(); known {
									t.Fatal("logical precision")
								}
							}
						}
					}
				}
				fixture.RunStep(t, ctx, x, backend.Recorder, step)
				if retained != nil {
					fixture.ScalarAssertDescription(t, retained, step.Columns, step.Types)
				}
			}
			exec := func(q string, count int64) { run(db, fixture.Exec(q, count)) }
			query := func(q string, names, types []string, want [][]any, bag bool, args ...any) {
				step := fixture.Select(q, names, types, want, args...)
				step.Bag = bag
				run(db, step)
				run(db, fixture.Prepared(step))
			}
			exec("CREATE TABLE t(id INT,a BOOL,b BOOL,s TEXT)", 0)
			exec("INSERT INTO t VALUES(1,TRUE,TRUE,'yes'),(2,TRUE,FALSE,'yes'),(3,TRUE,NULL,'yes'),(4,FALSE,TRUE,'yes'),(5,FALSE,FALSE,'yes'),(6,FALSE,NULL,'yes'),(7,NULL,TRUE,'yes'),(8,NULL,FALSE,'yes'),(9,NULL,NULL,'yes')", 9)
			exec("CREATE TABLE one(id INT)", 0)
			exec("INSERT INTO one VALUES(1)", 1)
			exec("CREATE TABLE empty(id INT,a BOOL,b BOOL,s TEXT)", 0)
			pairs := []struct {
				a, b                 string
				av, bv, and, or, not any
			}{
				{"TRUE", "TRUE", true, true, true, true, false}, {"TRUE", "FALSE", true, false, false, true, false}, {"TRUE", "NULL", true, nil, nil, true, false},
				{"FALSE", "TRUE", false, true, false, true, true}, {"FALSE", "FALSE", false, false, false, false, true}, {"FALSE", "NULL", false, nil, false, nil, true},
				{"NULL", "TRUE", nil, true, nil, true, nil}, {"NULL", "FALSE", nil, false, false, nil, nil}, {"NULL", "NULL", nil, nil, nil, nil, nil},
			}
			for _, p := range pairs {
				for _, op := range []struct {
					name  string
					value any
				}{{"AND", p.and}, {"OR", p.or}} {
					expr := p.a + " " + op.name + " " + p.b
					query("SELECT "+expr+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{op.value}}, false)
					want := [][]any{}
					if op.value == true {
						want = [][]any{{int64(1)}}
					}
					query("SELECT id FROM one WHERE "+expr, []string{"id"}, []string{"INT4"}, want, false)
				}
			}
			for _, p := range []struct {
				input string
				value any
			}{{"TRUE", false}, {"FALSE", true}, {"NULL", nil}} {
				query("SELECT NOT "+p.input+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{p.value}}, false)
				want := [][]any{}
				if p.value == true {
					want = [][]any{{int64(1)}}
				}
				query("SELECT id FROM one WHERE NOT "+p.input, []string{"id"}, []string{"INT4"}, want, false)
			}
			columnWant := [][]any{{int64(1), true, true, false}, {int64(2), false, true, false}, {int64(3), nil, true, false}, {int64(4), false, true, true}, {int64(5), false, false, true}, {int64(6), false, nil, true}, {int64(7), nil, true, nil}, {int64(8), false, nil, nil}, {int64(9), nil, nil, nil}}
			query("SELECT id,a AND b,a OR b,NOT a FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, columnWant, false)
			for _, tc := range []struct {
				expr  string
				value any
			}{
				{"TRUE OR FALSE AND FALSE", true}, {"(TRUE OR FALSE) AND FALSE", false}, {"NOT TRUE AND FALSE", false}, {"NOT (TRUE AND FALSE)", true}, {"NOT NOT NULL", nil}, {"(NULL OR FALSE) AND TRUE", nil}, {"'yes' AND 'no'", false}, {"NOT 'yes'", false}, {"(TRUE)", true},
			} {
				query("SELECT "+tc.expr+" FROM one", []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.value}}, false)
			}
			query("SELECT id FROM t WHERE NOT id=1 AND (a OR b) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(7)}}, false)
			query("SELECT a AND b FROM empty", []string{"?column?"}, []string{"BOOL"}, [][]any{}, false)
			heldSQL := "SELECT $1 AND $2,$1 OR $2,NOT $1 FROM one"
			held, e := db.PrepareContext(ctx, heldSQL)
			if e != nil {
				t.Fatal(e)
			}
			defer held.Close()
			filters := make([]*sql.Stmt, 3)
			filterSQL := []string{"SELECT id FROM one WHERE $1 AND $2", "SELECT id FROM one WHERE $1 OR $2", "SELECT id FROM one WHERE NOT $1"}
			for i, q := range filterSQL {
				filters[i], e = db.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				defer filters[i].Close()
			}
			for _, pair := range pairs {
				run(fixture.HeldStatement{Executor: db, Statement: held, SQL: heldSQL}, fixture.Select(heldSQL, []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{pair.and, pair.or, pair.not}}, pair.av, pair.bv))
				for i, value := range []any{pair.and, pair.or, pair.not} {
					args := []any{pair.av, pair.bv}
					if i == 2 {
						args = args[:1]
					}
					want := [][]any{}
					if value == true {
						want = [][]any{{int64(1)}}
					}
					run(fixture.HeldStatement{Executor: db, Statement: filters[i], SQL: filterSQL[i]}, fixture.Select(filterSQL[i], []string{"id"}, []string{"INT4"}, want, args...))
				}
			}
			query("SELECT $1 AND TRUE,$1 FROM one", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}}, false, nil)
			query("SELECT $1 AND $1,NOT $1 OR $1 FROM one", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, false, false)
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
			}{
				{"SELECT TRUE AND 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "17"}},
				{"SELECT TRUE OR 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of OR must be type boolean, not type integer", Position: "16"}},
				{"SELECT NOT 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of NOT must be type boolean, not type integer", Position: "12"}},
				{"SELECT TRUE AND s FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type text", Position: "17"}},
				{"SELECT /*é*/ TRUE AND 1 FROM t", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "23"}},
				{"SELECT TRUE AND 'truth' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "17"}},
				{"SELECT FALSE AND 1 FROM empty", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "18"}},
				{"SELECT TRUE OR 'truth' FROM empty", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "16"}},
				{"SELECT TRUE AND FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "17"}},
				{"SELECT NOT FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "12"}},
				{"SELECT id FROM t WHERE TRUE AND", stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: "32"}},
				{"SELECT (TRUE OR FALSE FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "23"}},
				{"SELECT () FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near ")"`, Position: "9"}},
				{"SELECT TRUE AND AND FALSE FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "AND"`, Position: "17"}},
				{"SELECT TRUE OR FROM t", stuntgres.Error{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "16"}},
				{"SELECT $1,$1 AND TRUE FROM one", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
				{"SELECT $1 FROM t WHERE a AND $1", stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "boolean versus text", Position: "8"}},
				{"SELECT id FROM t WHERE id=$1 AND $1", stuntgres.Error{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "34"}},
			} {
				// PostgreSQL accepts the bare AND/OR target aliases; native excludes them.
				if backend.PG && (tc.q == "SELECT TRUE AND FROM t" || tc.q == "SELECT TRUE OR FROM t") {
					label := "and"
					if tc.q == "SELECT TRUE OR FROM t" {
						label = "or"
					}
					query(tc.q, []string{label}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}, {true}, {true}, {true}, {true}, {true}}, true)
					continue
				}
				diagnostic := fixture.MigrationDiagnostic(tc.want)
				step := fixture.ImplicitError(tc.q, diagnostic)
				step.SelectRows, step.ErrorStage = true, "execute"
				if backend.PG && (tc.want.Code == "42P08" || tc.q == "SELECT id FROM t WHERE id=$1 AND $1") {
					// The direct call has no actual arguments: pq uses the simple-query route,
					// whose missing parameter diagnostic precedes prepared type analysis.
					position := "8"
					if tc.q == "SELECT id FROM t WHERE id=$1 AND $1" {
						position = "27"
					}
					direct := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: position})
					direct.SelectRows, direct.ErrorStage = true, "execute"
					run(db, direct)
				} else {
					run(db, step)
				}
				step.ErrorStage = "prepare"
				run(db, fixture.Prepared(step))
			}
			badSQL := "SELECT NOT $1 FROM one"
			bad, e := db.PrepareContext(ctx, badSQL)
			if e != nil {
				t.Fatal(e)
			}
			defer bad.Close()
			failure := fixture.ImplicitError(badSQL, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
			failure.SelectRows, failure.ErrorStage = true, "execute"
			run(fixture.HeldStatement{Executor: db, Statement: bad, SQL: badSQL}, failure)
			exec("CREATE TABLE bag(a BOOL,b BOOL)", 0)
			exec("INSERT INTO bag VALUES(TRUE,TRUE),(TRUE,TRUE),(NULL,FALSE),(NULL,NULL)", 4)
			query("SELECT a AND b,a OR b FROM bag", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {false, nil}, {nil, nil}}, true)
			query("SELECT a AND b FROM bag WHERE a OR b", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}}, true)
			suffix := fixture.ImplicitError("INSERT INTO one VALUES(2); SELECT TRUE AND FROM one", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "44"})
			suffix.ErrorStage = "execute"
			if backend.PG {
				// The native-only bare-alias exclusion cannot be used as a PostgreSQL
				// malformed suffix witness; retain the same failed-prefix atomicity route.
				suffix = fixture.ImplicitError("INSERT INTO one VALUES(2); SELECT TRUE AND AND FALSE FROM one", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "AND"`, Position: "44"})
				suffix.ErrorStage = "execute"
			}
			run(db, suffix)
			query("SELECT id FROM one", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, false)
			if backend.PG {
				return
			}
			raw, e := p.NativeConnector.Connect(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer raw.Close()
			stmt, e := raw.Prepare("SELECT $1 AND $2 FROM one")
			if e != nil {
				t.Fatal(e)
			}
			if stmt.NumInput() != 2 {
				t.Fatal(stmt.NumInput())
			}
			stmt.Close()
			cursor, e := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,a AND $1 FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: true}})
			if e != nil {
				t.Fatal(e)
			}
			defer cursor.Close()
			exec("INSERT INTO t VALUES(10,TRUE,FALSE,'later')", 1)
			var dump bytes.Buffer
			if e := stuntgres.Dump(ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Reset(ctx, p.Native); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			if meta.ColumnTypeDatabaseTypeName(1) != "BOOL" {
				t.Fatal("retained type replaced")
			}
			dst := make([]driver.Value, 2)
			for i, v := range []any{true, true, true, false, false, false, nil, nil, nil} {
				if e := cursor.Next(dst); e != nil || !reflect.DeepEqual(dst, []driver.Value{int64(i + 1), v}) {
					t.Fatal(dst, e)
				}
			}
			if e := cursor.Next(dst); e != io.EOF {
				t.Fatal(e)
			}
			cursor.Close()
			if meta.ColumnTypeDatabaseTypeName(1) != "BOOL" {
				t.Fatal("closed type replaced")
			}
			query("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"new"}}, false)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			canceledRows, e := raw.(driver.QueryerContext).QueryContext(canceled, "SELECT NOT TRUE FROM t", nil)
			if canceledRows != nil {
				canceledRows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		})
	}
}
