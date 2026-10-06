//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestReferenceArithmeticDeleteComposition(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t AS d WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14", "DELETE FROM t WHERE t.i=7"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferencePair(t)
				p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
				p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
				step := referenceExec(query, 2)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			})
		}
	}
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name+"/held", func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, "DELETE FROM t WHERE i+$1=9")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for index, arg := range []any{2, 16, nil} {
				result, err := stmt.ExecContext(p.ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if err != nil || count != []int64{2, 1, 0}[index] {
					t.Fatal(count, err)
				}
			}
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(3), nil, nil}}))
		})
	}
}

func TestReferenceArithmeticDeleteAtomicity(t *testing.T) {
	for _, tc := range []struct {
		input, query, code, message string
		rows                        [][]any
	}{
		{"(1,7),(2,0),(3,NULL)", "DELETE FROM fail WHERE 14/i=2", "22012", "division by zero", [][]any{{int64(1), int64(7)}, {int64(2), int64(0)}, {int64(3), nil}}},
		{"(1,-1),(2,2147483647)", "DELETE FROM fail WHERE i+1=0", "22003", "integer out of range", [][]any{{int64(1), int64(-1)}, {int64(2), int64(2147483647)}}},
	} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(tc.code+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					p := newReferencePair(t)
					p.step(t, referenceExec("CREATE TABLE fail(id INT,i INT)", 0))
					p.step(t, referenceExec("INSERT INTO fail VALUES"+tc.input, int64(len(tc.rows))))
					for _, backend := range []struct {
						name   string
						db     *sql.DB
						record *referenceRecorder
					}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
						t.Run(backend.name, func(t *testing.T) {
							actor, reader := referenceLockConnections(t, p.ctx, backend.db)
							run := func(step referenceStep) { referenceRunStep(t, p.ctx, actor, backend.record, step) }
							remaining := referenceSelect("SELECT id,i FROM fail ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, tc.rows)
							if explicit {
								run(referenceExec("BEGIN", 0))
							}
							failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
							failure.prepared = prepared
							failure.errorStage = "execute"
							run(failure)
							if explicit {
								run(referenceImplicitError("SELECT i FROM fail", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
								referenceRunStep(t, p.ctx, reader, backend.record, remaining)
								run(referenceExec("ROLLBACK", 0))
							}
							run(remaining)
							referenceRunStep(t, p.ctx, reader, backend.record, remaining)
						})
					}
				})
			}
		}
	}
}

func TestReferenceArithmeticDeleteDiagnostics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
			if populated {
				p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
			}
			for _, tc := range []struct {
				query string
				want  referenceDiagnostic
			}{
				{"DELETE FROM t WHERE i+1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "21"}},
				{"DELETE FROM t WHERE (i+1)", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE i+1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "27"}},
				{"/*é*/ DELETE FROM t WHERE (i+1)", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+1 IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "21"}},
				{"/*é*/ DELETE FROM t WHERE i+1 IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "27"}},
				{"DELETE FROM t WHERE (i+1) IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE (i+1) IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+TRUE=8", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "22", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			} {
				failure := referenceImplicitError(tc.query, tc.want)
				failure.errorStage = "execute"
				p.step(t, failure)
				failure.errorStage = "prepare"
				p.step(t, referencePrepared(failure))
			}
			var want [][]any
			if populated {
				want = [][]any{{int64(7)}}
			}
			p.step(t, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, want))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "qualified/direct", true: "qualified/prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
			query := "DELETE FROM t WHERE t.i+1=8"
			native := referenceImplicitError(query, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "+"`, Position: "24"})
			native.prepared = prepared
			native.errorStage = "execute"
			if prepared {
				native.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}}))
			pg := referenceExec(query, 2)
			pg.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
		})
	}
}

func TestReferenceArithmeticDateComposition(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7)", 1))
	for _, backend := range []struct {
		name string
		pg   bool
		db   *sql.DB
	}{{"native", false, p.memory}, {"postgres", true, p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				const query = "SELECT i+1,DATE '2000-01-01' FROM t WHERE id=1"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := backend.db.PrepareContext(p.ctx, query)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = backend.db.QueryContext(p.ctx, query)
				}
				if err != nil {
					t.Fatal(err)
				}
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"?column?", "date"}) {
					t.Fatal(names, err)
				}
				meta, err := rows.ColumnTypes()
				scan := reflect.TypeFor[string]()
				if backend.pg {
					scan = reflect.TypeFor[time.Time]()
				}
				if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "DATE" || meta[1].ScanType() != scan {
					t.Fatal(meta, err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var integer, date any
				if err := rows.Scan(&integer, &date); err != nil || integer != int64(8) {
					t.Fatal(integer, date, err)
				}
				if backend.pg {
					got, ok := date.(time.Time)
					if !ok || !got.Equal(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
						t.Fatal(date)
					}
				} else if date != "2000-01-01" {
					t.Fatal(date)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
		})
	}
}

// Native authorization is exercised by TestExpressionArithmeticDeleteAuthorization;
// GRANT here is PostgreSQL fixture authority, not a native implementation.
func TestReferenceArithmeticDeletePrivileges(t *testing.T) {
	const role = "arithmetic_delete_actor"
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				roleReferenceCleanup(t, []string{role})
				p := newReferencePair(t)
				admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
				for _, sql := range []string{"CREATE USER " + role, "CREATE TABLE t(id INT,i INT,b BIGINT)", "GRANT USAGE ON SCHEMA public TO " + role, "GRANT DELETE ON TABLE t TO " + role} {
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(sql, 0))
				}
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+role, 0))
				t.Cleanup(func() {
					referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
				})
				original := referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}})
				denied := referenceErrorDiagnostic(query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
				denied.prepared, denied.errorStage = prepared, "execute"
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, denied)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, original)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("GRANT SELECT(i) ON TABLE t TO "+role, 0))
				if query == "DELETE FROM t WHERE b+i=14" {
					referenceRunStep(t, p.ctx, actor, p.postgresRecord, denied)
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, original)
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("GRANT SELECT(b) ON TABLE t TO "+role, 0))
				}
				success := referenceExec(query, 2)
				success.prepared = prepared
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, success)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			})
		}
	}
}

func TestReferenceArithmeticDeleteParameterComposition(t *testing.T) {
	for index, arg := range []any{2, 16, nil} {
		t.Run([]string{"two", "sixteen", "null"}[index], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
			p.step(t, referenceExec("DELETE FROM t WHERE i+$1=9", []int64{2, 1, 0}[index], arg))
			want := [][][]any{
				{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}},
				{{int64(1), int64(7), int64(7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
				{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
			}[index]
			p.step(t, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, want))
		})
	}
}
