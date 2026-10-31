package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRewriteDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE sink2(v INT)", "CREATE TABLE base(id INT)", "CREATE VIEW v AS SELECT id FROM base",
				"CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.v)",
				"CREATE RULE old_only AS ON UPDATE TO src WHERE OLD.v>0 DO NOTHING",
				"CREATE RULE new_only AS ON UPDATE TO src WHERE NEW.v>0 DO NOTHING",
				"CREATE RULE both_sides AS ON UPDATE TO src WHERE NEW.v>OLD.v DO NOTHING",
				"CREATE RULE constant_only AS ON UPDATE TO src WHERE true DO NOTHING")
			for _, relation := range []string{"src", "v", "pg_stat_progress_copy"} {
				for _, oid := range []string{"0", "4294967295::oid", "'src'::regclass"} {
					for _, pretty := range []string{"", ",true"} {
						q := fmt.Sprintf("SELECT pg_get_expr(ev_action,%s%s) FROM pg_rewrite WHERE ev_class='%s'::regclass", oid, pretty, relation)
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "input is a query, not an expression"})
							s.SelectRows = true
							s.Prepared = prepared
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						}
					}
				}
			}
			for _, name := range []string{"old_only", "new_only", "both_sides"} {
				for _, oid := range []string{"0", "4294967295::oid", "'src'::regclass"} {
					if name == "old_only" && oid != "0" {
						continue
					}
					for _, pretty := range []string{"", ",false"} {
						message := "expression contains variables"
						if oid != "0" {
							message = "expression contains variables of more than one relation"
						}
						q := "SELECT pg_get_expr(ev_qual," + oid + pretty + ") FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='" + name + "'"
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: message})
							s.SelectRows = true
							s.Prepared = prepared
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						}
					}
				}
			}
			for _, tc := range []struct {
				name, oid string
				want      any
			}{{"r", "0", ""}, {"constant_only", "0", "true"}, {"old_only", "'src'::regclass", "(v > 0)"}, {"old_only", "4294967295::oid", nil}} {
				for _, pretty := range []string{"", ",false"} {
					q := "SELECT pg_get_expr(ev_qual," + tc.oid + pretty + ") AS expression FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='" + tc.name + "'"
					rewriteRows(t, p, b, fixture.Select(q, []string{"expression"}, []string{"TEXT"}, [][]any{{tc.want}}))
				}
			}
			rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(NULL::pg_node_tree,0) AS expression", []string{"expression"}, []string{"TEXT"}, [][]any{{nil}}))
			rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_action,NULL::oid) AS expression FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", []string{"expression"}, []string{"TEXT"}, [][]any{{nil}}))
			for _, relation := range []string{"v", "pg_stat_progress_copy", "pg_stat_progress_create_index"} {
				for _, pretty := range []string{"", ",true"} {
					rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_qual,0"+pretty+") AS expression FROM pg_rewrite WHERE ev_class='"+relation+"'::regclass AND rulename='_RETURN'", []string{"expression"}, []string{"TEXT"}, [][]any{{""}}))
				}
			}
			for _, tc := range []struct{ q, code, message, detail, hint string }{
				{"CREATE RULE r AS ON INSERT TO src DO NOTHING", "42710", `rule "r" for relation "src" already exists`, "", ""},
				{"DROP RULE missing ON src", "42704", `rule "missing" for relation "src" does not exist`, "", ""},
				{"CREATE RULE bad AS ON SELECT TO base DO INSTEAD SELECT id FROM base", "42809", `relation "base" cannot have ON SELECT rules`, "This operation is not supported for tables.", ""},
				{`ALTER RULE "_RETURN" ON v RENAME TO x`, "42P17", "renaming an ON SELECT rule is not allowed", "", ""},
				{`DROP RULE "_RETURN" ON v CASCADE`, "2BP01", "cannot drop rule _RETURN on view v because view v requires it", "", "You can drop view v instead."},
				{"DROP TABLE sink2 RESTRICT", "2BP01", "cannot drop table sink2 because other objects depend on it", "rule r on table src depends on table sink2", "Use DROP ... CASCADE to drop the dependent objects too."},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass", 5)
			captured := rewriteTrees(t, p, b, "src", "old_only")
			rewriteExec(t, p, b, "ALTER TABLE src RENAME COLUMN v TO amount")
			if got := rewriteTrees(t, p, b, "src", "old_only"); got != captured {
				t.Fatal("rename changed stored raw qualification", got, captured)
			}
			rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_qual,'src'::regclass) AS expression FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='old_only'", []string{"expression"}, []string{"TEXT"}, [][]any{{"(amount > 0)"}}))
		})
	}
}
