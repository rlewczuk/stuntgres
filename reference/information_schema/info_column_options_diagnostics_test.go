package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func copWriteDiagnostic(command string) fixture.Diagnostic {
	verb, action := "insert into", "inserting into"
	if command == "UPDATE" {
		verb, action = "update", "updating"
	}
	if command == "DELETE" {
		verb, action = "delete from", "deleting from"
	}
	d := fixture.ControlsReferenceDiagnostic("55000", "cannot "+verb+" view \"column_options\"")
	d.Detail = "Views that return set-returning functions are not automatically updatable."
	d.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + command + " trigger or an unconditional ON " + command + " DO INSTEAD rule."
	return d
}

func TestReferenceInfoColumnOptionsDiagnostics(t *testing.T) {
	cases := []struct {
		name, q      string
		d            fixture.Diagnostic
		query, parse bool
	}{
		{"projection", "SELECT absent FROM information_schema.column_options", fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`), true, true},
		{"unicode_projection", "SELECT /*é*/ absent FROM information_schema.column_options", fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`), true, true},
		{"insert", "INSERT INTO information_schema.column_options VALUES('d','s','t','c','o','v')", copWriteDiagnostic("INSERT"), false, true},
		{"update", "UPDATE information_schema.column_options SET option_value='x' WHERE false", copWriteDiagnostic("UPDATE"), false, true},
		{"delete", "DELETE FROM information_schema.column_options WHERE false", copWriteDiagnostic("DELETE"), false, true},
		{"missing_option", "ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(SET remote 'wrong',DROP absent)", fixture.ControlsReferenceDiagnostic("42704", `option "absent" not found`), false, false},
		{"duplicate_option", "ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(ADD remote 'wrong')", fixture.ControlsReferenceDiagnostic("42710", `option "remote" provided more than once`), false, false},
		{"null_option", "ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN label OPTIONS(SET remote NULL)", fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "NULL"`), false, true},
		{"unauthorized", "ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(SET remote 'wrong')", fixture.ControlsReferenceDiagnostic("42501", "must be owner of foreign table options"), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copScenario(t, func(c *copCase) {
				c.seed()
				want := c.seedRows()
				const identityQuery = "SELECT oid::bigint FROM pg_catalog.pg_class WHERE oid='cop_scope.options'::regclass"
				var identity int64
				if err := c.conn.QueryRowContext(c.p.Ctx, identityQuery).Scan(&identity); err != nil {
					t.Fatal(err)
				}
				if tc.name == "projection" {
					tc.d.Position = fixture.Position(8)
				}
				if tc.name == "unicode_projection" {
					tc.d.Position = fixture.Position(14)
				}
				if tc.name == "null_option" {
					tc.d.Position = fixture.Position(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, "NULL")]) + 1)
				}
				if tc.name == "unauthorized" {
					c.exec("CREATE ROLE cop_denied", "GRANT USAGE ON SCHEMA cop_scope TO cop_denied", "SET SESSION AUTHORIZATION cop_denied")
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, tc.d)
					s.SelectRows = tc.query
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if tc.parse {
							s.ErrorStage = "prepare"
						}
					}
					c.run(c.conn, s)
					if tc.name == "unauthorized" {
						c.exec("RESET SESSION AUTHORIZATION")
					}
					c.routes(copQuery, want, "cop_scope")
					var after int64
					if err := c.conn.QueryRowContext(c.p.Ctx, identityQuery).Scan(&after); err != nil {
						t.Fatal(err)
					}
					if after != identity {
						t.Fatalf("failed statement replaced foreign table: %d -> %d", identity, after)
					}
					if tc.name == "unauthorized" && !prepared {
						c.exec("SET SESSION AUTHORIZATION cop_denied")
					}
				}
				if !c.b.PG {
					ctx, cancel := context.WithCancel(c.p.Ctx)
					cancel()
					r, err := c.conn.QueryContext(ctx, copQuery, "cop_scope")
					if r != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("pre-canceled rows=%v error=%v", r, err)
					}
					c.check(c.conn, "cop_scope", want)
				}
			})
		})
	}
}
