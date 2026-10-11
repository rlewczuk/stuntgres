package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaConsumers(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE extrema_consume(k TEXT,v INT)", 0))
					run(fixture.Exec("INSERT INTO extrema_consume VALUES('a',3),('a',-2),('b',8),(NULL,NULL)", 4))
					run(fixture.Exec("CREATE TABLE extrema_consumed(k TEXT,lo INT,hi INT)", 0))
					for _, prepared := range []bool{false, true} {
						for _, tc := range []struct {
							query string
							count int64
						}{
							{"INSERT INTO extrema_consumed(lo,hi) SELECT min(v),max(v) FROM extrema_consume", 1},
							{"INSERT INTO extrema_consumed SELECT k,min(v),max(v) FROM extrema_consume GROUP BY k", 3},
							{"INSERT INTO extrema_consumed(lo,hi) SELECT min(v),max(v) FROM extrema_consume WHERE FALSE", 1},
							{"INSERT INTO extrema_consumed SELECT k,min(v),max(v) FROM extrema_consume WHERE FALSE GROUP BY k", 0},
						} {
							s := fixture.Exec(tc.query, tc.count)
							s.Prepared = prepared
							run(s)
						}
					}
					want := [][]any{{nil, int64(-2), int64(8)}, {"a", int64(-2), int64(3)}, {"b", int64(8), int64(8)}, {nil, nil, nil}, {nil, nil, nil}, {nil, int64(-2), int64(8)}, {"a", int64(-2), int64(3)}, {"b", int64(8), int64(8)}, {nil, nil, nil}, {nil, nil, nil}}
					s := fixture.Select("SELECT k,lo,hi FROM extrema_consumed", []string{"k", "lo", "hi"}, []string{"TEXT", "INT4", "INT4"}, want)
					s.Bag = true
					run(s)
					if plain {
						// A separate Context execution owns the middleware record;
						// the preceding Query still exercises the actual plain API.
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					const q = "SELECT min(v) AS lo,max(v) AS hi FROM extrema_consume"
					var stmt *sql.Stmt
					var err error
					if plain {
						stmt, err = backend.DB.Prepare(q)
					} else {
						stmt, err = backend.DB.PrepareContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					query := func(recorded bool) *sql.Rows {
						var rows *sql.Rows
						var err error
						if plain && !recorded {
							rows, err = stmt.Query()
						} else {
							rows, err = stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
						}
						if err != nil {
							t.Fatal(err)
						}
						return rows
					}
					old := query(false)
					defer old.Close()
					recordedOld := old
					if plain {
						if backend.PG {
							got := extremaPlainHeldRows(t, old, true)
							if !reflect.DeepEqual(got, [][]any{{int64(-2), int64(8)}}) {
								t.Fatalf("owned extrema %#v", got)
							}
						}
						// Same held statement, distinct Context execution: never
						// attribute its middleware record to the plain cursor.
						recordedOld = query(true)
						defer recordedOld.Close()
					}
					backend.Recorder.Mu.Lock()
					if len(backend.Recorder.Operations) == 0 {
						backend.Recorder.Mu.Unlock()
						t.Fatal("recorded Context companion has no operation")
					}
					op := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
					backend.Recorder.Mu.Unlock()
					consume := func(rows *sql.Rows, lo, hi int64, record ...*fixture.Operation) {
						var got [][]any
						if plain {
							got = extremaPlainHeldRows(t, rows, backend.PG)
						} else {
							got = fixture.AnyValueConsume(t, rows, backend.Recorder, q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, record...)
						}
						if !reflect.DeepEqual(got, [][]any{{lo, hi}}) {
							t.Fatalf("owned extrema %#v", got)
						}
					}
					consumeRecorded := func(rows *sql.Rows, lo, hi int64, record ...*fixture.Operation) {
						got := fixture.AnyValueConsume(t, rows, backend.Recorder, q, []string{"lo", "hi"}, []string{"INT4", "INT4"}, record...)
						if !reflect.DeepEqual(got, [][]any{{lo, hi}}) {
							t.Fatalf("recorded extrema %#v", got)
						}
					}
					// The native cursor is eager; PG is drained before another borrow.
					if backend.PG {
						if !plain {
							consume(old, -2, 8, op)
						}
						if plain {
							consumeRecorded(recordedOld, -2, 8, op)
						}
					}
					run(fixture.Exec("INSERT INTO extrema_consume VALUES('c',-9),('c',12)", 2))
					consume(query(false), -9, 12)
					if !backend.PG {
						consume(old, -2, 8, op)
						if plain {
							consumeRecorded(recordedOld, -2, 8, op)
						}
					}
					consume(query(false), -9, 12)
					if plain {
						consumeRecorded(query(true), -9, 12)
					}
				})
			}
		})
	}
}

// Plain Query cannot carry the Context-only recorder key. Assert the actual
// cursor's schema, raw codecs, values and closure independently of its companion.
func extremaPlainHeldRows(t *testing.T, rows *sql.Rows, pg bool) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"lo", "hi"}) {
		t.Fatalf("plain columns %v: %v", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatalf("plain metadata %v: %v", types, err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" {
			t.Fatalf("plain type %s, want INT4", typ.DatabaseTypeName())
		}
	}
	var values [][]any
	for rows.Next() {
		var lo, hi any
		if err := rows.Scan(&lo, &hi); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "INT4", lo)
		fixture.AssertRawValue(t, pg, "INT4", hi)
		values = append(values, []any{fixture.Normalize(lo), fixture.Normalize(hi)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestReferenceExtremaPermissions(t *testing.T) {
	const reader = "extrema_reader"
	fixture.RoleReferenceCleanup(t, []string{reader})
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE USER "+reader, 0))
		if pg {
			// Schema recreation removes public USAGE; admit lookup without
			// granting table SELECT so the error exercises table authority.
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+reader, 0))
		}
		run(fixture.Exec("CREATE TABLE public.extrema_secret(v INTEGER)", 0))
		queries := []string{"SELECT min(v),max(v) FROM public.extrema_secret", "SELECT min(v),max(v) FROM public.extrema_secret WHERE FALSE"}
		statements := make([]*sql.Stmt, len(queries))
		for i, q := range queries {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			statements[i] = stmt
		}
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO public.extrema_secret VALUES(7)", 1))
			}
			for i, q := range queries {
				value := any(nil)
				if populated && i == 0 {
					value = int64(7)
				}
				executor := fixture.HeldStatement{conn, statements[i], q}
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select(q, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{value, value}}))
			}
			run(fixture.Exec("SET ROLE "+reader, 0))
			for i, q := range queries {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table extrema_secret"})
				s.SelectRows, s.ErrorStage = true, "execute"
				extremaBoth(t, p, conn, recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, statements[i], q}, recorder, s)
			}
			run(fixture.Exec("RESET ROLE", 0))
			for i, q := range queries {
				value := any(nil)
				if populated && i == 0 {
					value = int64(7)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, statements[i], q}, recorder, fixture.Select(q, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{value, value}}))
			}
		}
	})
}

func TestReferenceExtremaCanceledConsumers(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_cancel(v INT)", 0))
		run(fixture.Exec("INSERT INTO extrema_cancel VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE extrema_cancel_out(lo INT,hi INT)", 0))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT min(v),max(v) FROM extrema_cancel", "INSERT INTO extrema_cancel_out SELECT min(v),max(v) FROM extrema_cancel"} {
				var executor fixture.Executor = conn
				if prepared {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{conn, stmt, q}
				}
				if pg {
					// Fast pq commands can beat even a precanceled context.
					// Observe server admission and join real cancellation instead,
					// then exercise this same owner and its preheld consumer.
					fixture.SessionCancel(t, p.Ctx, conn, prepared, q[:6] == "SELECT")
				} else {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if q[:6] == "SELECT" {
						rows, err := executor.QueryContext(ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled extrema exposed rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					} else {
						result, err := executor.ExecContext(ctx, q)
						if result != nil || !errors.Is(err, context.Canceled) {
							t.Fatal(result, err)
						}
					}
				}
				run(fixture.Select("SELECT lo,hi FROM extrema_cancel_out", []string{"lo", "hi"}, []string{"INT4", "INT4"}, nil))
				if q[:6] == "SELECT" {
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select(q, []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				} else {
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec(q, 1))
					run(fixture.Select("SELECT lo,hi FROM extrema_cancel_out", []string{"lo", "hi"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					run(fixture.Exec("DELETE FROM extrema_cancel_out", 1))
				}
			}
		}
		run(fixture.Select("SELECT lo,hi FROM extrema_cancel_out", []string{"lo", "hi"}, []string{"INT4", "INT4"}, nil))
	})
}
