package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreSettingsAndMetadataSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			ctx := p.Ctx
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
		})
	}
}

func TestReferenceStringCoreVoidSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			c, err := backend.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			random := func() any {
				var value any
				if err := c.QueryRowContext(ctx, "SELECT random()").Scan(&value); err != nil {
					t.Fatal(err)
				}
				if _, ok := value.(float64); !ok {
					t.Fatalf("raw random %T", value)
				}
				return value
			}
			seed := func(input float64) {
				var value any
				if err := c.QueryRowContext(ctx, "SELECT setseed($1)", input).Scan(&value); err != nil {
					t.Fatal(err)
				}
				if backend.PG {
					bytes, ok := value.([]byte)
					if !ok || bytes == nil || len(bytes) != 0 {
						t.Fatalf("raw PG VOID %#v", value)
					}
				} else if value != "" {
					t.Fatalf("raw native VOID %#v", value)
				}
			}
			seed(0)
			first := random()
			for _, q := range []string{"SELECT 'x'::text||setseed(0)", "SELECT setseed(0)||'x'::text"} {
				for _, prepared := range []bool{false, true} {
					random()
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
					if next := random(); next != first {
						t.Fatal("volatile VOID child was lost", next, first)
					}
				}
			}
			// A volatile seed argument detects duplicated evaluation, not just dropped calls.
			seed(0)
			seedArgument := random().(float64)
			seed(seedArgument)
			wantNext := random()
			for _, q := range []string{"SELECT 'x'::text||setseed(random())", "SELECT setseed(random())||'x'::text"} {
				for _, prepared := range []bool{false, true} {
					seed(0)
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
					if next := random(); next != wantNext {
						t.Fatal("volatile child was not evaluated exactly once", next, wantNext)
					}
				}
			}
		})
	}
}
