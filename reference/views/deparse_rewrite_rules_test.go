package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteRuleDefinition(t *testing.T) {
	for _, pretty := range []bool{false, true} {
		t.Run(fmt.Sprint(pretty), func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				rewriteSeed(t, p, b, c)
				q := fmt.Sprintf("SELECT pg_get_ruledef(oid,%v) AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", pretty)
				want := rewriteRuleDefault
				if pretty {
					want = rewriteRulePretty
				}
				rewriteText(t, p, b, c, q, want)
				definition := fixture.DeparseRelationsRead(t, p, b, c, q)
				for _, s := range []fixture.Step{
					fixture.Exec("DROP RULE r ON src", 0), fixture.Exec(definition, 0), fixture.Exec("INSERT INTO src VALUES(7)", 1),
					fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, nil),
					fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}),
					fixture.Select("SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
	// Independent default goldens follow the existing pg_rules family, not
	// pg_get_ruledef/pg_rules parity. Each definition is read by actual rule OID.
	for _, tc := range []struct{ name, ddl, tail, prettyTail string }{
		{"nothing", "ON INSERT TO src DO ALSO NOTHING", " DO NOTHING;", " DO NOTHING;"},
		{"instead_nothing", "ON DELETE TO src DO INSTEAD NOTHING", " DO INSTEAD NOTHING;", " DO INSTEAD NOTHING;"},
		{"condition", "ON UPDATE TO src WHERE NEW.v>OLD.v DO INSTEAD NOTHING", "\n   WHERE (new.v > old.v) DO INSTEAD NOTHING;", "\n   WHERE new.v > old.v DO INSTEAD NOTHING;"},
		{"update", "ON UPDATE TO src DO UPDATE dst SET v=NEW.v WHERE v=OLD.v", " DO  UPDATE dst SET v = new.v\n  WHERE (dst.v = old.v);", " DO  UPDATE dst SET v = new.v\n  WHERE dst.v = old.v;"},
		{"delete", "ON DELETE TO src DO DELETE FROM dst WHERE v=OLD.v", " DO  DELETE FROM dst\n  WHERE (dst.v = old.v);", " DO  DELETE FROM dst\n  WHERE dst.v = old.v;"},
		{"select", "ON INSERT TO src DO SELECT NEW.v AS copied", " DO  SELECT new.v AS copied;", " DO  SELECT new.v AS copied;"},
		{"ordered", "ON INSERT TO src DO (INSERT INTO dst VALUES(NEW.v); SELECT NEW.v)", " DO ( INSERT INTO dst (v)\n  VALUES (new.v);\n SELECT new.v;\n);", " DO ( INSERT INTO dst (v)\n  VALUES (new.v);\n SELECT new.v;\n);"},
		{"returning", "ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v) RETURNING v", " DO INSTEAD  INSERT INTO dst (v)\n  VALUES (new.v)\n  RETURNING dst.v;", " DO INSTEAD  INSERT INTO dst (v)\n  VALUES (new.v)\n  RETURNING dst.v;"},
		{"typed_insert", "ON INSERT TO src DO INSERT INTO typed_log VALUES(NEW.v,'text',1.25,NULL)", " DO  INSERT INTO typed_log (id, label, amount, optional)\n  VALUES (new.v, 'text'::text, 1.25, NULL::integer);", " DO  INSERT INTO typed_log (id, label, amount, optional)\n  VALUES (new.v, 'text'::text, 1.25, NULL::integer);"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE TABLE typed_log(id INT,label TEXT,amount NUMERIC,optional INT)", "CREATE RULE r AS " + tc.ddl} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				event := strings.Fields(tc.ddl)[1]
				for _, suffix := range []string{"", ",false", ",true"} {
					target, tail := "public.src", tc.tail
					if suffix == ",true" {
						target, tail = "src", tc.prettyTail
					}
					rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid"+suffix+") AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", "CREATE RULE r AS\n    ON "+event+" TO "+target+tail)
				}
				for _, table := range []string{"src", "dst", "typed_log"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		})
	}
}

func TestReferenceDeparseRewriteReturnRules(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE base(id INT,v INT)", "CREATE VIEW v(k,label) AS SELECT id,v FROM base WHERE v>0", "CREATE MATERIALIZED VIEW mv(k,label) AS SELECT id,v FROM base WHERE v>0"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO base VALUES(1,5)", 1))
		check := func(source, column string) {
			for _, target := range []string{"v", "mv"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT rulename,ev_type,is_instead FROM pg_rewrite WHERE ev_class=$1::regclass", []string{"rulename", "ev_type", "is_instead"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", true}}, target))
				for _, suffix := range []string{"", ",false", ",true"} {
					rel, predicate := "public."+target, "("+column+" > 0)"
					if suffix == ",true" {
						rel, predicate = target, column+" > 0"
					}
					want := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO " + rel + " DO INSTEAD  SELECT id AS k,\n    " + column + " AS label\n   FROM " + source + "\n  WHERE " + predicate + ";"
					rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid"+suffix+") AS definition FROM pg_rewrite WHERE ev_class=$1::regclass AND rulename='_RETURN'", want, target)
				}
			}
		}
		check("base", "v")
		for _, q := range []string{"ALTER TABLE base RENAME TO renamed_base", "ALTER TABLE renamed_base RENAME COLUMN v TO amount"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		check("renamed_base", "amount")
		// Replace the real view's rule using the actual returned definition;
		// preserve declared output names, not original source spelling.
		for _, pretty := range []bool{false, true} {
			q := fmt.Sprintf("SELECT pg_get_ruledef(oid,%v) AS definition FROM pg_rewrite WHERE ev_class='v'::regclass AND rulename='_RETURN'", pretty)
			definition := fixture.DeparseRelationsRead(t, p, b, c, q)
			definition = strings.Replace(definition, "CREATE RULE ", "CREATE OR REPLACE RULE ", 1)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(definition, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT k,label FROM v", []string{"k", "label"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(5)}}))
		}
		// _RETURN belongs to pg_rewrite even though pg_rules deliberately hides it.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT rulename FROM pg_rules WHERE schemaname='public' AND tablename IN ('v','mv')", []string{"rulename"}, []string{"NAME"}, nil))
	})
}
