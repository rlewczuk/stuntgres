package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTriggerDefinitions(t *testing.T) {
	t.Run("NoEvaluation", pgTriggerNoEvaluation)
	cases := []struct {
		name, clause, args, when, attrs, hex string
		bits, nargs                          int64
		qual                                 bool
	}{
		{"insert", "BEFORE INSERT", "", "", "", "", 7, 0, false},
		{"update", "BEFORE UPDATE", "", "", "", "", 19, 0, false},
		{"delete", "AFTER DELETE", "", "", "", "", 9, 0, false},
		{"statement", "AFTER UPDATE", "", "", "", "", 16, 0, false},
		{"truncate", "AFTER TRUNCATE", "", "", "", "", 32, 0, false},
		{"multi", "BEFORE INSERT OR UPDATE OR DELETE", "", "", "", "", 31, 0, false},
		{"attributes", "BEFORE UPDATE OF c,a", "'x','','é'", "", "3 1", "780000c3a900", 19, 3, false},
		{"constant", "BEFORE UPDATE", "", "true", "", "", 19, 0, true},
		{"null", "BEFORE UPDATE", "", "NULL::boolean", "", "", 19, 0, true},
		{"old", "BEFORE UPDATE", "", "OLD.a > 0", "", "", 19, 0, true},
		{"new", "BEFORE UPDATE", "", "NEW.a > 0", "", "", 19, 0, true},
		{"mixed", "BEFORE UPDATE", "", "OLD.a < NEW.a", "", "", 19, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					pgTriggerExec(t, p, b, b.DB, "CREATE TABLE pgt_definition(a integer,b integer,c integer)")
					pgTriggerExec(t, p, b, b.DB, "CREATE FUNCTION pgt_definition_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$")
					level, when := "ROW", ""
					if tc.name == "statement" || tc.name == "truncate" {
						level = "STATEMENT"
					}
					if tc.when != "" {
						when = " WHEN (" + tc.when + ")"
					}
					pgTriggerExec(t, p, b, b.DB, fmt.Sprintf("CREATE TRIGGER pgt_definition_trigger %s ON pgt_definition FOR EACH %s%s EXECUTE FUNCTION pgt_definition_fn(%s)", tc.clause, level, when, tc.args))
					query := "SELECT t.tgname,t.tgtype,t.tgenabled,t.tgisinternal,t.tgparentid,t.tgconstrrelid,t.tgconstrindid,t.tgconstraint,t.tgdeferrable,t.tginitdeferred,t.tgnargs,t.tgattr::text AS attrs,encode(t.tgargs,'hex') AS args,t.tgqual IS NOT NULL AS has_qual,t.tgoldtable,t.tgnewtable,c.relname,p.proname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=$1::regclass AND t.tgname=$2::name"
					columns := []string{"tgname", "tgtype", "tgenabled", "tgisinternal", "tgparentid", "tgconstrrelid", "tgconstrindid", "tgconstraint", "tgdeferrable", "tginitdeferred", "tgnargs", "attrs", "args", "has_qual", "tgoldtable", "tgnewtable", "relname", "proname"}
					types := []string{"NAME", "INT2", "CHAR", "BOOL", "OID", "OID", "OID", "OID", "BOOL", "BOOL", "INT2", "TEXT", "TEXT", "BOOL", "NAME", "NAME", "NAME", "NAME"}
					row := []any{"pgt_definition_trigger", tc.bits, "O", false, int64(0), int64(0), int64(0), int64(0), false, false, tc.nargs, tc.attrs, tc.hex, tc.qual, nil, nil, "pgt_definition", "pgt_definition_fn"}
					s := fixture.Select(query, columns, types, [][]any{row}, "pgt_definition", "pgt_definition_trigger")
					pgTriggerBoth(t, p, b, s)
					stmt, err := b.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
					fixture.PgDescriptionExecutorRows(t, p, b, held, s)
					missing := s
					missing.Args = []any{"pgt_definition", "absent"}
					missing.Rows = [][]any{}
					fixture.PgDescriptionExecutorRows(t, p, b, held, missing)
					fixture.PgDescriptionExecutorRows(t, p, b, held, s)
				})
			}
		})
	}
}
