package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceArithmeticDeleteComposition(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t AS d WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14", "DELETE FROM t WHERE t.i=7"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
				step := fixture.Exec(query, 2)
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			})
		}
	}
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/held", func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, "DELETE FROM t WHERE i+$1=9")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for index, arg := range []any{2, 16, nil} {
				held := fixture.HeldStatement{backend.DB, stmt, "DELETE FROM t WHERE i+$1=9"}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(held.SQL, []int64{2, 1, 0}[index], arg))
			}
			remaining := fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(3), nil, nil}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, remaining)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(remaining))
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
					p := fixture.NewScenario(t)
					p.Step(t, fixture.Exec("CREATE TABLE fail(id INT,i INT)", 0))
					p.Step(t, fixture.Exec("INSERT INTO fail VALUES"+tc.input, int64(len(tc.rows))))
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							actor, err := backend.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := actor.Close(); err != nil {
									t.Error(err)
								}
							}()
							reader, err := backend.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := reader.Close(); err != nil {
									t.Error(err)
								}
							}()
							run := func(step fixture.Step) {
								var executor fixture.Executor = actor
								if step.Prepared && !step.SelectRows && step.ErrorStage == "execute" {
									stmt, err := actor.PrepareContext(p.Ctx, step.Query)
									if err != nil {
										t.Fatal("execution error moved into Prepare", err)
									}
									defer func() {
										if err := stmt.Close(); err != nil {
											t.Error(err)
										}
									}()
									executor = fixture.HeldStatement{actor, stmt, step.Query}
									step.Prepared = false
								}
								fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{executor, t}, backend.Recorder, step)
							}
							remaining := fixture.Select("SELECT id,i FROM fail ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, tc.rows)
							if explicit {
								run(fixture.Exec("BEGIN", 0))
							}
							failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
							failure.Prepared = prepared
							failure.ErrorStage = "execute"
							run(failure)
							if explicit {
								aborted := fixture.ImplicitError("SELECT i FROM fail", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
								aborted.ErrorStage = "execute"
								run(aborted)
								fixture.RunStep(t, p.Ctx, reader, backend.Recorder, remaining)
								fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Prepared(remaining))
								run(fixture.Exec("ROLLBACK", 0))
							}
							run(remaining)
							run(fixture.Prepared(remaining))
							fixture.RunStep(t, p.Ctx, reader, backend.Recorder, remaining)
							fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Prepared(remaining))
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
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
			if populated {
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
			}
			for _, tc := range []struct {
				query string
				want  fixture.Diagnostic
			}{
				{"DELETE FROM t WHERE i+1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "21"}},
				{"DELETE FROM t WHERE (i+1)", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE i+1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "27"}},
				{"/*é*/ DELETE FROM t WHERE (i+1)", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+1 IS TRUE", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "21"}},
				{"/*é*/ DELETE FROM t WHERE i+1 IS TRUE", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "27"}},
				{"DELETE FROM t WHERE (i+1) IS TRUE", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "22"}},
				{"/*é*/ DELETE FROM t WHERE (i+1) IS TRUE", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "28"}},
				{"DELETE FROM t WHERE i+TRUE=8", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer + boolean", Position: "22", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			} {
				failure := fixture.ImplicitError(tc.query, tc.want)
				failure.ErrorStage = "execute"
				referenceArithmeticFailure(t, p, failure)
				failure.ErrorStage = "prepare"
				p.Step(t, fixture.Prepared(failure))
			}
			native := fixture.ImplicitError("DELETE FROM t WHERE t.i+1=8", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "+"`, Position: "24"})
			native.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{p.Memory, t}, p.MemoryRecord, native)
			native.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(native))
			var want [][]any
			if populated {
				want = [][]any{{int64(7)}}
			}
			survivors := fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, want)
			p.Step(t, survivors)
			p.Step(t, fixture.Prepared(survivors))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "qualified/direct", true: "qualified/prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
			query := "DELETE FROM t WHERE t.i+1=8"
			native := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "+"`, Position: "24"})
			native.Prepared = prepared
			native.ErrorStage = "execute"
			if prepared {
				native.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, native)
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}}))
			pg := fixture.Exec(query, 2)
			pg.Prepared = prepared
			if p.Postgres != nil {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pg)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			}
		})
	}
}

func TestReferenceArithmeticDateComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7)", 1))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				const query = "SELECT i+1,DATE '2000-01-01' FROM t WHERE id=1"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := backend.DB.PrepareContext(p.Ctx, query)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
				} else {
					rows, err = backend.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), query)
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
				if backend.PG {
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
				if backend.PG {
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
				referenceArithmeticRecordedClosure(t, backend.Recorder, query, 2, 1)
			}
		})
	}
}

func TestReferenceArithmeticDeleteParameterComposition(t *testing.T) {
	for index, arg := range []any{2, 16, nil} {
		t.Run([]string{"two", "sixteen", "null"}[index], func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
			p.Step(t, fixture.Exec("DELETE FROM t WHERE i+$1=9", []int64{2, 1, 0}[index], arg))
			want := [][][]any{
				{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}},
				{{int64(1), int64(7), int64(7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
				{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}},
			}[index]
			p.Step(t, fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, want))
		})
	}
}
