//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceDefaultArithmeticControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			names := []string{"id", "a", "b", "c", "g", "s", "q", "r", "w"}
			types := []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT8"}
			// Independently calculated: precedence, left associativity, signed
			// quotient/remainder and INT4+INT8 promotion. No native goldens.
			want := [][]any{{int64(1), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(2), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(3), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(4), nil, int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}}
			nativeComplete := false
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
				run(referenceExec("CREATE TABLE d(id INT,a INT DEFAULT +7,b INT DEFAULT -7,c INT DEFAULT 1+2*3,g INT DEFAULT (1+2)*3,s INT DEFAULT 9-4-2,q INT DEFAULT -7/3,r INT DEFAULT -7%3,w BIGINT DEFAULT 1+2147483648)", 0))
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, nil))
				run(referenceExec("INSERT INTO d(id) VALUES(1),(2)", 2))
				run(referenceExec("INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
				run(referenceExec("INSERT INTO d(id,a) VALUES(4,NULL)", 1))
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
				run(referenceExec("UPDATE d SET a=a+1 WHERE id=1", 1))
				updated := [][]any{{int64(1), int64(8), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, want[1], want[2], want[3]}
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, updated))
				if db == p.memory {
					nativeComplete = true
				}
			})
			if !nativeComplete {
				return
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				defaultArithmeticReferenceRoundtrip(t, p, "d")
				want[0][1] = int64(8)
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
				run(referenceExec("INSERT INTO d(id) SELECT 5", 1))
				want = append(want, []any{int64(5), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)})
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
			})
		})
	}
}

func TestReferenceDefaultArithmeticPlanningFailures(t *testing.T) {
	for _, tc := range []struct{ expression, code, message string }{
		{"2147483647+1", "22003", "integer out of range"},
		{"1/0", "22012", "division by zero"},
		{"1%0", "22012", "division by zero"},
		{"-(-2147483647-1)", "22003", "integer out of range"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
					run(referenceExec("CREATE TABLE src(id INT)", 0))
					run(referenceExec("CREATE TABLE d(id INT,v INT DEFAULT "+tc.expression+")", 0))
					run(referenceExec("INSERT INTO d VALUES(0,9),(1,NULL)", 2))
					if db == p.memory {
						defaultArithmeticReferenceRoundtrip(t, p, "d", "src")
					}
					for _, q := range []string{"INSERT INTO d(id) VALUES(2),(3)", "INSERT INTO d VALUES(2,DEFAULT)", "INSERT INTO d (VALUES(2,DEFAULT))", "INSERT INTO d DEFAULT VALUES", "INSERT INTO d VALUES(2,9),(3,DEFAULT)", "INSERT INTO d(id) SELECT id FROM src"} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message})
						s.errorStage = "execute"
						run(s)
						run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(9)}, {int64(1), nil}}))
					}
					run(referenceExec("INSERT INTO d(id,v) SELECT id,9 FROM src", 0))
				})
			})
		}
	}
}

func TestReferenceDefaultArithmeticPlanningDecisive(t *testing.T) {
	for _, tc := range []struct {
		expression string
		value      bool
		fails      bool
	}{
		{"(1+2=3) OR 1/0=0", true, false},
		{"(2*3=7) AND 1/0=0", false, false},
		{"TRUE OR 2147483647+1=0", true, false},
		{"FALSE AND 2147483647+1=0", false, false},
		{"(1+2=4) OR 1/0=0", false, true},
		{"1/0=0 OR TRUE", false, true},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				p := newReferencePair(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
					run(referenceExec("CREATE TABLE src(id INT)", 0))
					run(referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0))
					run(referenceExec("INSERT INTO d VALUES(0,NULL)", 1))
					if db == p.memory {
						defaultArithmeticReferenceRoundtrip(t, p, "d", "src")
					}
					want := [][]any{{int64(0), nil}}
					for _, q := range []string{"INSERT INTO d(id) SELECT id FROM src", "INSERT INTO d(id) VALUES(1)", "INSERT INTO d VALUES(2,DEFAULT)"} {
						if tc.fails {
							s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
							s.errorStage = "execute"
							run(s)
						} else {
							count := int64(1)
							if q == "INSERT INTO d(id) SELECT id FROM src" {
								count = 0
							}
							run(referenceExec(q, count))
							if count != 0 {
								want = append(want, []any{int64(len(want)), tc.value})
							}
						}
						run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
					}
				})
			})
		}
	}
}

func TestReferenceDefaultCharacterCastControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			want := [][]any{{int64(1), "é🙂", "é🙂", "a"}, {int64(2), "é🙂", "é🙂", "a"}, {int64(3), nil, nil, nil}}
			names, types := []string{"id", "c", "v", "s"}, []string{"INT4", "BPCHAR", "VARCHAR", "TEXT"}
			nativeComplete := false
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
				run(referenceExec("CREATE TABLE d(id INT,c CHAR(2) DEFAULT CAST('é🙂x' AS CHAR(2)),v VARCHAR(2) DEFAULT CAST('é🙂x' AS VARCHAR(2)),s TEXT DEFAULT CAST('a' AS CHAR(3)))", 0))
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, nil))
				run(referenceExec("INSERT INTO d(id) VALUES(1)", 1))
				run(referenceExec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT)", 1))
				run(referenceExec("INSERT INTO d VALUES(3,NULL,NULL,NULL)", 1))
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
				if db == p.memory {
					nativeComplete = true
				}
			})
			if !nativeComplete {
				return
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				defaultArithmeticReferenceRoundtrip(t, p, "d")
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
				run(referenceExec("INSERT INTO d(id) SELECT 4", 1))
				want = append(want, []any{int64(4), "é🙂", "é🙂", "a"})
				run(referenceSelect("SELECT * FROM d ORDER BY id", names, types, want))
			})
		})
	}
}

func TestReferenceDefaultCharacterCastDeferredAssignment(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *referenceRecorder) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, db, r, s) }
				run(referenceExec("CREATE TABLE src(id INT)", 0))
				run(referenceExec("CREATE TABLE d(id INT,v VARCHAR(2) DEFAULT CAST('long' AS TEXT))", 0))
				run(referenceExec("INSERT INTO d VALUES(0,'ok'),(1,NULL)", 2))
				if db == p.memory {
					defaultArithmeticReferenceRoundtrip(t, p, "d", "src")
				}
				for _, q := range []string{"INSERT INTO d(id) VALUES(2)", "INSERT INTO d VALUES(2,DEFAULT)", "INSERT INTO d VALUES(2,'ok'),(3,DEFAULT)", "INSERT INTO d(id) SELECT id FROM src"} {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "22001", Message: "value too long for type character varying(2)"})
					s.errorStage = "execute"
					run(s)
					run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARCHAR"}, [][]any{{int64(0), "ok"}, {int64(1), nil}}))
				}
				run(referenceExec("INSERT INTO d(id,v) SELECT id,'ok' FROM src", 0))
			})
		})
	}
}

func defaultArithmeticReferenceRoundtrip(t *testing.T, p *referencePair, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("non-deterministic default redump")
	}
	for _, table := range tables {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL replay", err)
	}
}
