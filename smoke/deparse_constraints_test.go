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
	deparseReviewSmoke(t, db)
}

func deparseReviewSmoke(t *testing.T, db *sql.DB) {
	badDetail := func(value string) string {
		if value == "NULL" {
			return "null"
		}
		return value
	}
	t.Helper()
	for i, tc := range []struct{ expression, good, bad string }{
		{"FALSE BETWEEN (a<2) AND TRUE", "2", "1"},
		{"a BETWEEN 1 AND 3 = TRUE", "2", "4"},
		{"round(a::numeric,1)>0", "2", "0"},
		{"num_nonnulls(a,NULL)>0", "2", "NULL"},
	} {
		original := fmt.Sprintf("review_original%d", i)
		constraint := fmt.Sprintf("review_c%d", i)
		notNullSmokeExec(t, db, false, "CREATE TABLE "+original+"(a INT,CONSTRAINT "+constraint+" CHECK("+tc.expression+"))", 0)
		for _, pretty := range []bool{false, true} {
			var suffix string
			if err := db.QueryRow("SELECT pg_get_constraintdef(oid,$1) FROM pg_catalog.pg_constraint WHERE conname=$2", pretty, constraint).Scan(&suffix); err != nil {
				t.Fatal(err)
			}
			copy := fmt.Sprintf("review_copy%d_%t", i, pretty)
			notNullSmokeExec(t, db, false, "CREATE TABLE "+copy+"(a INT,CONSTRAINT copied "+suffix+")", 0)
			notNullSmokeExec(t, db, true, "INSERT INTO "+copy+" VALUES("+tc.good+")", 1)
			_, err := db.Exec("INSERT INTO " + copy + " VALUES(" + tc.bad + ")")
			notNullSmokeDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "` + copy + `" violates check constraint "copied"`, Detail: "Failing row contains (" + badDetail(tc.bad) + ").", Schema: "public", Table: copy, Constraint: "copied"})
			if i != 3 {
				notNullSmokeExec(t, db, false, "INSERT INTO "+copy+" VALUES(NULL)", 1)
			}
		}
	}
	notNullSmokeExec(t, db, false, "CREATE TABLE review_time(a TIME DEFAULT TIME(3) '12:34:56.1234')", 0)
	for _, pretty := range []bool{false, true} {
		var expression string
		if err := db.QueryRow("SELECT pg_get_expr(d.adbin,0,$1) FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='review_time'", pretty).Scan(&expression); err != nil {
			t.Fatal(err)
		}
		if expression != "'12:34:56.1234'::time(3) without time zone" {
			t.Fatal(expression)
		}
		copy := fmt.Sprintf("review_time_%t", pretty)
		notNullSmokeExec(t, db, false, "CREATE TABLE "+copy+"(a TIME DEFAULT "+expression+")", 0)
		notNullSmokeExec(t, db, false, "INSERT INTO "+copy+" DEFAULT VALUES", 1)
		var got string
		if err := db.QueryRow("SELECT a::text FROM " + copy).Scan(&got); err != nil || got != "12:34:56.123" {
			t.Fatal(got, err)
		}
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	exec("CREATE SCHEMA review_shadow")
	exec("CREATE SEQUENCE public.review_s START 40")
	exec("CREATE SEQUENCE review_shadow.review_s START 900")
	exec("CREATE TABLE review_provider(a INT,CONSTRAINT review_provider_c CHECK(nextval('public.review_s')<100))")
	stmt, err := conn.PrepareContext(context.Background(), "SELECT pg_get_constraintdef(oid,$1) FROM pg_catalog.pg_constraint WHERE conname='review_provider_c'")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	observe := func(pretty bool, want string) {
		t.Helper()
		var direct, prepared string
		query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='review_provider_c'", pretty)
		if err := conn.QueryRowContext(context.Background(), query).Scan(&direct); err != nil {
			t.Fatal(err)
		}
		if err := stmt.QueryRowContext(context.Background(), pretty).Scan(&prepared); err != nil {
			t.Fatal(err)
		}
		if direct != want || prepared != want {
			t.Fatal(direct, prepared, want)
		}
	}
	exec("SET search_path TO review_shadow,public")
	for _, pretty := range []bool{false, true} {
		var suffix string
		if err := stmt.QueryRowContext(context.Background(), pretty).Scan(&suffix); err != nil {
			t.Fatal(err)
		}
		want := "CHECK ((nextval('public.review_s'::regclass) < 100))"
		if pretty {
			want = "CHECK (nextval('public.review_s'::regclass) < 100)"
		}
		if suffix != want {
			t.Fatal(suffix, want)
		}
		observe(pretty, want)
		var called bool
		if err := conn.QueryRowContext(context.Background(), "SELECT is_called FROM public.review_s").Scan(&called); err != nil || called {
			t.Fatal(called, err)
		}
		exec(fmt.Sprintf("CREATE TABLE public.review_provider_%t(a INT,CONSTRAINT copied %s)", pretty, suffix))
	}
	exec("SET search_path TO public,review_shadow")
	observe(false, "CHECK ((nextval('review_s'::regclass) < 100))")
	observe(true, "CHECK (nextval('review_s'::regclass) < 100)")
	exec("SET search_path TO review_shadow,public")
	exec("ALTER SEQUENCE public.review_s RENAME TO \"Renamed review sequence\"")
	var suffix string
	if err := stmt.QueryRowContext(context.Background(), true).Scan(&suffix); err != nil || suffix != `CHECK (nextval('"Renamed review sequence"'::regclass) < 100)` {
		t.Fatal(suffix, err)
	}
	observe(true, `CHECK (nextval('"Renamed review sequence"'::regclass) < 100)`)
	exec("INSERT INTO public.review_provider_false VALUES(1)")
	exec("INSERT INTO public.review_provider_true VALUES(1)")
	var last int64
	var called bool
	if err := conn.QueryRowContext(context.Background(), `SELECT last_value,is_called FROM public."Renamed review sequence"`).Scan(&last, &called); err != nil || last != 41 || !called {
		t.Fatal(last, called, err)
	}
	if err := conn.QueryRowContext(context.Background(), "SELECT last_value,is_called FROM review_shadow.review_s").Scan(&last, &called); err != nil || last != 900 || called {
		t.Fatal(last, called, err)
	}
	exec("SET search_path TO public")
}
