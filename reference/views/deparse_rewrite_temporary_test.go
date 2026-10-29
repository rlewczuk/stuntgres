package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Own TEMP aliases are presentation of captured identities, not permission to
// create a namespace or to apply information_schema's TEMP disclosure filter.
// Independent expectations follow REL_18_6 ruleutils.c generate_relation_name:
// visibility precedes get_namespace_name_or_temp and identifier quoting.
// lsyscache.c maps only this backend's TEMP namespace to pg_temp.
func TestReferenceDeparseRewriteTemporaryNames(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TEMP TABLE t(v INT)", "CREATE TEMP TABLE src(v INT)", "CREATE TEMP TABLE dst(v INT)",
			"CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION tf()",
			"CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v)",
			"CREATE TEMP VIEW v AS SELECT v AS label FROM t",
			"CREATE TABLE public.t(v INT)", "CREATE TABLE public.src(v INT)", "CREATE TABLE public.dst(v INT)",
			"CREATE VIEW public.v AS SELECT 1 AS label",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			function, lookup, target, prefix, body, hiddenBody string
		}{
			{"pg_get_triggerdef", "SELECT oid::bigint FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", "t", "CREATE TRIGGER tr BEFORE INSERT ON ", " FOR EACH ROW EXECUTE FUNCTION tf()", " FOR EACH ROW EXECUTE FUNCTION tf()"},
			{"pg_get_ruledef", "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", "src", "CREATE RULE r AS\n    ON INSERT TO ", " DO INSTEAD  INSERT INTO dst (v)\n  VALUES (new.v);", " DO INSTEAD  INSERT INTO pg_temp.dst (v)\n  VALUES (new.v);"},
			{"pg_get_ruledef", "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='v'::regclass AND rulename='_RETURN'", "v", "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO ", " DO INSTEAD  SELECT v AS label\n   FROM t;", " DO INSTEAD  SELECT v AS label\n   FROM pg_temp.t;"},
		} {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, tc.lookup).Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("TEMP registered identity", oid, err)
			}
			for _, suffix := range []string{"", ",false", ",true"} {
				target := "pg_temp." + tc.target
				if suffix == ",true" {
					target = tc.target
				}
				rewriteText(t, p, b, c, "SELECT "+tc.function+"($1::oid"+suffix+") AS definition", tc.prefix+target+tc.body, oid)
			}
			var held []*sql.Stmt
			for _, suffix := range []string{"", ",false", ",true"} {
				statement, err := c.PrepareContext(p.Ctx, "SELECT "+tc.function+"($1::oid"+suffix+") AS definition")
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, statement)
			}
			checkHeld := func(hidden bool, want string) {
				t.Helper()
				for i, statement := range held {
					expected := want
					if !hidden {
						target := "pg_temp." + tc.target
						if i == 2 {
							target = tc.target
						}
						expected = tc.prefix + target + tc.body
					}
					var got string
					if err := statement.QueryRowContext(p.Ctx, oid).Scan(&got); err != nil || got != expected {
						t.Fatal("held TEMP definition retargeted", got, expected, err)
					}
				}
			}
			checkHeld(false, "")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO public,pg_temp", 0))
			want := tc.prefix + "pg_temp." + tc.target + tc.hiddenBody
			for _, suffix := range []string{"", ",false", ",true"} {
				rewriteText(t, p, b, c, "SELECT "+tc.function+"($1::oid"+suffix+") AS definition", want, oid)
			}
			checkHeld(true, want)
			if tc.target == "v" {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers TO on", 0))
				quoted := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO \"pg_temp\".\"v\" DO INSTEAD  SELECT \"v\" AS \"label\"\n   FROM \"pg_temp\".\"t\";"
				for _, suffix := range []string{"", ",false", ",true"} {
					rewriteText(t, p, b, c, "SELECT pg_get_ruledef($1::oid"+suffix+") AS definition", quoted, oid)
				}
				checkHeld(true, quoted)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET quote_all_identifiers", 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET search_path", 0))
			checkHeld(false, "")
			for _, statement := range held {
				if err := statement.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
