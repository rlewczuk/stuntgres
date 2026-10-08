package stuntgres_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestAlterDefaultEligibility(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
			t.Run(strconv.FormatBool(readonly)+action, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(i INT DEFAULT 11)")
				tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: readonly})
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec("ALTER TABLE t ALTER i " + action)
				want := stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
				if readonly {
					want = stuntgres.Error{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}
				}
				typeCoreDiagnostic(t, err, want)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				tableCoreExec(t, db, false, "INSERT INTO t DEFAULT VALUES", 1)
				wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(11)}})
			})
		}
	}
}

func TestAlterDefaultSyntaxAndCreatePositions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "ét"(i INT DEFAULT 7)`)
	for _, q := range []string{`ALTER TABLE "ét" ALTER i SET DEFAULT`, `ALTER TABLE "ét" ALTER i SET DEFAULT 9 garbage`} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(q)
				err = e
				if s != nil {
					s.Close()
				}
			} else {
				_, err = db.Exec(q)
			}
			want := stuntgres.Error{Code: "42601", Message: "syntax error at end of input", Position: strconv.Itoa(utf8.RuneCountInString(q) + 1)}
			if strings.HasSuffix(q, "garbage") {
				want.Message = `syntax error at or near "garbage"`
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "garbage")]) + 1)
			}
			typeCoreDiagnostic(t, err, want)
		}
	}
	tableCoreExec(t, db, false, `INSERT INTO "ét" DEFAULT VALUES`, 1)
	wantTuples(t, db, `SELECT * FROM "ét"`, [][]any{{int64(7)}})
	for _, tc := range []struct{ q, token, code, message string }{{"CREATE TABLE bad(i INT DEFAULT i)", "i)", "0A000", "cannot use column reference in DEFAULT expression"}, {"CREATE TABLE bad(i INT DEFAULT $1)", "$1", "42P02", "there is no parameter $1"}} {
		_, err := db.Exec(tc.q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
	}
}

func TestAlterDefaultEmptyDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE t(id INT,i INT DEFAULT 7)", 0)
			tableCoreExec(t, db, prepared, "CREATE TABLE src(id INT)", 0)
			tableCoreExec(t, db, prepared, "ALTER TABLE t ALTER i SET DEFAULT 2147483648", 0)
			q := "INSERT INTO t(id) SELECT id FROM src"
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
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			tableCoreExec(t, db, prepared, "INSERT INTO t(id,i) SELECT id,99 FROM src", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(0,99),(1,NULL)", 2)
			wantTuples(t, db, "SELECT * FROM t ORDER BY id", [][]any{{int64(0), int64(99)}, {int64(1), nil}})
		})
	}
}
