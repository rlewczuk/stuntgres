package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestReferenceArithmeticDeleteComposition(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t AS d WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14", "DELETE FROM t WHERE t.i=7"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
				p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
				step := referenceExec(query, 2)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			})
		}
	}
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
	for _, backend := range p.backends() {
		t.Run(backend.name+"/held", func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, "DELETE FROM t WHERE i+$1=9")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for index, arg := range []any{2, 16, nil} {
				held := referenceHeldStatement{backend.db, stmt, "DELETE FROM t WHERE i+$1=9"}
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(held.query, []int64{2, 1, 0}[index], arg))
			}
			remaining := referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(3), nil, nil}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, remaining)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(remaining))
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
					p := newReferenceScenario(t)
					p.step(t, referenceExec("CREATE TABLE fail(id INT,i INT)", 0))
					p.step(t, referenceExec("INSERT INTO fail VALUES"+tc.input, int64(len(tc.rows))))
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							actor, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := actor.Close(); err != nil {
									t.Error(err)
								}
							}()
							reader, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := reader.Close(); err != nil {
									t.Error(err)
								}
							}()
							run := func(step referenceStep) {
								var executor referenceExecutor = actor
								if step.prepared && !step.selectRows && step.errorStage == "execute" {
									stmt, err := actor.PrepareContext(p.ctx, step.query)
									if err != nil {
										t.Fatal("execution error moved into Prepare", err)
									}
									defer func() {
										if err := stmt.Close(); err != nil {
											t.Error(err)
										}
									}()
									executor = referenceHeldStatement{actor, stmt, step.query}
									step.prepared = false
								}
								referenceRunStep(t, p.ctx, referenceArithmeticExecutor{executor, t}, backend.recorder, step)
							}
							remaining := referenceSelect("SELECT id,i FROM fail ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, tc.rows)
							if explicit {
								run(referenceExec("BEGIN", 0))
							}
							failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
							failure.prepared = prepared
							failure.errorStage = "execute"
							run(failure)
							if explicit {
								aborted := referenceImplicitError("SELECT i FROM fail", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
								aborted.errorStage = "execute"
								run(aborted)
								referenceRunStep(t, p.ctx, reader, backend.recorder, remaining)
								referenceRunStep(t, p.ctx, reader, backend.recorder, referencePrepared(remaining))
								run(referenceExec("ROLLBACK", 0))
							}
							run(remaining)
							run(referencePrepared(remaining))
							referenceRunStep(t, p.ctx, reader, backend.recorder, remaining)
							referenceRunStep(t, p.ctx, reader, backend.recorder, referencePrepared(remaining))
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
			p := newReferenceScenario(t)
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
				referenceArithmeticFailure(t, p, failure)
				failure.errorStage = "prepare"
				p.step(t, referencePrepared(failure))
			}
			native := referenceImplicitError("DELETE FROM t WHERE t.i+1=8", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "+"`, Position: "24"})
			native.errorStage = "execute"
			referenceRunStep(t, p.ctx, referenceArithmeticExecutor{p.memory, t}, p.memoryRecord, native)
			native.errorStage = "prepare"
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
			var want [][]any
			if populated {
				want = [][]any{{int64(7)}}
			}
			survivors := referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, want)
			p.step(t, survivors)
			p.step(t, referencePrepared(survivors))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "qualified/direct", true: "qualified/prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
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
			if p.postgres != nil {
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			}
		})
	}
}

func TestReferenceArithmeticDateComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7)", 1))
	for _, backend := range p.backends() {
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
					rows, err = stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
				} else {
					rows, err = backend.db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), query)
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
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				referenceArithmeticRecordedClosure(t, backend.recorder, query, 2, 1)
			}
		})
	}
}

func TestReferenceArithmeticDeleteParameterComposition(t *testing.T) {
	for index, arg := range []any{2, 16, nil} {
		t.Run([]string{"two", "sixteen", "null"}[index], func(t *testing.T) {
			p := newReferenceScenario(t)
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
