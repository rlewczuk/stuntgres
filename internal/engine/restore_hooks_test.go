package engine

import (
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/parser"
)

// Only the generated foreign metadata repair shape reaches normal binding.
// Bound eligibility (including rejection of scalar 7) is exercised by the
// public default-persistence definition, not guessed by this raw AST gate.
func TestRestoreAdmitsForeignDefaultRepair(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		fail bool
	}{
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 's.later'::regclass"},
		{sql: `ALTER FOREIGN TABLE "odd""schema"."odd""table" ALTER COLUMN "odd""column" SET DEFAULT 's.later'::regclass`},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 7"},
		{sql: "ALTER FOREIGN TABLE t ALTER COLUMN c SET DEFAULT 's.later'::regclass", fail: true},
		{sql: "ALTER FOREIGN TABLE IF EXISTS s.t ALTER COLUMN c SET DEFAULT 's.later'::regclass", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c DROP DEFAULT", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT 7, ALTER COLUMN d SET DEFAULT 8", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET NOT NULL", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t OPTIONS(tag 'not-a-default')", fail: true},
		{sql: "ALTER FOREIGN TABLE s.t ALTER COLUMN c SET DEFAULT $1", fail: true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			admission, err := restoreAdmitForeignDDL(new(restoreRun), nil, statements[0])
			if tc.fail {
				if err == nil {
					t.Fatal("unsupported foreign replay shape admitted", admission)
				}
			} else if err != nil || admission != control.Admitted {
				t.Fatal("generated foreign default repair rejected", admission, err)
			}
		})
	}
}

// Admission must hand emitted installation SQL to ordinary stage execution,
// rather than consume it as an import repair or reject it before resolution.
func TestRestoreAdmitsExtensionReplay(t *testing.T) {
	for _, sql := range []string{
		`ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()`,
		`ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`,
		`ALTER FUNCTION pg_catalog.plpgsql_call_handler() OWNER TO postgres`,
		`ALTER EXTENSION plpgsql ADD FUNCTION pg_catalog.plpgsql_call_handler()`,
		`ALTER EXTENSION plpgsql ADD LANGUAGE plpgsql`,
		`ALTER EXTENSION cube SET SCHEMA restored`,
		`ALTER EXTENSION earthdistance UPDATE TO '1.2'`,
		`DROP EXTENSION plpgsql`,
		`DROP EXTENSION unaccent CASCADE`,
		`DROP FUNCTION restored.removed(integer) CASCADE`,
		`DROP TYPE restored.removed CASCADE`,
		`DROP DOMAIN restored.blocker`,
		`ALTER DOMAIN restored.member RENAME TO retained`,
		`ALTER DOMAIN restored.member SET SCHEMA working`,
		`ALTER TABLE restored.member RENAME TO retained`,
		`ALTER TABLE restored.member SET SCHEMA working`,
		`ALTER VIEW restored.member RENAME TO retained`,
		`ALTER MATERIALIZED VIEW restored.member SET SCHEMA working`,
		`ALTER FOREIGN TABLE restored.member SET SCHEMA working`,
	} {
		t.Run(sql, func(t *testing.T) {
			statements, err := parser.Parse(sql)
			if err != nil {
				t.Fatal(err)
			}
			answer := control.NotHandled
			for _, hook := range restoreHooks {
				if hook.Admit == nil {
					continue
				}
				answer, err = hook.Admit(new(restoreRun), nil, statements[0])
				if err != nil {
					t.Fatal(err)
				}
				if answer != control.NotHandled {
					break
				}
			}
			if answer != control.Admitted {
				t.Fatal("emitted SQL did not reach ordinary execution", answer)
			}
		})
	}
}
