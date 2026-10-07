package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestSearchPathCurrentSchemas(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE SCHEMA sp2", "SET search_path TO missing,sp2,sp2,sp1"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	stmt, err := c.PrepareContext(ctx, "SELECT current_schemas($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	check := func(arg any, want any) {
		t.Helper()
		rows, err := stmt.QueryContext(ctx, arg)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 1 {
			t.Fatal(cols, err)
		}
		if nullable, known := cols[0].Nullable(); !known || !nullable {
			t.Fatal("NAME[] nullable metadata", nullable, known)
		}
		if cols[0].Name() != "current_schemas" || cols[0].DatabaseTypeName() != "_NAME" || cols[0].ScanType() != reflect.TypeOf("") {
			t.Fatalf("metadata: %s %s %v", cols[0].Name(), cols[0].DatabaseTypeName(), cols[0].ScanType())
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil || got != want {
			t.Fatalf("value %#v want %#v: %v", got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("unexpected trailing row", rows.Err())
		}
	}
	check(false, "{sp2,sp1}")
	check(true, "{pg_catalog,sp2,sp1}")
	check(nil, nil)
	if _, err := c.ExecContext(ctx, "SET search_path TO sp1,pg_catalog,sp2"); err != nil {
		t.Fatal(err)
	}
	check(false, "{sp1,pg_catalog,sp2}")
	check(true, "{sp1,pg_catalog,sp2}")
	if _, err := c.ExecContext(ctx, "SELECT set_config('search_path','',false)"); err != nil {
		t.Fatal(err)
	}
	check(false, "{}")
	check(true, "{pg_catalog}")
}

func TestSearchPathQualifiedProviders(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE TABLE sp1.t(id pg_catalog.int4)", "INSERT INTO sp1.t VALUES(7)", "SET search_path TO sp1"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, tc := range []struct {
		sql  string
		want any
	}{
		{"SELECT pg_catalog.num_nulls(NULL,7)", int64(1)},
		{"SELECT pg_catalog.current_schemas(false)", "{sp1}"},
		{"SELECT pg_catalog.current_schemas('false')", "{sp1}"},
		{"SELECT pg_catalog.current_setting('search_path')", "sp1"},
		{"SELECT CAST(id AS pg_catalog.int4) FROM sp1.t", int64(7)},
		{"SELECT id OPERATOR(pg_catalog.+) 1 FROM sp1.t", int64(8)},
		{"SELECT id OPERATOR(pg_catalog.=) 7 FROM sp1.t", true},
		{"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM sp1.t", int64(2)},
		{"SELECT OPERATOR(pg_catalog.-) id + 3 FROM sp1.t", int64(-10)},
		{"SELECT id - 2 + 3 FROM sp1.t", int64(8)},
	} {
		var got any
		if err := c.QueryRowContext(ctx, tc.sql).Scan(&got); err != nil || got != tc.want {
			t.Fatalf("%s: %#v want %#v: %v", tc.sql, got, tc.want, err)
		}
	}
}

func TestSearchPathMissingProviderDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE TABLE sp1.t(id INT)", "INSERT INTO sp1.t VALUES(7)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
	const operatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ sql, code, message, hint, position string }{
		{"SELECT current_schemas()", "42883", "function current_schemas() does not exist", functionHint, "8"},
		{"SELECT current_schemas(1)", "42883", "function current_schemas(integer) does not exist", functionHint, "8"},
		{"SELECT public.current_schemas(false)", "42883", "function public.current_schemas(boolean) does not exist", functionHint, "8"},
		{"SELECT public.current_setting('search_path')", "42883", "function public.current_setting(unknown) does not exist", functionHint, "8"},
		{"SELECT public.num_nulls(1)", "42883", "function public.num_nulls(integer) does not exist", functionHint, "8"},
		{"SELECT 1 OPERATOR(public.+) 2 FROM sp1.t", "42883", "operator does not exist: integer public.+ integer", operatorHint, "10"},
		{"SELECT 1::public.int4 FROM sp1.t", "42704", "type \"public.int4\" does not exist", "", "11"},
		{"SELECT missing_path_ns.num_nulls(1)", "3F000", `schema "missing_path_ns" does not exist`, "", "8"},
		{"SELECT pg_temp.num_nulls(1)", "3F000", `schema "pg_temp" does not exist`, "", "8"},
		{"SELECT 1 OPERATOR(missing_path_ns.+) 2 FROM sp1.t", "3F000", `schema "missing_path_ns" does not exist`, "", "10"},
		{"SELECT 1::missing_path_ns.int4 FROM sp1.t", "3F000", `schema "missing_path_ns" does not exist`, "", "11"},
	} {
		rows, err := db.Query(tc.sql)
		if rows != nil {
			rows.Close()
		}
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v (%v) want %#v", tc.sql, got, err, want)
		}
	}
}

func TestSearchPathQualifiedRandomProviders(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		sql  string
		want any
	}{
		{"SELECT pg_catalog.random(7,7)", int64(7)},
		{"SELECT pg_catalog.random_normal(7,0)", float64(7)},
		{"SELECT pg_catalog.setseed(0)", ""},
	} {
		var got any
		if err := db.QueryRow(tc.sql).Scan(&got); err != nil || got != tc.want {
			t.Fatal(tc.sql, got, tc.want, err)
		}
		stmt, err := db.Prepare(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		err = stmt.QueryRow().Scan(&got)
		stmt.Close()
		if err != nil || got != tc.want {
			t.Fatal(tc.sql, got, tc.want, err)
		}
	}
	_, err := db.Query("SELECT public.random()")
	var diagnostic *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function public.random() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(diagnostic, err, want)
	}
}

func TestSearchPathQualifiedMathProviders(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE SCHEMA math_path")
	execSQL(t, db, "CREATE TABLE math_path.source(id INT)")
	execSQL(t, db, "INSERT INTO math_path.source VALUES(7)")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reject := func(q string, want stuntgres.Error) {
		t.Helper()
		check := func(err error) {
			t.Helper()
			var diagnostic *stuntgres.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(q, diagnostic, err, want)
			}
		}
		rows, err := c.QueryContext(ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("unavailable math provider exposed cursor", q)
		}
		check(err)
		held, err := c.PrepareContext(ctx, q)
		if held != nil {
			held.Close()
			t.Fatal("unavailable math provider exposed statement", q)
		}
		check(err)
	}
	for _, tc := range []struct {
		name, args, typ, signature string
		want                       any
	}{
		{"min_scale", "8.4100", "INT4", "numeric", int64(2)},
		{"scale", "8.4100", "INT4", "numeric", int64(4)},
		{"trim_scale", "8.4100", "NUMERIC", "numeric", "8.41"},
		{"round", "1.25,1", "NUMERIC", "numeric, integer", "1.3"},
		{"trunc", "-1.25,1", "NUMERIC", "numeric, integer", "-1.2"},
		{"sign", "-13.000", "NUMERIC", "numeric", "-1"},
		{"mod", "-9,4", "INT4", "integer, integer", int64(-1)},
	} {
		checkRows := func(rows *sql.Rows) {
			t.Helper()
			defer rows.Close()
			metadata, err := rows.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].Name() != tc.name || metadata[0].DatabaseTypeName() != tc.typ || metadata[0].ScanType() != reflect.TypeOf(tc.want) {
				t.Fatal("qualified math metadata", metadata, err)
			}
			if !rows.Next() {
				t.Fatal("missing math row", rows.Err())
			}
			var got any
			if err := rows.Scan(&got); err != nil || got != tc.want {
				t.Fatal(tc.name, got, tc.want, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra math row", rows.Err())
			}
		}
		if _, err := c.ExecContext(ctx, "SET search_path TO public"); err != nil {
			t.Fatal(err)
		}
		q := "SELECT pg_catalog." + tc.name + "(" + tc.args + ")"
		rows, err := c.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		checkRows(rows)
		held, err := c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		for _, path := range []string{"public", "math_path"} {
			if _, err := c.ExecContext(ctx, "SET search_path TO "+path); err != nil {
				held.Close()
				t.Fatal(err)
			}
			rows, err := held.QueryContext(ctx)
			if err != nil {
				held.Close()
				t.Fatal(q, err)
			}
			checkRows(rows)
		}
		held.Close()
		rows, err = c.QueryContext(ctx, "SELECT "+tc.name+"("+tc.args+")")
		if err != nil {
			t.Fatal(err)
		}
		checkRows(rows)
		reject("SELECT public."+tc.name+"("+tc.args+")", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function public." + tc.name + "(" + tc.signature + ") does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	}
	reject("SELECT missing_path_ns.round(1.25,1)", stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "missing_path_ns" does not exist`, Position: "8"})
	var path string
	if err := c.QueryRowContext(ctx, "SHOW search_path").Scan(&path); err != nil || path != "math_path" {
		t.Fatal("failed math lookup changed path", path, err)
	}
	var id int64
	if err := c.QueryRowContext(ctx, "SELECT id FROM math_path.source").Scan(&id); err != nil || id != 7 {
		t.Fatal("failed math lookup changed source", id, err)
	}
}

func TestSearchPathQualifiedTemporalPrecision(t *testing.T) {
	for _, tc := range []struct {
		name, typ, input, databaseType string
		want                           any
	}{
		{"time", "pg_catalog.time(3)", "12:34:56.123789", "TIME", "12:34:56.124"},
		{"timestamp", "pg_catalog.timestamp(3)", "2000-01-01 12:34:56.123789", "TIMESTAMP", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
		{"time-quoted", `pg_catalog."time"(3)`, "12:34:56.123789", "TIME", "12:34:56.124"},
		{"timestamp-quoted", `pg_catalog."timestamp"(3)`, "2000-01-01 12:34:56.123789", "TIMESTAMP", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
	} {
		t.Run(tc.name+"/declaration", func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx := context.Background()
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			execSQL(t, db, "CREATE TABLE public.temporal(v "+tc.typ+")")
			execSQL(t, db, "INSERT INTO public.temporal VALUES('"+tc.input+"')")
			const query = "SELECT v FROM public.temporal"
			checkRows := func(rows *sql.Rows) {
				t.Helper()
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 1 || metadata[0].ScanType() != reflect.TypeOf(tc.want) {
					rows.Close()
					t.Fatal(metadata, err)
				}
				timeCheckRows(t, rows, []string{"v"}, []string{tc.databaseType}, [][]any{{tc.want}})
			}
			held, err := c.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, path := range []string{"public", "pg_catalog"} {
				if _, err := c.ExecContext(ctx, "SET search_path TO "+path); err != nil {
					t.Fatal(err)
				}
				rows, err := c.QueryContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				checkRows(rows)
				rows, err = held.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				checkRows(rows)
			}
		})
	}
}

func TestSearchPathNameArrayOrderingBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE public.path_array_order(id INT,flag BOOL)")
	for _, populated := range []bool{false, true} {
		if populated {
			execSQL(t, db, "INSERT INTO public.path_array_order VALUES(1,NULL)")
		}
		for _, q := range []string{
			"SELECT id FROM public.path_array_order ORDER BY current_schemas(false)",
			"SELECT current_schemas(false) AS paths FROM public.path_array_order ORDER BY paths",
			"SELECT current_schemas(flag) FROM public.path_array_order ORDER BY 1",
		} {
			want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type name[]", Hint: "Use an explicit ordering operator or modify the query.", Position: strconv.Itoa(strings.LastIndex(q, "ORDER BY ") + len("ORDER BY ") + 1)}
			check := func(err error) {
				t.Helper()
				var got *stuntgres.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatal(q, populated, got, err, want)
				}
			}
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
				t.Fatal("NAME[] ordering analysis exposed cursor", q)
			}
			check(err)
			held, err := db.Prepare(q)
			if held != nil {
				held.Close()
				t.Fatal("NAME[] ordering analysis exposed statement", q)
			}
			check(err)
		}
	}
}

func TestSearchPathQualifiedBitwiseProviders(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE public.path_bits(id INT,mask INT DEFAULT (7 OPERATOR(pg_catalog.&) 3))")
	execSQL(t, db, "INSERT INTO public.path_bits(id) VALUES(7)")
	for _, tc := range []struct {
		expression string
		want       int64
	}{
		{"id OPERATOR(pg_catalog.&) 3", 3},
		{"id OPERATOR(pg_catalog.|) 8", 15},
		{"id OPERATOR(pg_catalog.#) 3", 4},
		{"id OPERATOR(pg_catalog.<<) 1", 14},
		{"id OPERATOR(pg_catalog.>>) 1", 3},
		{"OPERATOR(pg_catalog.~) id", -8},
		{"CAST(id OPERATOR(pg_catalog.&) 3 AS pg_catalog.int2)", 3},
		{"mask", 3},
	} {
		q := "SELECT " + tc.expression + " FROM public.path_bits"
		var got int64
		if err := db.QueryRow(q).Scan(&got); err != nil || got != tc.want {
			t.Fatal(q, got, tc.want, err)
		}
		held, err := db.Prepare(q)
		if err != nil {
			t.Fatal(q, err)
		}
		err = held.QueryRow().Scan(&got)
		held.Close()
		if err != nil || got != tc.want {
			t.Fatal(q, got, tc.want, err)
		}
	}
	_, err := db.Query("SELECT id OPERATOR(public.&) 3 FROM public.path_bits")
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: integer public.& integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "11"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err, want)
	}
}
