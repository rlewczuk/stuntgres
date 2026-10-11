package reference_test

import (
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticCastComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)", 3))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t ORDER BY id", []string{"?column?", "int8", "int8"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(8), int64(8), int64(8)}, {int64(-6), int64(-6), int64(-6)}, {nil, nil, nil}}),
		fixture.Select("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint,i::int4+1 FROM t ORDER BY id", []string{"?column?", "int8", "int8", "?column?"}, []string{"INT8", "INT8", "INT8", "INT4"}, [][]any{{int64(8), int64(8), int64(8), int64(8)}, {int64(-6), int64(-6), int64(-6), int64(-6)}, {nil, nil, nil, nil}}),
		fixture.Select("SELECT (i+1)::bigint=8,CAST(i+1 AS bool) IS TRUE FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, true}, {nil, false}}),
		fixture.Select("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t WHERE FALSE", []string{"?column?", "int8", "int8"}, []string{"INT8", "INT8", "INT8"}, nil),
		fixture.Select("SELECT i::bigint FROM t ORDER BY id", []string{"i"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(-7)}, {nil}}),
		fixture.Select("SELECT i::int4+1 FROM t ORDER BY id", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(-6)}, {nil}}),
		fixture.Select("SELECT (i+1)::int8=8 FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}),
		fixture.Select("SELECT id FROM t WHERE CAST(i AS bigint)+1=8 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT 2147483647::bigint+1 FROM t WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(2147483648)}}),
		fixture.Select("SELECT num_nulls((+1)::int4,NULL)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT num_nulls(CAST(+1 AS int4),NULL),num_nonnulls(+1::int4,NULL)", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
	} {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	// Each real backend independently checks held executions and their schema.
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/held", func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT CAST($1 AS bigint)+i FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, arg := range []any{2, nil, 3} {
				want := [][]any{{nil}, {nil}, {nil}}
				if arg == 2 {
					want = [][]any{{int64(9)}, {int64(-5)}, {nil}}
				}
				if arg == 3 {
					want = [][]any{{int64(10)}, {int64(-4)}, {nil}}
				}
				held := fixture.HeldStatement{backend.DB, stmt, "SELECT CAST($1 AS bigint)+i FROM t ORDER BY id"}
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(held.SQL, []string{"?column?"}, []string{"INT8"}, want, arg))
			}
		})
	}
	for _, arg := range []any{2, nil, 3} {
		want := [][]any{{nil}, {nil}, {nil}}
		if arg == 2 {
			want = [][]any{{int64(9)}, {int64(-5)}, {nil}}
		}
		if arg == 3 {
			want = [][]any{{int64(10)}, {int64(-4)}, {nil}}
		}
		step := fixture.Select("SELECT CAST($1 AS bigint)+i FROM t ORDER BY id", []string{"?column?"}, []string{"INT8"}, want, arg)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Exec("CREATE TABLE mins(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO mins VALUES(-2147483648)", 1))
	step := fixture.Select("SELECT -i::bigint FROM mins", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(2147483648)}})
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	for _, query := range []string{"SELECT (-i)::bigint FROM mins", "SELECT CAST(2147483647+1 AS bigint) FROM t WHERE id=1"} {
		failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		p.Step(t, failure)
		p.Step(t, fixture.Prepared(failure))
	}
}

func TestReferenceArithmeticCastPreflightComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE e(i INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7),(2,NULL)", 2))
	for _, expression := range []string{"i+CAST($1::text AS int4)", "CAST($1::text AS int4)+i", "-CAST($1::text AS int4)+i"} {
		for _, source := range []string{"e", "t WHERE FALSE", "t ORDER BY id"} {
			query := "SELECT " + expression + " FROM " + source
			for _, backend := range p.Backends() {
				t.Run(query+"/"+backend.Name, func(t *testing.T) {
					stmt, err := backend.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal("conversion moved into Prepare", err)
					}
					defer func() {
						if err := stmt.Close(); err != nil {
							t.Error(err)
						}
					}()
					held := fixture.HeldStatement{backend.DB, stmt, query}
					for _, arg := range []any{"oops", "2", nil, "oops", "7", "3"} {
						var step fixture.Step
						if arg == "oops" {
							step = fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, arg)
							step.SelectRows, step.ErrorStage = true, "execute"
						} else {
							var value any
							if arg != nil {
								n := int64(2)
								if arg == "7" {
									n = 7
								}
								if arg == "3" {
									n = 3
								}
								if strings.HasPrefix(expression, "-") {
									n = -n
								}
								value = int64(7) + n
							}
							var want [][]any
							if source == "t ORDER BY id" {
								want = [][]any{{value}, {nil}}
							}
							step = fixture.Select(query, []string{"?column?"}, []string{"INT4"}, want, arg)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
					}
				})
			}
		}
	}
	for _, query := range []string{"SELECT i+CAST('oops'::text AS int4) FROM e", "SELECT i+CAST('oops'::text AS int4) FROM t WHERE FALSE"} {
		failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		failure.SelectRows, failure.ErrorStage = true, "execute"
		p.Step(t, failure)
		p.Step(t, fixture.Prepared(failure))
	}
	for _, query := range []string{"SELECT i/$1 FROM e", "SELECT i/$1 FROM t WHERE FALSE"} {
		step := fixture.Select(query, []string{"?column?"}, []string{"INT4"}, nil, 0)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	failure := fixture.ImplicitError("SELECT i/$1 FROM t WHERE id=1", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, 0)
	failure.SelectRows = true
	failure.ErrorStage = "execute"
	p.Step(t, failure)
	p.Step(t, fixture.Prepared(failure))
}

func TestReferenceArithmeticCastDiagnosticComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(7),(NULL)", 2))
		}
		for _, tc := range []struct {
			query string
			want  fixture.Diagnostic
		}{
			{"SELECT (i+1)::int4 IS TRUE FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "9"}},
			{"SELECT /*é*/ (i+1)::int4 IS TRUE FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "15"}},
			{"SELECT i FROM t WHERE (i+1)::int4 IS TRUE", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "24"}},
			{"SELECT CAST(i+1 AS bigint) IS TRUE FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type bigint", Position: "8"}},
			{"SELECT i::int8+TRUE FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: bigint + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		} {
			step := fixture.ImplicitError(tc.query, tc.want)
			step.SelectRows, step.ErrorStage = true, "execute"
			p.Step(t, step)
			step.ErrorStage = "prepare"
			p.Step(t, fixture.Prepared(step))
		}
		for _, tc := range []struct {
			query, message, position string
			rows                     [][]any
		}{
			{"SELECT num_nulls(CAST(1+2 AS int4))", "arithmetic in null-count arguments is unsupported", "24", [][]any{{int64(0)}}},
			{"SELECT num_nulls(CAST(1+2 AS int4)) FROM t", "arithmetic in null-count arguments is unsupported", "24", [][]any{{int64(0)}, {int64(0)}}},
			{"SELECT num_nulls((1+2)::int4) FROM t", "arithmetic in null-count arguments is unsupported", "20", [][]any{{int64(0)}, {int64(0)}}},
		} {
			pg := fixture.Select(tc.query, []string{"num_nulls"}, []string{"INT4"}, tc.rows)
			if !populated && strings.Contains(tc.query, "FROM t") {
				pg.Rows = nil
			}
			if p.Postgres != nil {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pg)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pg))
			}
			native := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
			native.SelectRows, native.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, native)
			native.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(native))
		}
	}
}

func TestReferenceArithmeticCastAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	step := fixture.Exec("INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)", 1)
	p.Step(t, step)
	single := fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
	p.Step(t, single)
	p.Step(t, fixture.Prepared(single))
	referenceArithmeticCastPeerRows(t, p, single)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Select("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}, {int64(3), int64(-1)}}))
	p.Step(t, fixture.Exec("CREATE TABLE d(v BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(9)", 1))
	for _, tc := range []struct{ query, code, message string }{
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),(CAST(2147483647+1 AS bigint))", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((2147483648::bigint+0)::int4)", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((1/0)::bigint)", "22012", "division by zero"},
	} {
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
		failure.ErrorStage = "execute"
		referenceArithmeticFailure(t, p, failure)
		remaining := fixture.Select("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}})
		p.Step(t, remaining)
		p.Step(t, fixture.Prepared(remaining))
		referenceArithmeticCastPeerRows(t, p, remaining)
		referenceArithmeticFailure(t, p, fixture.Prepared(failure))
		p.Step(t, remaining)
		p.Step(t, fixture.Prepared(remaining))
		referenceArithmeticCastPeerRows(t, p, remaining)
		p.Step(t, fixture.Exec("BEGIN", 0))
		referenceArithmeticFailure(t, p, failure)
		aborted := fixture.ImplicitError("INSERT INTO d VALUES(10)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.ErrorStage = "execute"
		referenceArithmeticFailure(t, p, aborted)
		p.Step(t, fixture.Exec("ROLLBACK", 0))
		p.Step(t, fixture.Select("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}}))
		for _, backend := range p.Backends() {
			func() {
				reader, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := reader.Close(); err != nil {
						t.Error(err)
					}
				}()
				tx, err := backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{tx, t}, backend.Recorder, failure)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				remaining := fixture.Select("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}})
				fixture.RunStep(t, p.Ctx, reader, backend.Recorder, remaining)
				fixture.RunStep(t, p.Ctx, reader, backend.Recorder, fixture.Prepared(remaining))
			}()
		}
	}
}

func TestReferenceArithmeticSignedCastLiteralComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7),(2,NULL)", 2))
	success := fixture.Select("SELECT num_nulls(-1::int4,NULL),num_nonnulls(-(CAST(1 AS bigint)::int8),NULL),num_nulls((-1)::int4,NULL),num_nonnulls(-1.5,NULL),num_nulls(+1.5,NULL)", []string{"num_nulls", "num_nonnulls", "num_nulls", "num_nonnulls", "num_nulls"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(1), int64(1)}})
	p.Step(t, success)
	p.Step(t, fixture.Prepared(success))
	for _, tc := range []struct{ query, message string }{
		{"SELECT num_nulls(-CAST(2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-9223372036854775808 AS int8),NULL)", "bigint out of range"},
	} {
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22003", Message: tc.message})
		failure.SelectRows, failure.ErrorStage = true, "execute"
		p.Step(t, failure)
		p.Step(t, fixture.Prepared(failure))
	}
	for _, sign := range []string{"+", "-"} {
		for _, child := range []string{"CAST(1+2 AS int4)", "CAST(i AS int4)", "CAST($1 AS int4)"} {
			args := []any{}
			if strings.Contains(child, "$1") {
				args = []any{2}
			}
			for _, from := range []bool{false, true} {
				query := "SELECT num_nulls(" + sign + child + ",NULL)"
				want := [][]any{{int64(1)}}
				message, position := "arithmetic in null-count arguments is unsupported", "18"
				if from {
					query += " FROM t ORDER BY id"
					want = [][]any{{int64(1)}, {int64(1)}}
					if strings.Contains(child, "i AS") {
						want[1][0] = int64(2)
					}
					message, position = "arithmetic in null-count arguments is unsupported", "18"
				}
				pg := fixture.Select(query, []string{"num_nulls"}, []string{"INT4"}, want, args...)
				if p.Postgres != nil {
					if !from && strings.Contains(child, "i AS") {
						failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42703", Message: `column "i" does not exist`, Position: "24"})
						failure.SelectRows, failure.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, failure)
						failure.ErrorStage = "prepare"
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(failure))
					} else {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pg)
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pg))
					}
				}
				native := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: message, Position: position}, args...)
				native.SelectRows, native.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, native)
				native.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(native))
			}
		}
	}
}

// The unwrapped native pool is a distinct public pool sharing the fixture Driver/DSN.
// Observe committed casted writes here as well as through the recorded actor pool.
func referenceArithmeticCastPeerRows(t *testing.T, p *fixture.Pair, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "native-peer/direct"
		if prepared {
			name = "native-peer/prepared"
		}
		t.Run(name, func(t *testing.T) {
			var executor fixture.Executor = p.Native
			if prepared {
				stmt, err := p.Native.PrepareContext(p.Ctx, step.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				executor = fixture.HeldStatement{p.Native, stmt, step.Query}
			}
			rows, err := executor.QueryContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			labels, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(labels, step.Columns) {
				t.Fatal("native peer columns", labels, step.Columns, err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != len(step.Types) {
				t.Fatal("native peer metadata", meta, err)
			}
			for i, typ := range step.Types {
				if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != reflect.TypeFor[int64]() {
					t.Fatalf("native peer column %d type=%s scan=%v want=%s/int64", i, meta[i].DatabaseTypeName(), meta[i].ScanType(), typ)
				}
			}
			row := make([]any, len(step.Columns))
			dest := make([]any, len(row))
			for i := range row {
				dest[i] = &row[i]
			}
			count := 0
			for rows.Next() {
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				if count >= len(step.Rows) || !reflect.DeepEqual(row, step.Rows[count]) {
					t.Fatal("native peer row", count, row, step.Rows)
				}
				count++
			}
			if err := rows.Err(); err != nil || count != len(step.Rows) {
				t.Fatal("native peer row count/end", count, len(step.Rows), err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
