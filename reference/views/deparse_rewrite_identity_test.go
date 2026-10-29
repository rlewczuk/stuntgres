package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteIdentityPretty(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rewrite_names")
	fixture.SchemaReferenceCleanup(t, "Rewrite Space")
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		rewriteSeed(t, p, b, c)
		triggerID, ruleID := rewriteOID(t, p, c, true), rewriteOID(t, p, c, false)
		for _, tc := range []struct {
			f                     string
			id                    int64
			defaultDef, prettyDef string
		}{{"pg_get_triggerdef", triggerID, rewriteTriggerDefault, rewriteTriggerPretty}, {"pg_get_ruledef", ruleID, rewriteRuleDefault, rewriteRulePretty}} {
			q := "SELECT " + tc.f + "($1::oid,$2::boolean) AS definition"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, pretty := range []bool{false, true} {
				want := tc.defaultDef
				if pretty {
					want = tc.prettyDef
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, want, tc.id, pretty))
			}
			// Each held statement crosses all identity and visibility changes.
			// Use a transaction savepoint so the next function starts with the same seed.
			for _, ddl := range []string{"BEGIN", "CREATE SCHEMA rewrite_names", "ALTER TABLE t SET SCHEMA rewrite_names", "ALTER TABLE rewrite_names.t RENAME TO \"Trigger Table\"", "ALTER TABLE rewrite_names.\"Trigger Table\" RENAME COLUMN v TO \"Value\"", "ALTER FUNCTION tf() SET SCHEMA rewrite_names", "ALTER FUNCTION rewrite_names.tf() RENAME TO \"Original Function\"", "CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := 999; RETURN NEW; END$$", "ALTER TABLE src SET SCHEMA rewrite_names", "ALTER TABLE rewrite_names.src RENAME TO \"Rule Source\"", "ALTER TABLE rewrite_names.\"Rule Source\" RENAME COLUMN v TO \"Value\"", "ALTER TABLE dst SET SCHEMA rewrite_names", "ALTER TABLE rewrite_names.dst RENAME TO \"Rule Destination\"", "ALTER TABLE rewrite_names.\"Rule Destination\" RENAME COLUMN v TO \"Value\"", "ALTER SCHEMA rewrite_names RENAME TO \"Rewrite Space\""} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
			}
			triggerDefault := `CREATE TRIGGER tr BEFORE INSERT ON "Rewrite Space"."Trigger Table" FOR EACH ROW WHEN ((new."Value" > 0)) EXECUTE FUNCTION "Rewrite Space"."Original Function"('O''Reilly', '')`
			triggerPretty := `CREATE TRIGGER tr BEFORE INSERT ON "Rewrite Space"."Trigger Table" FOR EACH ROW WHEN (new."Value" > 0) EXECUTE FUNCTION "Rewrite Space"."Original Function"('O''Reilly', '')`
			ruleDefault := "CREATE RULE r AS\n    ON INSERT TO \"Rewrite Space\".\"Rule Source\" DO INSTEAD  INSERT INTO \"Rewrite Space\".\"Rule Destination\" (\"Value\")\n  VALUES (new.\"Value\");"
			check := func(defaultDef, prettyDef string) {
				for _, pretty := range []bool{false, true} {
					want := defaultDef
					if pretty {
						want = prettyDef
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, want, tc.id, pretty))
				}
			}
			if tc.f == "pg_get_triggerdef" {
				check(triggerDefault, triggerPretty)
			} else {
				check(ruleDefault, ruleDefault)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET LOCAL search_path TO "Rewrite Space",public`, 0))
			triggerDefault = `CREATE TRIGGER tr BEFORE INSERT ON "Rewrite Space"."Trigger Table" FOR EACH ROW WHEN ((new."Value" > 0)) EXECUTE FUNCTION "Original Function"('O''Reilly', '')`
			triggerPretty = `CREATE TRIGGER tr BEFORE INSERT ON "Trigger Table" FOR EACH ROW WHEN (new."Value" > 0) EXECUTE FUNCTION "Original Function"('O''Reilly', '')`
			ruleDefault = "CREATE RULE r AS\n    ON INSERT TO \"Rewrite Space\".\"Rule Source\" DO INSTEAD  INSERT INTO \"Rule Destination\" (\"Value\")\n  VALUES (new.\"Value\");"
			rulePretty := "CREATE RULE r AS\n    ON INSERT TO \"Rule Source\" DO INSTEAD  INSERT INTO \"Rule Destination\" (\"Value\")\n  VALUES (new.\"Value\");"
			if tc.f == "pg_get_triggerdef" {
				check(triggerDefault, triggerPretty)
			} else {
				check(ruleDefault, rulePretty)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET LOCAL quote_all_identifiers TO on", 0))
			if tc.f == "pg_get_triggerdef" {
				check(`CREATE TRIGGER "tr" BEFORE INSERT ON "Rewrite Space"."Trigger Table" FOR EACH ROW WHEN (("new"."Value" > 0)) EXECUTE FUNCTION "Original Function"('O''Reilly', '')`, `CREATE TRIGGER "tr" BEFORE INSERT ON "Trigger Table" FOR EACH ROW WHEN ("new"."Value" > 0) EXECUTE FUNCTION "Original Function"('O''Reilly', '')`)
			} else {
				check("CREATE RULE \"r\" AS\n    ON INSERT TO \"Rewrite Space\".\"Rule Source\" DO INSTEAD  INSERT INTO \"Rule Destination\" (\"Value\")\n  VALUES (\"new\".\"Value\");", "CREATE RULE \"r\" AS\n    ON INSERT TO \"Rule Source\" DO INSTEAD  INSERT INTO \"Rule Destination\" (\"Value\")\n  VALUES (\"new\".\"Value\");")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET LOCAL quote_all_identifiers TO off", 0))
			// PL/pgSQL source does not follow column renames. Repair the original
			// routine in place so replay tests identity, not a stale body reference.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE OR REPLACE FUNCTION "Rewrite Space"."Original Function"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW."Value" := NEW."Value" * 2; RETURN NEW; END$$`, 0))
			// Returned SQL, not a canned reconstruction, must still refer to the
			// renamed original, not the old-name decoy. Replay both formats.
			for _, pretty := range []bool{false, true} {
				catalog, predicate, drop := "pg_rewrite", "ev_class='\"Rule Source\"'::regclass AND rulename='r'", `DROP RULE r ON "Rule Source"`
				if tc.f == "pg_get_triggerdef" {
					catalog, predicate, drop = "pg_trigger", "tgrelid='\"Trigger Table\"'::regclass AND tgname='tr'", `DROP TRIGGER tr ON "Trigger Table"`
				}
				definition := fixture.DeparseRelationsRead(t, p, b, c, fmt.Sprintf("SELECT %s(oid,%v) AS definition FROM %s WHERE %s", tc.f, pretty, catalog, predicate))
				for _, ddl := range []string{drop, definition} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO "Trigger Table" VALUES(5)`, 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO "Rule Source" VALUES(7)`, 1))
			for _, s := range []fixture.Step{
				fixture.Select(`SELECT "Value" FROM "Trigger Table"`, []string{"Value"}, []string{"INT4"}, [][]any{{int64(10)}}),
				fixture.Select(`SELECT "Value" FROM "Rule Source"`, []string{"Value"}, []string{"INT4"}, nil),
				fixture.Select(`SELECT "Value" FROM "Rule Destination"`, []string{"Value"}, []string{"INT4"}, [][]any{{int64(7)}}),
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
		}
	})
}

func TestReferenceDeparseRewriteUnevaluated(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TABLE t(v INT)", "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE TABLE counter(v INT)",
			"CREATE FUNCTION counted(v INT) RETURNS INT LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO counter VALUES(v); RETURN v; END$$",
			"CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW WHEN(counted(NEW.v)>0) EXECUTE FUNCTION tf()",
			"CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(counted(NEW.v))",
			"ALTER TABLE t DISABLE TRIGGER tr", "ALTER TABLE src DISABLE RULE r",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for range 2 {
			for _, suffix := range []string{"", ",false", ",true"} {
				target, predicate := "public.t", "((counted(new.v) > 0))"
				if suffix == ",true" {
					target, predicate = "t", "(counted(new.v) > 0)"
				}
				rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid"+suffix+") AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", "CREATE TRIGGER tr BEFORE INSERT ON "+target+" FOR EACH ROW WHEN "+predicate+" EXECUTE FUNCTION tf()")
				target = "public.src"
				if suffix == ",true" {
					target = "src"
				}
				rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid"+suffix+") AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", "CREATE RULE r AS\n    ON INSERT TO "+target+" DO INSTEAD  INSERT INTO dst (v)\n  VALUES (counted(new.v));")
			}
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT count(*) FROM counter", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
			fixture.Select("SELECT tgenabled FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", []string{"tgenabled"}, []string{"CHAR"}, [][]any{{"D"}}),
			fixture.Select("SELECT ev_enabled FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", []string{"ev_enabled"}, []string{"CHAR"}, [][]any{{"D"}}),
			fixture.Exec("ALTER TABLE t ENABLE TRIGGER tr", 0), fixture.Exec("ALTER TABLE src ENABLE RULE r", 0),
			fixture.Exec("INSERT INTO t VALUES(5)", 1), fixture.Exec("INSERT INTO src VALUES(7)", 1),
			fixture.Select("SELECT v FROM counter ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(7)}}),
			fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}),
			fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, nil),
			fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
