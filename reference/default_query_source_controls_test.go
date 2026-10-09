package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceDefaultQuerySourceControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			want := [][]any{{int64(0), int64(9), "seed", false}}
			for id := int64(1); id <= 8; id++ {
				var v any = int64(7)
				if id == 7 {
					v = nil
				}
				want = append(want, []any{id, v, "x", true})
			}
			selectRows := func(rows [][]any) referenceStep {
				return referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v", "s", "b"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, rows)
			}
			program := func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; defaultReferenceRunStep(t, p, db, r, s) }
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE d(id INT,v INT DEFAULT 7,s TEXT DEFAULT 'x',b BOOL DEFAULT TRUE)", 0),
					referenceExec("CREATE TABLE src(id INT)", 0), referenceExec("INSERT INTO src VALUES(1),(2)", 2),
					referenceExec("INSERT INTO d VALUES(0,9,'seed',FALSE)", 1),
					referenceExec("INSERT INTO d(id) SELECT id FROM src ORDER BY id", 2),
					referenceExec("INSERT INTO d(id) VALUES(3),(4) ORDER BY 1 DESC", 2),
					referenceExec("INSERT INTO d(id) (VALUES(5),(6))", 2),
					referenceExec("INSERT INTO d(id,v) SELECT 7,NULL", 1), referenceExec("INSERT INTO d SELECT 8", 1),
					referenceExec("INSERT INTO d(id) SELECT id FROM src WHERE id=99", 0), selectRows(want),
				} {
					run(s)
				}
				// The query API exposes no result columns for command-only INSERT.
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, r)
				q := "INSERT INTO d(id) SELECT 10"
				var rows *sql.Rows
				var err error
				if prepared {
					s, e := db.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer s.Close()
					rows, err = s.QueryContext(ctx)
				} else {
					rows, err = db.QueryContext(ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				names, e := rows.Columns()
				meta, me := rows.ColumnTypes()
				if e != nil || me != nil || len(names) != 0 || len(meta) != 0 || rows.Next() || rows.Err() != nil {
					t.Fatal(names, meta, e, me, rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				run(selectRows(append(append([][]any{}, want...), []any{int64(10), int64(7), "x", true})))
			}
			defaultConsumerReferenceEach(t, p, program)
			t.Run("native-controls-and-replay", func(t *testing.T) {
				// The program above supplies each backend's initial rows; replay must
				// preserve them and the source while retaining future default requests.
				defaultConsumerReferenceRoundtrip(t, p, "DEFAULT 7", "d", "src")
				before := selectRows(append(append([][]any{}, want...), []any{int64(10), int64(7), "x", true}))
				before.prepared = prepared
				defaultReferenceStep(t, p, before)
				s := referenceExec("INSERT INTO d(id) SELECT 11", 1)
				s.prepared = prepared
				p.step(t, s)
				rows := append(append([][]any{}, want...), []any{int64(10), int64(7), "x", true}, []any{int64(11), int64(7), "x", true})
				s = selectRows(rows)
				s.prepared = prepared
				defaultReferenceStep(t, p, s)
			})
		})
	}
}

func TestReferenceDefaultQuerySourceAtomicityAndCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"SELECT id,n FROM src ORDER BY id", "VALUES(1,9),(2,NULL) ORDER BY 1"} {
			t.Run(fmt.Sprintf("%v/%s", prepared, source), func(t *testing.T) {
				p := newReferenceScenario(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
					for _, s := range []referenceStep{referenceExec("CREATE TABLE src(id INT,n INT)", 0), referenceExec("INSERT INTO src VALUES(1,9),(2,NULL)", 2), referenceExec("CREATE TABLE d(id INT,v INT DEFAULT 7,n INT NOT NULL)", 0), referenceExec("INSERT INTO d VALUES(0,9,9)", 1)} {
						run(s)
					}
					failure := referenceImplicitError("INSERT INTO d(id,n) "+source, referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "d" violates not-null constraint`, Detail: "Failing row contains (2, 7, null).", Schema: "public", Table: "d", Column: "n"})
					failure.errorStage = "execute"
					run(failure)
					seed := referenceSelect("SELECT * FROM d", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(9), int64(9)}})
					run(seed)
					ctx, cancel := context.WithCancel(p.ctx)
					cancel()
					q := "INSERT INTO d(id,n) SELECT 3,9"
					var err error
					if prepared {
						s, e := db.PrepareContext(context.WithValue(p.ctx, referenceRecorderKey{}, r), q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.ExecContext(ctx)
						s.Close()
					} else {
						_, err = db.ExecContext(ctx, q)
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal("ordinary cancellation boundary", err)
					}
					run(seed)
					run(referenceExec(q, 1))
					run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(9), int64(9)}, {int64(3), int64(7), int64(9)}}))
				})
			})
		}
	}
}

func TestReferenceDefaultQuerySourceDeferredFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
				run(referenceExec("CREATE TABLE d(id INT,v INT DEFAULT 2147483648)", 0))
				run(referenceExec("INSERT INTO d VALUES(0,9)", 1))
				run(referenceExec("CREATE TABLE src(id INT)", 0))
				// Supported empty table source, not unsupported FROM-less WHERE
				// or LIMIT syntax. Immutable default planning precedes row demand.
				for _, q := range []string{"INSERT INTO d(id) SELECT id FROM src", "INSERT INTO d(id) SELECT 1", "INSERT INTO d(id) VALUES(1),(2) ORDER BY 1"} {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
					s.errorStage = "execute"
					run(s)
					run(referenceSelect("SELECT * FROM d", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(9)}}))
				}
				run(referenceExec("INSERT INTO d(id,v) SELECT id,9 FROM src", 0))
				run(referenceExec("INSERT INTO src VALUES(1),(2)", 2))
				run(referenceExec("INSERT INTO d(id,v) SELECT id,9 FROM src ORDER BY id", 2))
				run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(9)}, {int64(1), int64(9)}, {int64(2), int64(9)}}))
			})
		})
	}
}

func TestReferenceDefaultDecoratedValuesBoundary(t *testing.T) {
	// Root's pinned oracle observed these complete ORDER-source records:
	// 42601 at DEFAULT, direct Execute versus prepared Prepare.
	for _, q := range []string{"INSERT INTO d VALUES(DEFAULT),(2) ORDER BY 1", "INSERT INTO d (VALUES(1),(DEFAULT) ORDER BY 1)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", prepared, q), func(t *testing.T) {
				p := newReferenceScenario(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
					run(referenceExec("CREATE TABLE d(v INT DEFAULT 7)", 0))
					run(referenceExec("INSERT INTO d VALUES(DEFAULT)", 1))
					run(referenceExec("INSERT INTO d (VALUES(DEFAULT))", 1))
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: strconv.Itoa(strings.Index(q, "DEFAULT") + 1)})
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
					run(referenceSelect("SELECT v FROM d", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
				})
			})
		}
	}
}

func TestReferenceDefaultQuerySourceHeldPrepared(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; defaultReferenceRunStep(t, p, db, r, s) }
				run(referenceExec("CREATE TABLE src(id INT)", 0))
				run(referenceExec("CREATE TABLE d(id INT,v INT DEFAULT 7,b BOOL DEFAULT FALSE)", 0))
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, r)
				q := "INSERT INTO d(id) SELECT $1 FROM src"
				execute := func(arg any) (sql.Result, error) { return db.ExecContext(ctx, q, arg) }
				if prepared {
					s, err := db.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					execute = func(arg any) (sql.Result, error) { return s.ExecContext(ctx, arg) }
				}
				for _, tc := range []struct {
					before               string
					inserted, arg, count int64
				}{
					{"", 0, 7, 0},
					{"INSERT INTO src VALUES(1),(2)", 2, 8, 2},
					{"INSERT INTO src VALUES(3)", 1, 9, 3},
				} {
					if tc.before != "" {
						run(referenceExec(tc.before, tc.inserted))
					}
					result, err := execute(tc.arg)
					if err != nil {
						t.Fatal(err)
					}
					count, err := result.RowsAffected()
					if err != nil || count != tc.count {
						t.Fatal(count, err, tc.count)
					}
				}
				run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v", "b"}, []string{"INT4", "INT4", "BOOL"}, [][]any{
					{int64(8), int64(7), false}, {int64(8), int64(7), false},
					{int64(9), int64(7), false}, {int64(9), int64(7), false}, {int64(9), int64(7), false},
				}))
			})
		})
	}
}
