package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsSmoke(t *testing.T) {
	db := alterDropSmokePool(t, new([]stuntgres.Error))
	ctx := context.Background()
	notNullSmokeExec(t, db, false, "CREATE TABLE t(a INT,b BIGINT DEFAULT 7 CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a::bigint>0))", 0)
	var cid, rid, did int64
	for _, tc := range []struct {
		query string
		dst   *int64
	}{
		{"SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'", &cid},
		{"SELECT conrelid FROM pg_catalog.pg_constraint WHERE conname='ck'", &rid},
		{"SELECT oid FROM pg_catalog.pg_attrdef", &did},
	} {
		if err := db.QueryRow(tc.query).Scan(tc.dst); err != nil || *tc.dst <= 0 {
			t.Fatal(tc.query, *tc.dst, err)
		}
	}
	if cid == rid || cid == did || rid == did {
		t.Fatal("colliding identities", cid, rid, did)
	}
	text := func(prepared bool, q string, want any, args ...any) {
		t.Helper()
		var r *sql.Rows
		var err error
		if prepared {
			s, e := db.Prepare(q)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r, err = s.Query(args...)
		} else {
			r, err = db.Query(q, args...)
		}
		if err != nil {
			t.Fatal(q, err)
		}
		defer r.Close()
		ct, e := r.ColumnTypes()
		if e != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "TEXT" {
			t.Fatal(ct, e)
		}
		if !r.Next() {
			t.Fatal("missing row", r.Err())
		}
		var got any
		if e = r.Scan(&got); e != nil || got != want {
			t.Fatal(q, got, want, e)
		}
		if r.Next() || r.Err() != nil {
			t.Fatal("extra row", r.Err())
		}
	}
	for _, prepared := range []bool{false, true} {
		text(prepared, "SELECT pg_get_constraintdef($1)", "CHECK (((a)::bigint > 0))", cid)
		text(prepared, "SELECT pg_get_constraintdef($1,$2)", "CHECK (a::bigint > 0)", cid, true)
		text(prepared, "SELECT pg_get_expr(conbin,conrelid) FROM pg_catalog.pg_constraint WHERE conname='ck'", "((a)::bigint > 0)")
		text(prepared, "SELECT pg_get_expr(conbin,conrelid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", "a::bigint > 0")
		text(prepared, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", "7")
		text(prepared, "SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint WHERE conname='nn'", "NOT NULL b")
		text(prepared, "SELECT pg_get_constraintdef($1,NULL)", nil, cid)
		text(prepared, "SELECT pg_get_constraintdef(4294967295::oid)", nil)
		text(prepared, "SELECT pg_get_expr(adbin,4294967295::oid) FROM pg_catalog.pg_attrdef", nil)
	}
	_, err := db.Query("SELECT pg_get_expr(conbin,0) FROM pg_catalog.pg_constraint WHERE conname='ck'")
	notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "expression contains variables"})
	notNullSmokeExec(t, db, false, "CREATE TABLE other(z TEXT)", 0)
	text(true, "SELECT pg_get_expr(conbin,'other'::regclass) FROM pg_catalog.pg_constraint WHERE conname='ck'", "((z)::bigint > 0)")
	notNullSmokeExec(t, db, false, `ALTER TABLE t RENAME a TO "A renamed"`, 0)
	text(true, "SELECT pg_get_constraintdef($1,true)", `CHECK ("A renamed"::bigint > 0)`, cid)
	for _, pretty := range []bool{false, true} {
		var suffix string
		if err = db.QueryRow("SELECT pg_get_constraintdef($1,$2)", cid, pretty).Scan(&suffix); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("copy_%t", pretty)
		notNullSmokeExec(t, db, false, `CREATE TABLE `+name+`("A renamed" INT,CONSTRAINT copied `+suffix+`)`, 0)
		notNullSmokeExec(t, db, true, "INSERT INTO "+name+" VALUES(1),(NULL)", 2)
		for _, q := range []string{"INSERT INTO " + name + " VALUES(1),(0),(1)", `UPDATE ` + name + ` SET "A renamed"=0`} {
			_, err = db.Exec(q)
			notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "` + name + `" violates check constraint "copied"`, Detail: "Failing row contains (0).", Schema: "public", Table: name, Constraint: "copied"})
		}
		notNullSmokeQuery(t, db, `SELECT "A renamed" FROM `+name+` ORDER BY "A renamed" NULLS FIRST`, []string{"A renamed"}, []string{"INT4"}, []bool{true}, [][]any{{nil}, {int64(1)}})
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	text(false, "SELECT pg_get_constraintdef($1)", nil, cid)
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	text(true, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", `CHECK ("A renamed"::bigint > 0)`)
	text(false, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", "7")
}
