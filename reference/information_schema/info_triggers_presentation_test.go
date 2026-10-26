package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersPresentation(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"SET standard_conforming_strings=on",
			"CREATE SCHEMA it_scope", "CREATE SCHEMA it_function", "CREATE SCHEMA it_shadow",
			`CREATE DOMAIN it_scope.flag AS boolean`,
			`CREATE TABLE it_scope."Data 名"("Value 名" integer, flag it_scope.flag)`,
			`CREATE FUNCTION it_function."Fn ""名"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'metadata must not invoke'; END$$`,
			`CREATE TRIGGER "Trig 名" BEFORE UPDATE ON it_scope."Data 名" FOR EACH ROW WHEN (OLD."Value 名">0 AND NEW."Value 名">OLD."Value 名") EXECUTE PROCEDURE it_function."Fn ""名"('O''Reilly','plain',7,'',E'a\\b')`,
			`CREATE TRIGGER "Implicit" BEFORE UPDATE ON it_scope."Data 名" FOR EACH ROW WHEN (NEW.flag) EXECUTE FUNCTION it_function."Fn ""名"('O''Reilly','plain',7,'',E'a\\b')`,
		} {
			run(fixture.Exec(q, 0))
		}
		condition := `((old."Value 名" > 0) AND (new."Value 名" > old."Value 名"))`
		statement := `EXECUTE FUNCTION it_function."Fn ""名"('O''Reilly', 'plain', '7', '', 'a\b')`
		where := "trigger_schema='it_scope'"
		// Retain one actual seventeen-column consumer across every path/name
		// and on/off/reset string-setting transition below.
		held := itHeld(t, p, c, r, itProjection(b.PG, where, nil).Query)
		check := func(action, when string) {
			// Boolean context implicitly coerces the declared Boolean domain
			// to bool. PostgreSQL's nonpretty WHEN hides that coercion:
			// action_condition is new.flag, not (new.flag)::boolean.
			// Native Boolean-context binding represents this as an Assignment
			// coercion, which its deparser hides in both display modes. This
			// checks the genuine SQL result, not a synthetic cast/flag mutation.
			s := itProjection(b.PG, where, [][]any{
				itrRow(database, "it_scope", "Data 名", "Implicit", "UPDATE", 1, "new.flag", action),
				itrRow(database, "it_scope", "Data 名", "Trig 名", "UPDATE", 2, when, action),
			})
			run(s)
			run(fixture.Prepared(s))
			held(s)
		}
		check(statement, condition)
		run(fixture.Exec("SET search_path=it_function,public", 0))
		check(`EXECUTE FUNCTION "Fn ""名"('O''Reilly', 'plain', '7', '', 'a\b')`, condition)
		run(fixture.Exec(`CREATE FUNCTION it_shadow."Fn ""名"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$`, 0))
		run(fixture.Exec("SET search_path=it_shadow,it_function,public", 0))
		check(statement, condition)
		run(fixture.Exec(`ALTER FUNCTION it_function."Fn ""名"() RENAME TO renamed_fn`, 0))
		check(`EXECUTE FUNCTION renamed_fn('O''Reilly', 'plain', '7', '', 'a\b')`, condition)
		run(fixture.Exec("ALTER SCHEMA it_function RENAME TO it_renamed", 0))
		check(`EXECUTE FUNCTION it_renamed.renamed_fn('O''Reilly', 'plain', '7', '', 'a\b')`, condition)
		run(fixture.Exec(`ALTER TABLE it_scope."Data 名" RENAME COLUMN "Value 名" TO changed`, 0))
		check(`EXECUTE FUNCTION it_renamed.renamed_fn('O''Reilly', 'plain', '7', '', 'a\b')`, `((old.changed > 0) AND (new.changed > old.changed))`)
		run(fixture.Exec("SET standard_conforming_strings=off", 0))
		check(`EXECUTE FUNCTION it_renamed.renamed_fn('O''Reilly', 'plain', '7', '', 'a\\b')`, `((old.changed > 0) AND (new.changed > old.changed))`)
		run(fixture.Exec("RESET standard_conforming_strings", 0))
		check(`EXECUTE FUNCTION it_renamed.renamed_fn('O''Reilly', 'plain', '7', '', 'a\b')`, `((old.changed > 0) AND (new.changed > old.changed))`)
		run(fixture.Exec("RESET search_path", 0))
	})
}
