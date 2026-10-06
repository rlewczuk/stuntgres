//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDefaultValuesPlanningDecisive(t *testing.T) {
	// Independently specified ordinary-consumer constant planning, not native
	// golden values. Each PostgreSQL subtest runs before the native assertions.
	for _, expression := range []struct {
		sql   string
		value bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		for _, prepared := range []bool{false, true} {
			for _, form := range []struct {
				name, sql string
				ids       []any
				mixed     bool
			}{
				{"omission-single", "INSERT INTO d(id) VALUES(1)", []any{int64(1)}, false},
				{"omission-multiple", "INSERT INTO d(id) VALUES(1),(2)", []any{int64(1), int64(2)}, false},
				{"requests", "INSERT INTO d VALUES(1,DEFAULT),(2,DEFAULT)", []any{int64(1), int64(2)}, false},
				{"parenthesized-requests", "INSERT INTO d (VALUES(1,DEFAULT),(2,DEFAULT))", []any{int64(1), int64(2)}, false},
				{"default-values", "INSERT INTO d DEFAULT VALUES", []any{nil}, false},
				{"mixed", "INSERT INTO d VALUES(1,NULL),(2,FALSE),(3,DEFAULT)", []any{int64(1), int64(2), int64(3)}, true},
			} {
				t.Run(fmt.Sprintf("%s/prepared=%v/%s", expression.sql, prepared, form.name), func(t *testing.T) {
					p := newReferencePair(t)
					want := [][]any{{int64(0), nil}}
					for _, id := range form.ids {
						want = append(want, []any{id, expression.value})
					}
					if form.mixed {
						want = [][]any{{int64(0), nil}, {int64(1), nil}, {int64(2), false}, {int64(3), expression.value}}
					}
					selectRows := func(rows [][]any) referenceStep {
						return referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, rows)
					}
					nativeComplete := false
					defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
						run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression.sql+"))", 0))
						if db == p.memory {
							defaultConsumerReferenceRoundtrip(t, p, "'oops'", "d")
							empty := selectRows(nil)
							empty.prepared = prepared
							p.step(t, empty)
						}
						run(referenceExec("INSERT INTO d VALUES(0,NULL)", 1))
						run(referenceExec(form.sql, int64(len(form.ids))))
						run(selectRows(want))
						if db == p.memory {
							nativeComplete = true
						}
					})
					if !nativeComplete {
						return
					}
					t.Run("native-controls-and-replay", func(t *testing.T) {
						defaultConsumerReferenceRoundtrip(t, p, "'oops'", "d")
						run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
						run(selectRows(want))
						run(referenceExec("INSERT INTO d(id) VALUES(9)", 1))
						run(referenceExec("INSERT INTO d (VALUES(10,DEFAULT))", 1))
						if form.name == "default-values" {
							want = [][]any{{int64(0), nil}, {int64(9), expression.value}, {int64(10), expression.value}, {nil, expression.value}}
						} else {
							want = append(want, []any{int64(9), expression.value}, []any{int64(10), expression.value})
						}
						run(selectRows(want))
					})
				})
			}
		}
	}
}

func TestReferenceDefaultValuesPlanningRequiredBranches(t *testing.T) {
	for _, expression := range []string{
		"NULL OR CAST('oops'::text AS INT)=1", "NULL AND CAST('oops'::text AS INT)=1",
		"FALSE OR CAST('oops'::text AS INT)=1", "TRUE AND CAST('oops'::text AS INT)=1",
		"CAST('oops'::text AS INT)=1 OR TRUE", "CAST('oops'::text AS INT)=1 AND FALSE",
		"(TRUE OR CAST('discarded'::text AS INT)=1) AND CAST('oops'::text AS INT)=1",
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", expression, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
					run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression+"))", 0))
					run(referenceExec("INSERT INTO d VALUES(0,TRUE),(1,NULL),(2,FALSE)", 3))
					seed := referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}, {int64(1), nil}, {int64(2), false}})
					for _, q := range []string{
						"INSERT INTO d(id) VALUES(3),(4)", "INSERT INTO d VALUES(3,DEFAULT)",
						"INSERT INTO d (VALUES(3,DEFAULT))", "INSERT INTO d DEFAULT VALUES",
						"INSERT INTO d VALUES(3,NULL),(4,TRUE),(5,DEFAULT)",
					} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
						s.errorStage = "execute"
						run(s)
						run(seed)
					}
				})
			})
		}
	}
}

func TestReferenceDefaultValuesPlanningSelectiveColumns(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			want := [][]any{{int64(0), nil, int64(9)}, {int64(1), nil, int64(9)}, {int64(2), true, int64(9)}, {int64(3), true, int64(9)}, {int64(4), true, int64(9)}}
			selectRows := func(rows [][]any) referenceStep {
				return referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b", "v"}, []string{"INT4", "BOOL", "INT4"}, rows)
			}
			nativeComplete := false
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
				run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT (TRUE OR CAST('oops'::text AS INT)=1),v INT DEFAULT int4('never'::text))", 0))
				if db == p.memory {
					defaultConsumerReferenceRoundtrip(t, p, "'never'", "d")
					empty := selectRows(nil)
					empty.prepared = prepared
					p.step(t, empty)
				}
				run(referenceExec("INSERT INTO d VALUES(0,NULL,9)", 1))
				run(referenceExec("INSERT INTO d VALUES(1,NULL,9),(2,TRUE,9),(3,DEFAULT,9)", 3))
				run(referenceExec("INSERT INTO d(id,v) VALUES(4,9)", 1))
				run(selectRows(want))
				if db == p.memory {
					nativeComplete = true
				}
			})
			if !nativeComplete {
				return
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				defaultConsumerReferenceRoundtrip(t, p, "'never'", "d")
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(selectRows(want))
				run(referenceExec("INSERT INTO d VALUES(5,DEFAULT,9)", 1))
				want = append(want, []any{int64(5), true, int64(9)})
				run(selectRows(want))
			})
		})
	}
}
