//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

// Run PostgreSQL first in a separate subtest: a native failure must not prevent
// the independently specified pinned-release expectation from being exercised.
func defaultConsumerReferenceEach(t *testing.T, p *referencePair, f func(*testing.T, *sql.DB, *referenceRecorder)) {
	t.Helper()
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{
		{"postgres-oracle", p.postgres, p.postgresRecord},
		{"native", p.memory, p.memoryRecord},
	} {
		t.Run(b.name, func(t *testing.T) { f(t, b.db, b.record) })
	}
}

func defaultConsumerReferenceRoundtrip(t *testing.T, p *referencePair, witness string, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), witness) {
		t.Errorf("lost default structure %q in %s", witness, dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err, dump.String())
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("default redump", err, again.String(), dump.String())
	}
	for _, table := range tables {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL replay", err, dump.String())
	}
}

func defaultConsumerReferenceControls(t *testing.T, create, witness string, names, types []string, nativeDefault, postgresDefault []any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			row := func(id int64, values []any) []any { return append([]any{id}, values...) }
			nativeRows, postgresRows := [][]any{}, [][]any{}
			for id := int64(1); id <= 3; id++ {
				nativeRows = append(nativeRows, row(id, nativeDefault))
				postgresRows = append(postgresRows, row(id, postgresDefault))
			}
			nulls := make([]any, len(nativeDefault))
			nativeRows = append(nativeRows, row(4, nulls))
			postgresRows = append(postgresRows, row(4, nulls))
			requested := strings.TrimSuffix(strings.Repeat("DEFAULT,", len(nativeDefault)), ",")
			nullSQL := strings.TrimSuffix(strings.Repeat("NULL,", len(nativeDefault)), ",")
			insert := "INSERT INTO d VALUES(3," + requested + "),(4," + nullSQL + ")"
			selectRows := func(want [][]any) referenceStep {
				return referenceSelect("SELECT * FROM d ORDER BY id", names, types, want)
			}
			t.Run("postgres-oracle", func(t *testing.T) {
				run := func(s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				}
				run(referenceExec(create, 0))
				run(selectRows(nil))
				run(referenceExec("INSERT INTO d(id) VALUES(1),(2)", 2))
				run(referenceExec(insert, 2))
				run(selectRows(postgresRows))
			})
			t.Run("native-controls-and-replay", func(t *testing.T) {
				native := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
				both := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				check := func() {
					native(selectRows(nativeRows))
					s := selectRows(postgresRows)
					s.prepared = prepared
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				}
				native(referenceExec(create, 0))
				native(selectRows(nil))
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				native(selectRows(nil))
				s := selectRows(nil)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				both(referenceExec("INSERT INTO d(id) VALUES(1),(2)", 2))
				both(referenceExec(insert, 2))
				check()
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				check()
				both(referenceExec("INSERT INTO d(id) VALUES(5)", 1))
				both(referenceExec("INSERT INTO d VALUES(6,"+requested+")", 1))
				for id := int64(5); id <= 6; id++ {
					nativeRows = append(nativeRows, row(id, nativeDefault))
					postgresRows = append(postgresRows, row(id, postgresDefault))
				}
				check()
			})
		})
	}
}

func TestReferenceDefaultCastControls(t *testing.T) {
	// PostgreSQL's explicit cast pathways, not assignment coercions.
	want := []any{"1", int64(1), true, int64(7), int64(42), true}
	defaultConsumerReferenceControls(t, "CREATE TABLE d(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT CAST(TRUE AS INT),b BOOL DEFAULT CAST(-2 AS BOOL),n BIGINT DEFAULT CAST(7 AS BIGINT),f INT DEFAULT int4('42'::text),p BOOL DEFAULT ((text(TRUE) = 'true') IS TRUE))", "CAST(", []string{"id", "s", "i", "b", "n", "f", "p"}, []string{"INT4", "TEXT", "INT4", "BOOL", "INT8", "INT4", "BOOL"}, want, want)
}

func TestReferenceDefaultDateCompoundControls(t *testing.T) {
	// Civil chronology reverses lexicographic order of these BC spellings.
	// lib/pq returns finite DATE as time.Time; native DATE returns owned text.
	defaultConsumerReferenceControls(t, "CREATE TABLE d(id INT,v DATE DEFAULT DATE '0002-01-01 BC',b BOOL DEFAULT (DATE '0002-01-01 BC' < DATE '0001-01-01 BC'),e BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),n BOOL DEFAULT ((DATE '0002-01-01 BC' < DATE '0001-01-01 BC') IS TRUE))", "DATE '0002-01-01 BC'", []string{"id", "v", "b", "e", "n"}, []string{"INT4", "DATE", "BOOL", "BOOL", "BOOL"}, []any{"0002-01-01 BC", true, true, true}, []any{time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC), true, true, true})
}

func TestReferenceDefaultCastDeferredInputControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			create := referenceExec("CREATE TABLE d(id INT,v INT DEFAULT CAST('oops'::text AS int4))", 0)
			selectRows := referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}})
			failure := func(q string) referenceStep {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
				s.errorStage = "execute"
				return s
			}
			t.Run("postgres-oracle", func(t *testing.T) {
				run := func(s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
				}
				run(create)
				run(referenceExec("INSERT INTO d VALUES(1,9),(2,NULL)", 2))
				for _, q := range []string{"INSERT INTO d(id) VALUES(3)", "INSERT INTO d VALUES(4,DEFAULT)"} {
					run(failure(q))
					run(selectRows)
				}
			})
			t.Run("native-controls-and-replay", func(t *testing.T) {
				native := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
				both := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				native(create)
				defaultConsumerReferenceRoundtrip(t, p, "CAST(", "d")
				both(referenceExec("INSERT INTO d VALUES(1,9),(2,NULL)", 2))
				defaultConsumerReferenceRoundtrip(t, p, "CAST(", "d")
				for _, q := range []string{"INSERT INTO d(id) VALUES(3)", "INSERT INTO d VALUES(4,DEFAULT)"} {
					both(failure(q))
					both(selectRows)
				}
			})
		})
	}
}

func TestReferenceDefaultCastParameterProhibitions(t *testing.T) {
	for _, expression := range []string{"int4($1)", "int8($1)", "bool($1)", "text($1)", "CAST($1 AS INT)", "$1::int4", "CAST(int4($1) AS TEXT)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%s", prepared, expression), func(t *testing.T) {
				p := newReferencePair(t)
				q := "CREATE TABLE bad(v INT DEFAULT " + expression + ")"
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(strings.Index(q, "$1") + 1)})
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, db, r, s)
					referenceRunStep(t, p.ctx, db, r, referenceImplicitError("SELECT * FROM bad", referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
				})
			})
		}
	}
}

func TestReferenceDefaultQuerySourceControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
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
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
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
				s := referenceExec("INSERT INTO d(id) SELECT 11", 1)
				s.prepared = prepared
				p.step(t, s)
				rows := append(append([][]any{}, want...), []any{int64(10), int64(7), "x", true}, []any{int64(11), int64(7), "x", true})
				s = selectRows(rows)
				s.prepared = prepared
				p.step(t, s)
			})
		})
	}
}

func TestReferenceDefaultQuerySourceAtomicityAndCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, source := range []string{"SELECT id,n FROM src ORDER BY id", "VALUES(1,9),(2,NULL) ORDER BY 1"} {
			t.Run(fmt.Sprintf("%v/%s", prepared, source), func(t *testing.T) {
				p := newReferencePair(t)
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
			p := newReferencePair(t)
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
				p := newReferencePair(t)
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
			p := newReferencePair(t)
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
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

func TestReferenceDefaultByteaControls(t *testing.T) {
	// The existing logical reference fixture records BYTEA as raw strings.
	// Native public controls separately assert actual []byte and its metadata.
	want := []any{string([]byte{0, 128, 255, 92, 39}), ""}
	defaultConsumerReferenceControls(t, `CREATE TABLE d(id INT,v BYTEA DEFAULT '\x0080ff5c27',e BYTEA DEFAULT '\x')`, `DEFAULT '\x0080ff5c27'`, []string{"id", "v", "e"}, []string{"INT4", "BYTEA", "BYTEA"}, want, want)
}

func TestReferenceDefaultQueryPlanningDecisive(t *testing.T) {
	// Source-derived expectations from REL_18_0's constant AND/OR planning,
	// independently exercised on PostgreSQL before the native assertion.
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"CAST(1 AS BOOL) OR CAST('oops'::text AS INT)=1", true},
		{"CAST(0 AS BOOL) AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
		{"(NULL IS NULL) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE IS TRUE) OR CAST('oops'::text AS INT)=1", true},
		{"(1 IS NOT DISTINCT FROM 1) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE OR CAST('oops'::text AS INT)=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", tc.expression, prepared, populated), func(t *testing.T) {
					p := newReferencePair(t)
					nativeComplete := false
					defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
						run(referenceExec("CREATE TABLE src(id INT)", 0))
						run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0))
						run(referenceExec("INSERT INTO d VALUES(0,NULL)", 1))
						count := int64(0)
						if populated {
							run(referenceExec("INSERT INTO src VALUES(1),(2)", 2))
							count = 2
						}
						run(referenceExec("INSERT INTO d(id,b) SELECT 9,NULL FROM src", count))
						run(referenceExec("INSERT INTO d(id) SELECT id FROM src ORDER BY id", count))
						want := [][]any{{int64(0), nil}}
						if populated {
							want = append(want, []any{int64(1), tc.want}, []any{int64(2), tc.want}, []any{int64(9), nil}, []any{int64(9), nil})
						}
						run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
						if db == p.memory {
							nativeComplete = true
						}
					})
					if !nativeComplete {
						return
					}
					t.Run("native-controls-and-replay", func(t *testing.T) {
						defaultConsumerReferenceRoundtrip(t, p, "'oops'", "d", "src")
						s := referenceExec("INSERT INTO d(id) SELECT id FROM src ORDER BY id", 0)
						want := [][]any{{int64(0), nil}}
						if populated {
							s.affected = 2
							want = [][]any{{int64(0), nil}, {int64(1), tc.want}, {int64(1), tc.want}, {int64(2), tc.want}, {int64(2), tc.want}, {int64(9), nil}, {int64(9), nil}}
						}
						s.prepared = prepared
						p.step(t, s)
						s = referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
						s.prepared = prepared
						p.step(t, s)
					})
				})
			}
		}
	}
}

func TestReferenceDefaultQueryPlanningRequiredBranches(t *testing.T) {
	for _, expression := range []string{
		"NULL OR CAST('oops'::text AS INT)=1",
		"NULL AND CAST('oops'::text AS INT)=1",
		"FALSE OR CAST('oops'::text AS INT)=1",
		"TRUE AND CAST('oops'::text AS INT)=1",
		"CAST('oops'::text AS INT)=1 OR TRUE",
		"CAST('oops'::text AS INT)=1 AND FALSE",
		"(TRUE OR CAST('discarded'::text AS INT)=1) AND CAST('oops'::text AS INT)=1",
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", expression, prepared, populated), func(t *testing.T) {
					p := newReferencePair(t)
					defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
						run(referenceExec("CREATE TABLE src(id INT)", 0))
						run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression+"))", 0))
						run(referenceExec("INSERT INTO d VALUES(0,TRUE)", 1))
						if populated {
							run(referenceExec("INSERT INTO src VALUES(1),(2)", 2))
						}
						s := referenceImplicitError("INSERT INTO d(id) SELECT id FROM src ORDER BY id", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
						s.errorStage = "execute"
						run(s)
						run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}}))
					})
				})
			}
		}
	}
}

func TestReferenceDefaultQueryPlanningAnalysis(t *testing.T) {
	for _, tc := range []struct{ expression, token, code, message string }{
		{"TRUE OR CAST($1 AS INT)=1", "$1", "42P02", "there is no parameter $1"},
		{"FALSE AND CAST(b AS INT)=1", "b AS", "0A000", "cannot use column reference in DEFAULT expression"},
		{"TRUE OR CAST('oops' AS INT)=1", "'oops'", "22P02", `invalid input syntax for type integer: "oops"`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					q := "CREATE TABLE bad(b BOOL DEFAULT (" + tc.expression + "))"
					s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(q, tc.token) + 1)})
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, db, r, s)
					referenceRunStep(t, p.ctx, db, r, referenceImplicitError("SELECT * FROM bad", referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
				})
			})
		}
	}
}
