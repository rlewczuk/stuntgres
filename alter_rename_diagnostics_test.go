package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestAlterRenameDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE t(a INT,b TEXT,"q""u\z" INT)`)
	execSQL(t, db, "CREATE TABLE u(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7,'keep',9)")
	var before bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ q, code, message string }{
		{"ALTER TABLE t RENAME missing TO b", "42703", `column "missing" does not exist`},
		{"ALTER TABLE t RENAME a TO b", "42701", `column "b" of relation "t" already exists`},
		{"ALTER TABLE t RENAME a TO a", "42701", `column "a" of relation "t" already exists`},
		{"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`},
		{"ALTER TABLE t RENAME TO u", "42P07", `relation "u" already exists`},
		{`ALTER TABLE t RENAME a TO "q""u\z"`, "42701", `column "q"u\z" of relation "t" already exists`},
		{`ALTER TABLE t RENAME "abs""ent\z" TO b`, "42703", `column "abs"ent\z" does not exist`},
	}
	for _, system := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		cases = append(cases, struct{ q, code, message string }{"ALTER TABLE t RENAME " + system + " TO b", "0A000", `cannot rename system column "` + system + `"`}, struct{ q, code, message string }{"ALTER TABLE t RENAME a TO " + system, "42701", `column name "` + system + `" conflicts with a system column name`})
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				s, e := db.Prepare(tc.q)
				if e != nil {
					t.Fatal("semantic utility failed Prepare", e)
				}
				_, err = s.Exec()
				s.Close()
			} else {
				_, err = db.Exec(tc.q)
			}
			alterDropDiagnostic(t, err, tc.code, tc.message, "")
			var after bytes.Buffer
			if err = stuntgres.Dump(context.Background(), db, &after); err != nil || before.String() != after.String() {
				t.Fatal("failure changed dump", err)
			}
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec("ALTER TABLE t RENAME TO blocked")
	alterDropDiagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction", "")
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
