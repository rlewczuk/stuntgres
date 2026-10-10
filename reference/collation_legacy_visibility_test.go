package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceCollationLegacyVisibility(t *testing.T) {
	schemaReferenceCleanup(t, "a")
	schemaReferenceCleanup(t, "b")
	collationScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend) {
		ctx := context.Background()
		db := backend.db
		db.SetMaxOpenConns(2)
		exec := func(q string) {
			t.Helper()
			r, err := db.ExecContext(ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
			count, err := r.RowsAffected()
			if err != nil || count != 0 {
				t.Fatal(q, count, err)
			}
		}
		check := func(rows *sql.Rows, err error, label string, want any) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			labels, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(labels, []string{label}) {
				t.Fatal(labels, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" || types[0].ScanType() != reflect.TypeFor[bool]() {
				t.Fatal(types, err)
			}
			if !rows.Next() {
				t.Fatal("missing row", rows.Err())
			}
			var got any
			if err := rows.Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want || rows.Next() || rows.Err() != nil {
				t.Fatal(got, want, rows.Err())
			}
		}
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.t(id INT)", "CREATE TABLE b.t(id INT)"} {
			exec(q)
		}
		for _, tc := range []struct {
			expression, label string
			want              any
		}{
			{"pg_table_is_visible('a.t'::regclass)", "pg_table_is_visible", false},
			{"pg_type_is_visible(23)", "pg_type_is_visible", true},
			{"pg_collation_is_visible(811)", "pg_collation_is_visible", true},
			{"pg_conversion_is_visible(4402)", "pg_conversion_is_visible", true},
			{"pg_type_is_visible(0)", "pg_type_is_visible", nil},
			{"pg_collation_is_visible(NULL)", "pg_collation_is_visible", nil},
		} {
			rows, err := db.QueryContext(ctx, "SELECT "+tc.expression)
			check(rows, err, tc.label, tc.want)
		}
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var candidates map[int64]bool
		discover := func(path string) int64 {
			t.Helper()
			if _, err := conn.ExecContext(ctx, "SET search_path="+path); err != nil {
				t.Fatal(err)
			}
			rows, err := conn.QueryContext(ctx, "SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='t'")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var found int64
			seen := make(map[int64]bool)
			for rows.Next() {
				var oid int64
				var visible bool
				if err := rows.Scan(&oid, &visible); err != nil {
					t.Fatal(err)
				}
				if oid <= 0 || oid > 4294967295 {
					t.Fatal("invalid relation identity", oid)
				}
				if _, duplicate := seen[oid]; duplicate {
					t.Fatal("duplicate candidate identity", oid)
				}
				seen[oid] = visible
				if visible {
					if found != 0 {
						t.Fatal("multiple visible same-name relations")
					}
					found = oid
				}
			}
			if rows.Err() != nil || found == 0 {
				t.Fatal("missing visible relation", path, rows.Err())
			}
			if len(seen) != 2 {
				t.Fatal("expected exactly two relation candidates", seen)
			}
			if candidates != nil {
				for oid := range candidates {
					if _, exists := seen[oid]; !exists {
						t.Fatal("candidate identity bag changed", candidates, seen)
					}
				}
			}
			candidates = seen
			return found
		}
		a, b := discover("a,b"), discover("b,a")
		if a == b {
			t.Fatal("same-name relations share an identity", a)
		}
		stmt, err := conn.PrepareContext(ctx, "SELECT pg_table_is_visible($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			path string
			a, b bool
		}{{"a,b", true, false}, {"b,a", false, true}, {"public", false, false}} {
			if _, err := conn.ExecContext(ctx, "SET search_path="+tc.path); err != nil {
				t.Fatal(err)
			}
			for _, object := range []struct {
				oid  int64
				want bool
			}{{a, tc.a}, {b, tc.b}} {
				rows, err := stmt.QueryContext(ctx, object.oid)
				check(rows, err, "pg_table_is_visible", object.want)
			}
		}
		stmt.Close()
		conn.Close()
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT pg_type_is_visible()", "42883", "function pg_type_is_visible() does not exist"},
			{"SELECT pg_collation_is_visible(TRUE)", "42883", "function pg_collation_is_visible(boolean) does not exist"},
			{"SELECT public.pg_conversion_is_visible(1)", "42883", "function public.pg_conversion_is_visible(integer) does not exist"},
			{"SELECT pg_table_is_visible('bad')", "22P02", `invalid input syntax for type oid: "bad"`},
		} {
			rows, err := db.QueryContext(ctx, tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("bad call exposed rows")
			}
			want := referenceDiagnostic{Code: tc.code, Message: tc.message}
			if tc.code == "42883" {
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
			}
			if tc.code == "22P02" {
				want.Position = "28"
			}
			updateReturningReferenceDiagnostic(t, err, want)
		}
		rows, err := db.QueryContext(ctx, "SELECT pg_type_is_visible(23)")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !backend.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		check(rows, nil, "pg_type_is_visible", true)
		rows, err = db.QueryContext(ctx, "SELECT pg_table_is_visible('a.t'::regclass)")
		check(rows, err, "pg_table_is_visible", false)
		rows, err = db.QueryContext(ctx, "SELECT pg_conversion_is_visible(4531)")
		check(rows, err, "pg_conversion_is_visible", true)
	})
}
