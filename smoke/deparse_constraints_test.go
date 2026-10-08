package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
	"time"
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
	deparseReviewConsumerSmoke(t, db)
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

func deparseReviewConsumerSmoke(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	for i, tc := range []struct{ columns, input, compact, pretty, good, bad, detail, code, message string }{
		{"n NUMERIC(4,2)", "n<1000", "CHECK ((n < (1000)::numeric))", "CHECK (n < 1000::numeric)", "(1),(NULL)", "", "", "", ""},
		{"n NUMERIC(4,2)", "n<CAST(1000 AS NUMERIC(4,2))", "CHECK ((n < (1000)::numeric(4,2)))", "CHECK (n < 1000::numeric(4,2))", "", "(1)", "A field with precision 4, scale 2 must round to an absolute value less than 10^2.", "22003", "numeric field overflow"},
		{"a INT,flag BOOL", "CASE WHEN flag THEN a ELSE 2 END>0", "CHECK ((\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0))", "CHECK (\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "(1,true),(0,false),(NULL,true)", "(0,true)", "0, t", "23514", ""},
		{"id INT,b BOOL", "current_schemas(b) IS NOT NULL", "CHECK ((current_schemas(b) IS NOT NULL))", "CHECK (current_schemas(b) IS NOT NULL)", "(1,true),(2,false)", "(3,NULL)", "3, null", "23514", ""},
		{"id INT,v TIMESTAMP", "(v AT LOCAL)>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "CHECK (((v AT LOCAL) > '2026-10-08 00:00:00+00'::timestamp with time zone))", "CHECK ((v AT LOCAL) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(1,'2026-10-08 01:00:00'),(2,NULL)", "(3,'2026-10-07 23:00:00')", "3, 2026-10-07 23:00:00", "23514", ""},
		{"p DOUBLE PRECISION", "random_normal(5,0)>p", "CHECK ((random_normal((5)::double precision, (0)::double precision) > p))", "CHECK (random_normal(5::double precision, 0::double precision) > p)", "(4),(NULL)", "(5)", "5", "23514", ""},
		{"a INT,flag BOOL", "CASE WHEN flag THEN a END>0", "CHECK ((\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0))", "CHECK (\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0)", "(1,true),(0,false),(NULL,true)", "(0,true)", "0, t", "23514", ""},
		{"a INT,flag BOOL", "CASE WHEN flag THEN 1 ELSE 1/a END>0", "CHECK ((\nCASE\n    WHEN flag THEN 1\n    ELSE (1 / a)\nEND > 0))", "CHECK (\nCASE\n    WHEN flag THEN 1\n    ELSE 1 / a\nEND > 0)", "(0,true),(1,false),(NULL,false)", "(0,false)", "", "22012", "division by zero"},
		{"a INT,b INT", "a IS NOT DISTINCT FROM b", "CHECK ((NOT (a IS DISTINCT FROM b)))", "CHECK (NOT a IS DISTINCT FROM b)", "(1,1),(NULL,NULL)", "(1,2)", "1, 2", "23514", ""},
		{"a INT,b INT,c INT,expected INT", "(a-b)-c=expected", "CHECK ((((a - b) - c) = expected))", "CHECK ((a - b - c) = expected)", "(10,6,2,2),(NULL,6,2,2)", "(10,6,2,6)", "10, 6, 2, 6", "23514", ""},
		{"id INT,v TIMESTAMP", "(v AT TIME ZONE 'UTC')>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "CHECK (((v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone))", "CHECK ((v AT TIME ZONE 'UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(1,'2026-10-08 01:00:00'),(2,NULL)", "(3,'2026-10-07 23:00:00')", "3, 2026-10-07 23:00:00", "23514", ""},
		{"id INT,v TIMESTAMP", "(v AT TIME ZONE 'Etc/UTC')>TIMESTAMPTZ '2026-10-08 00:00:00+00'", "CHECK (((v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone))", "CHECK ((v AT TIME ZONE 'Etc/UTC'::text) > '2026-10-08 00:00:00+00'::timestamp with time zone)", "(1,'2026-10-08 01:00:00'),(2,NULL)", "(3,'2026-10-07 23:00:00')", "3, 2026-10-07 23:00:00", "23514", ""},
	} {
		original := fmt.Sprintf("consumer_original%d", i)
		constraint := fmt.Sprintf("consumer_c%d", i)
		notNullSmokeExec(t, db, false, "SET TIME ZONE 'UTC'", 0)
		notNullSmokeExec(t, db, false, "CREATE TABLE "+original+"("+tc.columns+",CONSTRAINT "+constraint+" CHECK("+tc.input+"))", 0)
		for mode, want := range []string{tc.compact, tc.pretty} {
			var actual string
			if err := db.QueryRow("SELECT pg_get_constraintdef(oid,$1) FROM pg_catalog.pg_constraint WHERE conname=$2", mode == 1, constraint).Scan(&actual); err != nil || actual != want {
				t.Fatal(actual, want, err)
			}
			name := fmt.Sprintf("consumer_copy%d_%d", i, mode)
			notNullSmokeExec(t, db, false, "CREATE TABLE "+name+"("+tc.columns+",CONSTRAINT copied "+actual+")", 0)
			for _, prepared := range []bool{false, true} {
				notNullSmokeExec(t, db, false, "DELETE FROM "+name, 0)
				if tc.good != "" {
					count := int64(2)
					if i == 2 || i == 6 || i == 7 {
						count = 3
					}
					notNullSmokeExec(t, db, prepared, "INSERT INTO "+name+" VALUES"+tc.good, count)
				}
				if tc.bad != "" {
					var err error
					if prepared {
						s, e := db.Prepare("INSERT INTO " + name + " VALUES" + tc.bad)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec("INSERT INTO " + name + " VALUES" + tc.bad)
					}
					wantError := stuntgres.Error{Code: tc.code, Message: tc.message, Detail: tc.detail}
					if tc.code == "23514" {
						wantError.Message = `new row for relation "` + name + `" violates check constraint "copied"`
						wantError.Detail = "Failing row contains (" + tc.detail + ")."
						wantError.Schema = "public"
						wantError.Table = name
						wantError.Constraint = "copied"
					}
					notNullSmokeDiagnostic(t, err, wantError)
				}
				if i == 0 {
					rows, err := db.Query("SELECT n FROM " + name + " ORDER BY n NULLS FIRST")
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 1 {
						rows.Close()
						t.Fatal(metadata, err)
					}
					precision, scale, known := metadata[0].DecimalSize()
					if !known || precision != 4 || scale != 2 {
						rows.Close()
						t.Fatal(precision, scale, known)
					}
					notNullSmokeRows(t, rows, []string{"n"}, []string{"NUMERIC"}, []bool{true}, [][]any{{nil}, {"1.00"}})
				}
				if i == 2 {
					notNullSmokeQuery(t, db, "SELECT a,flag FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, [][]any{{nil, true}, {int64(0), false}, {int64(1), true}})
				}
				if i == 3 {
					notNullSmokeExec(t, db, false, "CREATE SCHEMA path_first_"+name, 0)
					notNullSmokeExec(t, db, false, "CREATE SCHEMA path_second_"+name, 0)
					held, e := db.Prepare("SELECT current_schemas(b) FROM public." + name + " WHERE id=$1")
					if e != nil {
						t.Fatal(e)
					}
					for _, reverse := range []bool{false, true} {
						first, second := "path_first_"+name, "path_second_"+name
						if reverse {
							first, second = second, first
						}
						notNullSmokeExec(t, db, false, "SET search_path TO "+first+","+second, 0)
						for id, want := range []string{"{pg_catalog," + first + "," + second + "}", "{" + first + "," + second + "}"} {
							var got string
							if e := held.QueryRow(id + 1).Scan(&got); e != nil || got != want {
								t.Fatal(got, want, e)
							}
						}
					}
					held.Close()
					notNullSmokeExec(t, db, false, "SET search_path TO public", 0)
					notNullSmokeExec(t, db, false, "DROP SCHEMA path_first_"+name, 0)
					notNullSmokeExec(t, db, false, "DROP SCHEMA path_second_"+name, 0)
					notNullSmokeQuery(t, db, "SELECT id,b FROM "+name+" ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, []bool{true, true}, [][]any{{int64(1), true}, {int64(2), false}})
				}
				if i == 4 {
					held, e := db.Prepare("INSERT INTO " + name + " VALUES(7,$1)")
					if e != nil {
						t.Fatal(e)
					}
					if _, e := held.Exec("2026-10-08 01:00:00"); e != nil {
						t.Fatal(e)
					}
					notNullSmokeExec(t, db, false, "SET TIME ZONE 'UTC-2'", 0)
					_, e = held.Exec("2026-10-08 01:00:00")
					notNullSmokeDiagnostic(t, e, stuntgres.Error{Code: "23514", Message: `new row for relation "` + name + `" violates check constraint "copied"`, Detail: "Failing row contains (7, 2026-10-08 01:00:00).", Schema: "public", Table: name, Constraint: "copied"})
					var instant time.Time
					if e := db.QueryRow("SELECT v AT LOCAL FROM " + name + " WHERE id=1").Scan(&instant); e != nil || !instant.Equal(time.Date(2026, 10, 7, 23, 0, 0, 0, time.UTC)) {
						t.Fatal(instant, e)
					}
					held.Close()
					notNullSmokeExec(t, db, false, "SET TIME ZONE 'UTC'", 0)
				}
				if i == 5 {
					var ignored any
					if e := db.QueryRow("SELECT setseed(0.25)").Scan(&ignored); e != nil {
						t.Fatal(e)
					}
					var control, got float64
					if e := db.QueryRow("SELECT random_normal(5,0)").Scan(&control); e != nil || control != 5 {
						t.Fatal(control, e)
					}
					if e := db.QueryRow("SELECT random()").Scan(&control); e != nil {
						t.Fatal(e)
					}
					if e := db.QueryRow("SELECT setseed(0.25)").Scan(&ignored); e != nil {
						t.Fatal(e)
					}
					notNullSmokeExec(t, db, prepared, "INSERT INTO "+name+" VALUES(4)", 1)
					if e := db.QueryRow("SELECT random()").Scan(&got); e != nil || got != control {
						t.Fatal(got, control, e)
					}
				}
				if i == 6 {
					notNullSmokeQuery(t, db, "SELECT a,flag FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, [][]any{{nil, true}, {int64(0), false}, {int64(1), true}})
				}
				if i == 7 {
					notNullSmokeQuery(t, db, "SELECT a,flag FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "flag"}, []string{"INT4", "BOOL"}, []bool{true, true}, [][]any{{nil, false}, {int64(0), true}, {int64(1), false}})
				}
				if i == 8 {
					_, e := db.Exec("INSERT INTO " + name + " VALUES(1,NULL)")
					notNullSmokeDiagnostic(t, e, stuntgres.Error{Code: "23514", Message: `new row for relation "` + name + `" violates check constraint "copied"`, Detail: "Failing row contains (1, null).", Schema: "public", Table: name, Constraint: "copied"})
					notNullSmokeQuery(t, db, "SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, [][]any{{nil, nil}, {int64(1), int64(1)}})
				}
				if i == 9 {
					notNullSmokeQuery(t, db, "SELECT a,b,c,expected FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "b", "c", "expected"}, []string{"INT4", "INT4", "INT4", "INT4"}, []bool{true, true, true, true}, [][]any{{nil, int64(6), int64(2), int64(2)}, {int64(10), int64(6), int64(2), int64(2)}})
				}
				if i == 10 || i == 11 {
					stored := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
					retained := [][]any{{int64(1), stored}, {int64(2), nil}}
					snapshot := func() {
						notNullSmokeQuery(t, db, "SELECT id,v FROM "+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TIMESTAMP"}, []bool{true, true}, retained)
					}
					snapshot()
					held, e := db.Prepare("INSERT INTO " + name + " VALUES(7,$1)")
					if e != nil {
						t.Fatal(e)
					}
					defer held.Close()
					if _, e := held.Exec("2026-10-08 01:00:00"); e != nil {
						t.Fatal(e)
					}
					retained = append(retained, []any{int64(7), stored})
					notNullSmokeExec(t, db, false, "SET TIME ZONE 'UTC-2'", 0)
					_, e = held.Exec("2026-10-08 01:00:00")
					zone, hour := "UTC", 23
					day := 7
					if i == 10 {
						notNullSmokeDiagnostic(t, e, stuntgres.Error{Code: "23514", Message: `new row for relation "` + name + `" violates check constraint "copied"`, Detail: "Failing row contains (7, 2026-10-08 01:00:00).", Schema: "public", Table: name, Constraint: "copied"})
					} else {
						if e != nil {
							t.Fatal(e)
						}
						retained = append(retained, []any{int64(7), stored})
						zone, hour, day = "Etc/UTC", 1, 8
					}
					var instant time.Time
					if e := db.QueryRow("SELECT v AT TIME ZONE '" + zone + "' FROM " + name + " WHERE id=1").Scan(&instant); e != nil || !instant.Equal(time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)) {
						t.Fatal(instant, e)
					}
					notNullSmokeExec(t, db, false, "SET TIME ZONE 'UTC'", 0)
					snapshot()
					held.Close()
				}
				count := int64(2)
				if i == 1 {
					count = 0
				}
				if i == 2 || i == 4 || i == 5 || i == 6 || i == 7 {
					count = 3
				}
				if i == 10 {
					count = 3
				}
				if i == 11 {
					count = 4
				}
				notNullSmokeExec(t, db, false, "DELETE FROM "+name, count)
			}
		}
	}
}
