package reference_test

import (
	"reflect"
	"strings"
	"testing"
)

func TestReferenceArithmeticCastComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL)", 3))
	for _, step := range []referenceStep{
		referenceSelect("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t ORDER BY id", []string{"?column?", "int8", "int8"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(8), int64(8), int64(8)}, {int64(-6), int64(-6), int64(-6)}, {nil, nil, nil}}),
		referenceSelect("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint,i::int4+1 FROM t ORDER BY id", []string{"?column?", "int8", "int8", "?column?"}, []string{"INT8", "INT8", "INT8", "INT4"}, [][]any{{int64(8), int64(8), int64(8), int64(8)}, {int64(-6), int64(-6), int64(-6), int64(-6)}, {nil, nil, nil, nil}}),
		referenceSelect("SELECT (i+1)::bigint=8,CAST(i+1 AS bool) IS TRUE FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, true}, {nil, false}}),
		referenceSelect("SELECT i::bigint+1,CAST(i+1 AS bigint),(i+1)::bigint FROM t WHERE FALSE", []string{"?column?", "int8", "int8"}, []string{"INT8", "INT8", "INT8"}, nil),
		referenceSelect("SELECT i::bigint FROM t ORDER BY id", []string{"i"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(-7)}, {nil}}),
		referenceSelect("SELECT i::int4+1 FROM t ORDER BY id", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(-6)}, {nil}}),
		referenceSelect("SELECT (i+1)::int8=8 FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}),
		referenceSelect("SELECT id FROM t WHERE CAST(i AS bigint)+1=8 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT 2147483647::bigint+1 FROM t WHERE id=1", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(2147483648)}}),
		referenceSelect("SELECT num_nulls((+1)::int4,NULL)", []string{"num_nulls"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT num_nulls(CAST(+1 AS int4),NULL),num_nonnulls(+1::int4,NULL)", []string{"num_nulls", "num_nonnulls"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	// Each real backend independently checks held executions and their schema.
	for _, backend := range p.backends() {
		t.Run(backend.name+"/held", func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, "SELECT CAST($1 AS bigint)+i FROM t ORDER BY id")
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
				held := referenceHeldStatement{backend.db, stmt, "SELECT CAST($1 AS bigint)+i FROM t ORDER BY id"}
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(held.query, []string{"?column?"}, []string{"INT8"}, want, arg))
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
		step := referenceSelect("SELECT CAST($1 AS bigint)+i FROM t ORDER BY id", []string{"?column?"}, []string{"INT8"}, want, arg)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE mins(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO mins VALUES(-2147483648)", 1))
	step := referenceSelect("SELECT -i::bigint FROM mins", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(2147483648)}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
	for _, query := range []string{"SELECT (-i)::bigint FROM mins", "SELECT CAST(2147483647+1 AS bigint) FROM t WHERE id=1"} {
		failure := referenceImplicitError(query, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		failure.selectRows = true
		failure.errorStage = "execute"
		p.step(t, failure)
		p.step(t, referencePrepared(failure))
	}
}

func TestReferenceArithmeticCastPreflightComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE e(i INT)", 0))
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7),(2,NULL)", 2))
	for _, expression := range []string{"i+CAST($1::text AS int4)", "CAST($1::text AS int4)+i", "-CAST($1::text AS int4)+i"} {
		for _, source := range []string{"e", "t WHERE FALSE", "t ORDER BY id"} {
			query := "SELECT " + expression + " FROM " + source
			for _, backend := range p.backends() {
				t.Run(query+"/"+backend.name, func(t *testing.T) {
					stmt, err := backend.db.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal("conversion moved into Prepare", err)
					}
					defer func() {
						if err := stmt.Close(); err != nil {
							t.Error(err)
						}
					}()
					held := referenceHeldStatement{backend.db, stmt, query}
					for _, arg := range []any{"oops", "2", nil, "oops", "7", "3"} {
						var step referenceStep
						if arg == "oops" {
							step = referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, arg)
							step.selectRows, step.errorStage = true, "execute"
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
							step = referenceSelect(query, []string{"?column?"}, []string{"INT4"}, want, arg)
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
						referenceRunStep(t, p.ctx, held, backend.recorder, step)
					}
				})
			}
		}
	}
	for _, query := range []string{"SELECT i+CAST('oops'::text AS int4) FROM e", "SELECT i+CAST('oops'::text AS int4) FROM t WHERE FALSE"} {
		failure := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		failure.selectRows, failure.errorStage = true, "execute"
		p.step(t, failure)
		p.step(t, referencePrepared(failure))
	}
	for _, query := range []string{"SELECT i/$1 FROM e", "SELECT i/$1 FROM t WHERE FALSE"} {
		step := referenceSelect(query, []string{"?column?"}, []string{"INT4"}, nil, 0)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	failure := referenceImplicitError("SELECT i/$1 FROM t WHERE id=1", referenceDiagnostic{Code: "22012", Message: "division by zero"}, 0)
	failure.selectRows = true
	failure.errorStage = "execute"
	p.step(t, failure)
	p.step(t, referencePrepared(failure))
}

func TestReferenceArithmeticCastDiagnosticComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(7),(NULL)", 2))
		}
		for _, tc := range []struct {
			query string
			want  referenceDiagnostic
		}{
			{"SELECT (i+1)::int4 IS TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "9"}},
			{"SELECT /*é*/ (i+1)::int4 IS TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "15"}},
			{"SELECT i FROM t WHERE (i+1)::int4 IS TRUE", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "24"}},
			{"SELECT CAST(i+1 AS bigint) IS TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type bigint", Position: "8"}},
			{"SELECT i::int8+TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: bigint + boolean", Position: "15", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
		} {
			step := referenceImplicitError(tc.query, tc.want)
			step.selectRows, step.errorStage = true, "execute"
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		}
		for _, tc := range []struct {
			query, message, position string
			rows                     [][]any
		}{
			{"SELECT num_nulls(CAST(1+2 AS int4))", "arithmetic in null-count arguments is unsupported", "24", [][]any{{int64(0)}}},
			{"SELECT num_nulls(CAST(1+2 AS int4)) FROM t", "arithmetic in null-count arguments is unsupported", "24", [][]any{{int64(0)}, {int64(0)}}},
			{"SELECT num_nulls((1+2)::int4) FROM t", "arithmetic in null-count arguments is unsupported", "20", [][]any{{int64(0)}, {int64(0)}}},
		} {
			pg := referenceSelect(tc.query, []string{"num_nulls"}, []string{"INT4"}, tc.rows)
			if !populated && strings.Contains(tc.query, "FROM t") {
				pg.rows = nil
			}
			if p.postgres != nil {
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(pg))
			}
			native := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
			native.selectRows, native.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			native.errorStage = "prepare"
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
		}
	}
}

func TestReferenceArithmeticCastAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE dest(i INT,b BIGINT)", 0))
	step := referenceExec("INSERT INTO dest VALUES(CAST(7/2 AS int4),(-7%2)::int8)", 1)
	p.step(t, step)
	single := referenceSelect("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}})
	p.step(t, single)
	p.step(t, referencePrepared(single))
	referenceArithmeticCastPeerRows(t, p, single)
	p.step(t, referencePrepared(step))
	p.step(t, referenceSelect("SELECT i,b FROM dest", []string{"i", "b"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(-1)}, {int64(3), int64(-1)}}))
	p.step(t, referenceExec("CREATE TABLE d(v BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO d VALUES(9)", 1))
	for _, tc := range []struct{ query, code, message string }{
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),(CAST(2147483647+1 AS bigint))", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((2147483648::bigint+0)::int4)", "22003", "integer out of range"},
		{"INSERT INTO d VALUES(CAST(1+1 AS bigint)),((1/0)::bigint)", "22012", "division by zero"},
	} {
		failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
		failure.errorStage = "execute"
		referenceArithmeticFailure(t, p, failure)
		remaining := referenceSelect("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}})
		p.step(t, remaining)
		p.step(t, referencePrepared(remaining))
		referenceArithmeticCastPeerRows(t, p, remaining)
		referenceArithmeticFailure(t, p, referencePrepared(failure))
		p.step(t, remaining)
		p.step(t, referencePrepared(remaining))
		referenceArithmeticCastPeerRows(t, p, remaining)
		p.step(t, referenceExec("BEGIN", 0))
		referenceArithmeticFailure(t, p, failure)
		aborted := referenceImplicitError("INSERT INTO d VALUES(10)", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.errorStage = "execute"
		referenceArithmeticFailure(t, p, aborted)
		p.step(t, referenceExec("ROLLBACK", 0))
		p.step(t, referenceSelect("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}}))
		for _, backend := range p.backends() {
			func() {
				reader, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := reader.Close(); err != nil {
						t.Error(err)
					}
				}()
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, referenceArithmeticExecutor{tx, t}, backend.recorder, failure)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				remaining := referenceSelect("SELECT v FROM d", []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}})
				referenceRunStep(t, p.ctx, reader, backend.recorder, remaining)
				referenceRunStep(t, p.ctx, reader, backend.recorder, referencePrepared(remaining))
			}()
		}
	}
}

func TestReferenceArithmeticSignedCastLiteralComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,i INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,7),(2,NULL)", 2))
	success := referenceSelect("SELECT num_nulls(-1::int4,NULL),num_nonnulls(-(CAST(1 AS bigint)::int8),NULL),num_nulls((-1)::int4,NULL),num_nonnulls(-1.5,NULL),num_nulls(+1.5,NULL)", []string{"num_nulls", "num_nonnulls", "num_nulls", "num_nonnulls", "num_nulls"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(1), int64(1)}})
	p.step(t, success)
	p.step(t, referencePrepared(success))
	for _, tc := range []struct{ query, message string }{
		{"SELECT num_nulls(-CAST(2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-2147483648 AS int4),NULL)", "integer out of range"},
		{"SELECT num_nulls(-CAST(-9223372036854775808 AS int8),NULL)", "bigint out of range"},
	} {
		failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22003", Message: tc.message})
		failure.selectRows, failure.errorStage = true, "execute"
		p.step(t, failure)
		p.step(t, referencePrepared(failure))
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
				pg := referenceSelect(query, []string{"num_nulls"}, []string{"INT4"}, want, args...)
				if p.postgres != nil {
					if !from && strings.Contains(child, "i AS") {
						failure := referenceImplicitError(query, referenceDiagnostic{Code: "42703", Message: `column "i" does not exist`, Position: "24"})
						failure.selectRows, failure.errorStage = true, "execute"
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, failure)
						failure.errorStage = "prepare"
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(failure))
					} else {
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(pg))
					}
				}
				native := referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: message, Position: position}, args...)
				native.selectRows, native.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
				native.errorStage = "prepare"
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
			}
		}
	}
}

// The unwrapped native pool is a distinct public pool sharing the fixture Driver/DSN.
// Observe committed casted writes here as well as through the recorded actor pool.
func referenceArithmeticCastPeerRows(t *testing.T, p *referencePair, step referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "native-peer/direct"
		if prepared {
			name = "native-peer/prepared"
		}
		t.Run(name, func(t *testing.T) {
			var executor referenceExecutor = p.native
			if prepared {
				stmt, err := p.native.PrepareContext(p.ctx, step.query)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				executor = referenceHeldStatement{p.native, stmt, step.query}
			}
			rows, err := executor.QueryContext(p.ctx, step.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			labels, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(labels, step.columns) {
				t.Fatal("native peer columns", labels, step.columns, err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != len(step.types) {
				t.Fatal("native peer metadata", meta, err)
			}
			for i, typ := range step.types {
				if meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != reflect.TypeFor[int64]() {
					t.Fatalf("native peer column %d type=%s scan=%v want=%s/int64", i, meta[i].DatabaseTypeName(), meta[i].ScanType(), typ)
				}
			}
			row := make([]any, len(step.columns))
			dest := make([]any, len(row))
			for i := range row {
				dest[i] = &row[i]
			}
			count := 0
			for rows.Next() {
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				if count >= len(step.rows) || !reflect.DeepEqual(row, step.rows[count]) {
					t.Fatal("native peer row", count, row, step.rows)
				}
				count++
			}
			if err := rows.Err(); err != nil || count != len(step.rows) {
				t.Fatal("native peer row count/end", count, len(step.rows), err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
