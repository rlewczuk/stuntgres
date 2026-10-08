package stuntgres_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"stuntgres"
	"testing"
)

func TestStringCoreScalarNulls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE string_core_seed(id INT)")
	for _, tc := range []stringCoreCase{
		{"SELECT '['::text||CASE WHEN false THEN true ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN false ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::smallint ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::integer ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::bigint ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 42::oid ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 1.5::real ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 1.5::double precision ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 12.340::numeric ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc'::name ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '\\x4142'::bytea ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '550e8400-e29b-41d4-a716-446655440000'::uuid ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03'::date ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '04:05:06'::time ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '04:05:06+00'::timetz ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03 04:05:06'::timestamp ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '2001-02-03 04:05:06+00'::timestamptz ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN '1 day 02:03:04'::interval ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'string_core_seed'::regclass ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN setseed(0) ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc'::text ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc '::varchar ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT '['::text||CASE WHEN false THEN 'abc '::char(5) ELSE NULL END AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN true ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN false ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::smallint ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::integer ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::bigint ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 42::oid ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 1.5::real ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 1.5::double precision ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 12.340::numeric ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc'::name ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '\\x4142'::bytea ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '550e8400-e29b-41d4-a716-446655440000'::uuid ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03'::date ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '04:05:06'::time ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '04:05:06+00'::timetz ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03 04:05:06'::timestamp ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '2001-02-03 04:05:06+00'::timestamptz ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN '1 day 02:03:04'::interval ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'string_core_seed'::regclass ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN setseed(0) ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc'::text ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc '::varchar ELSE NULL END||']'::text AS v", nil, "TEXT"},
		{"SELECT CASE WHEN false THEN 'abc '::char(5) ELSE NULL END||']'::text AS v", nil, "TEXT"},
	} {
		t.Run(tc.query, func(t *testing.T) { stringCoreCheck(t, db, tc) })
	}
}
func TestStringCoreVoidEffects(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	randomSeed(t, c, 0)
	first := randomOne(t, c, "SELECT random()")
	for _, q := range []string{"SELECT 'x'::text||setseed(0)", "SELECT setseed(0)||'x'::text"} {
		for _, prepared := range []bool{false, true} {
			randomOne(t, c, "SELECT random()")
			var value string
			var err error
			if prepared {
				s, e := c.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				err = s.QueryRowContext(ctx).Scan(&value)
				s.Close()
			} else {
				err = c.QueryRowContext(ctx, q).Scan(&value)
			}
			if err != nil || value != "x" {
				t.Fatal(value, err)
			}
			if next := randomOne(t, c, "SELECT random()"); next != first {
				t.Fatal("volatile VOID child was lost", next, first)
			}
		}
	}
	// A volatile seed argument detects duplicated evaluation, not just dropped calls.
	randomSeed(t, c, 0)
	seedArgument := randomOne(t, c, "SELECT random()").(float64)
	randomSeed(t, c, seedArgument)
	wantNext := randomOne(t, c, "SELECT random()")
	for _, q := range []string{"SELECT 'x'::text||setseed(random())", "SELECT setseed(random())||'x'::text"} {
		for _, prepared := range []bool{false, true} {
			randomSeed(t, c, 0)
			var value string
			var err error
			if prepared {
				s, e := c.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				err = s.QueryRowContext(ctx).Scan(&value)
				s.Close()
			} else {
				err = c.QueryRowContext(ctx, q).Scan(&value)
			}
			if err != nil || value != "x" {
				t.Fatal(value, err)
			}
			if next := randomOne(t, c, "SELECT random()"); next != wantNext {
				t.Fatal("volatile child was not evaluated exactly once", next, wantNext)
			}
		}
	}
}
func TestStringCoreSettingsAndMetadata(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	check := func(stmt *sql.Stmt, arg any, want string) {
		t.Helper()
		rows, e := stmt.QueryContext(ctx, arg)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		types, e := rows.ColumnTypes()
		if e != nil || len(types) != 1 {
			t.Fatal(types, e)
		}
		typ := types[0]
		n, known := typ.Length()
		if typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() || !known || n != math.MaxInt64 {
			t.Fatal(typ, n, known)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got string
		if e = rows.Scan(&got); e != nil || got != want {
			t.Fatal(got, want, e)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	exec := func(q string) {
		t.Helper()
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(q, e)
		}
	}
	exec("SET DateStyle='ISO, MDY'")
	for _, tc := range []struct {
		typ             string
		input           any
		settings, wants []string
	}{
		{"bytea", []byte{0x41}, []string{"SET bytea_output='hex'", "SET bytea_output='escape'"}, []string{`x\x41`, "xA"}},
		{"date", "2001-02-03", []string{"SET DateStyle='ISO, MDY'", "SET DateStyle='SQL, DMY'"}, []string{"x2001-02-03", "x03/02/2001"}},
		{"timestamptz", "2001-02-03 04:05:06+00", []string{"SET TimeZone='UTC'", "SET TimeZone='Europe/Helsinki'"}, []string{"x2001-02-03 04:05:06+00", "x2001-02-03 06:05:06+02"}},
		{"interval", "1 day 02:03:04", []string{"SET IntervalStyle='postgres'", "SET IntervalStyle='iso_8601'"}, []string{"x1 day 02:03:04", "xP1DT2H3M4S"}},
	} {
		if tc.typ == "timestamptz" {
			exec("SET DateStyle='ISO, MDY'")
		}
		s, e := c.PrepareContext(ctx, "SELECT 'x'::text||$1::"+tc.typ+" AS v")
		if e != nil {
			t.Fatal(e)
		}
		for i, setting := range tc.settings {
			exec(setting)
			check(s, tc.input, tc.wants[i])
		}
		s.Close()
	}
	// Explicit mixed bool text conversion does not alter the ordinary bool adapter.
	var ordinary bool
	if err = c.QueryRowContext(ctx, "SELECT true FROM pg_catalog.pg_class WHERE relname='pg_class'").Scan(&ordinary); err != nil || !ordinary {
		t.Fatal(ordinary, err)
	}
}
