package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

// Expectations follow integer operator widths and truncation toward zero, not
// values obtained from the native engine. Only already-supported operators occur.
func TestDefaultArithmeticControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,a INT DEFAULT +7,b INT DEFAULT -7,c INT DEFAULT 1+2*3,g INT DEFAULT (1+2)*3,s INT DEFAULT 9-4-2,q INT DEFAULT -7/3,r INT DEFAULT -7%3,w BIGINT DEFAULT 1+2147483648)", 0)
			names := []string{"id", "a", "b", "c", "g", "s", "q", "r", "w"}
			types := []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT8"}
			defaultConsumerRows(t, db, prepared, names, types, nil)
			defaultArithmeticRoundtrip(t, db)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(1),(2)", 2)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id,a) VALUES(4,NULL)", 1)
			want := [][]any{{int64(1), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(2), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(3), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}, {int64(4), nil, int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)}}
			defaultConsumerRows(t, db, prepared, names, types, want)
			// Existing UPDATE remains a supplied-expression consumer, not UPDATE DEFAULT.
			tableCoreExec(t, db, prepared, "UPDATE d SET a=a+1 WHERE id=1", 1)
			want[0][1] = int64(8)
			defaultArithmeticRoundtrip(t, db)
			defaultConsumerRows(t, db, prepared, names, types, want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) SELECT 5", 1)
			want = append(want, []any{int64(5), int64(7), int64(-7), int64(7), int64(9), int64(3), int64(-2), int64(-1), int64(2147483649)})
			defaultConsumerRows(t, db, prepared, names, types, want)
		})
	}
}

func TestDefaultArithmeticPlanningFailures(t *testing.T) {
	for _, tc := range []struct{ expression, code, message string }{
		{"2147483647+1", "22003", "integer out of range"},
		{"1/0", "22012", "division by zero"},
		{"1%0", "22012", "division by zero"},
		{"-(-2147483647-1)", "22003", "integer out of range"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
				tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v INT DEFAULT "+tc.expression+")", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,9),(1,NULL)", 2)
				defaultArithmeticRoundtrip(t, db)
				seed := [][]any{{int64(0), int64(9)}, {int64(1), nil}}
				for _, q := range []string{
					"INSERT INTO d(id) VALUES(2),(3)", "INSERT INTO d VALUES(2,DEFAULT)",
					"INSERT INTO d (VALUES(2,DEFAULT))", "INSERT INTO d DEFAULT VALUES",
					"INSERT INTO d VALUES(2,9),(3,DEFAULT)", "INSERT INTO d(id) SELECT id FROM src",
				} {
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal("consumed arithmetic planning belongs to Execute", e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
					defaultConsumerRows(t, db, prepared, []string{"id", "v"}, []string{"INT4", "INT4"}, seed)
				}
				tableCoreExec(t, db, prepared, "INSERT INTO d(id,v) SELECT id,9 FROM src", 0)
				defaultConsumerRows(t, db, prepared, []string{"id", "v"}, []string{"INT4", "INT4"}, seed)
			})
		}
	}
}

func TestDefaultArithmeticPlanningDecisive(t *testing.T) {
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
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
				tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0)
				tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,NULL)", 1)
				defaultArithmeticRoundtrip(t, db)
				want := [][]any{{int64(0), nil}}
				for _, q := range []string{"INSERT INTO d(id) SELECT id FROM src", "INSERT INTO d(id) VALUES(1)", "INSERT INTO d VALUES(2,DEFAULT)"} {
					if tc.fails {
						var err error
						if prepared {
							s, e := db.Prepare(q)
							if e != nil {
								t.Fatal(e)
							}
							_, err = s.Exec()
							s.Close()
						} else {
							_, err = db.Exec(q)
						}
						typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
					} else {
						count := int64(1)
						if q == "INSERT INTO d(id) SELECT id FROM src" {
							count = 0
						}
						tableCoreExec(t, db, prepared, q, count)
						if count != 0 {
							want = append(want, []any{int64(len(want)), tc.value})
						}
					}
					defaultConsumerRows(t, db, prepared, []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
				}
			})
		}
	}
}

func TestDefaultCharacterCastControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,c CHAR(2) DEFAULT CAST('é🙂x' AS CHAR(2)),v VARCHAR(2) DEFAULT CAST('é🙂x' AS VARCHAR(2)),s TEXT DEFAULT CAST('a' AS CHAR(3)))", 0)
			check := func(want [][]any) {
				characterMetadata(t, db, "SELECT c,v,s FROM d", []string{"BPCHAR", "VARCHAR", "TEXT"}, []int64{2, 2, -1})
				wantTuples(t, db, "SELECT * FROM d ORDER BY id", want)
			}
			check([][]any{})
			defaultArithmeticRoundtrip(t, db)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) VALUES(1)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(2,DEFAULT,DEFAULT,DEFAULT)", 1)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(3,NULL,NULL,NULL)", 1)
			want := [][]any{{int64(1), "é🙂", "é🙂", "a"}, {int64(2), "é🙂", "é🙂", "a"}, {int64(3), nil, nil, nil}}
			check(want)
			defaultArithmeticRoundtrip(t, db)
			check(want)
			tableCoreExec(t, db, prepared, "INSERT INTO d(id) SELECT 4", 1)
			want = append(want, []any{int64(4), "é🙂", "é🙂", "a"})
			check(want)
		})
	}
}

func TestDefaultCharacterCastDeferredAssignment(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
			tableCoreExec(t, db, prepared, "CREATE TABLE d(id INT,v VARCHAR(2) DEFAULT CAST('long' AS TEXT))", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO d VALUES(0,'ok'),(1,NULL)", 2)
			defaultArithmeticRoundtrip(t, db)
			for _, q := range []string{"INSERT INTO d(id) VALUES(2)", "INSERT INTO d VALUES(2,DEFAULT)", "INSERT INTO d VALUES(2,'ok'),(3,DEFAULT)", "INSERT INTO d(id) SELECT id FROM src"} {
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("assignment must be deferred until Execute", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22001", Message: "value too long for type character varying(2)"})
				wantTuples(t, db, "SELECT * FROM d ORDER BY id", [][]any{{int64(0), "ok"}, {int64(1), nil}})
			}
			tableCoreExec(t, db, prepared, "INSERT INTO d(id,v) SELECT id,'ok' FROM src", 0)
		})
	}
}

// Persistence is proved by actual replay and subsequent behavior, not SQL
// substrings. Deterministic redump is the existing native control contract.
func defaultArithmeticRoundtrip(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("non-deterministic default redump")
	}
}
