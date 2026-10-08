package smoke_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestCheckSmoke(t *testing.T) {
	db := alterDropSmokePool(t, new([]stuntgres.Error))
	ctx := context.Background()
	notNullSmokeExec(t, db, false, "CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0)
	notNullSmokeExec(t, db, false, "INSERT INTO t VALUES(1)", 1)
	stmt, err := db.Prepare("INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, v := range []any{nil, 2, 1} {
		r, e := stmt.Exec(v)
		if v == 2 {
			if r != nil {
				t.Fatal(r)
			}
			notNullSmokeDiagnostic(t, e, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "only_one"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "only_one"})
		} else {
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
		}
	}
	want := [][]any{{nil}, {int64(1)}, {int64(1)}}
	for _, q := range []string{"INSERT INTO t VALUES(1),(2),(1)", "UPDATE t SET a=2"} {
		r, e := db.Exec(q)
		if r != nil {
			t.Fatal(r)
		}
		notNullSmokeDiagnostic(t, e, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "only_one"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "only_one"})
		notNullSmokeQuery(t, db, "SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, []bool{true}, want)
	}
	notNullSmokeExec(t, db, false, "CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))", 0)
	notNullSmokeExec(t, db, false, "INSERT INTO u VALUES(1),(2)", 2)
	_, err = db.Exec("UPDATE u SET a=a+1")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "u" violates check constraint "c"`, Detail: "Failing row contains (3).", Schema: "public", Table: "u", Constraint: "c"})
	notNullSmokeQuery(t, db, "SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{true}, [][]any{{int64(1)}, {int64(2)}})
	_, err = db.Exec("CREATE TABLE bad(a INT CHECK(a))")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer", Position: "30"})
	notNullSmokeExec(t, db, false, "CREATE TABLE bad(a INT)", 0)
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), `CONSTRAINT "only_one" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
		t.Fatal(dump.String())
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	notNullSmokeQuery(t, db, "SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, []bool{true}, want)
	_, err = db.Exec("INSERT INTO t VALUES(2)")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "only_one"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "only_one"})
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, again.String())
	}
}
