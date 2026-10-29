package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const rewriteTriggerDefault = "CREATE TRIGGER tr BEFORE INSERT ON public.t FOR EACH ROW WHEN ((new.v > 0)) EXECUTE FUNCTION tf('O''Reilly', '')"
const rewriteTriggerPretty = "CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW WHEN (new.v > 0) EXECUTE FUNCTION tf('O''Reilly', '')"
const rewriteRuleDefault = "CREATE RULE r AS\n    ON INSERT TO public.src DO INSTEAD  INSERT INTO dst (v)\n  VALUES (new.v);"
const rewriteRulePretty = "CREATE RULE r AS\n    ON INSERT TO src DO INSTEAD  INSERT INTO dst (v)\n  VALUES (new.v);"

func rewriteSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE t(v INT)", "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)",
		"CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v * 2; RETURN NEW; END$$",
		"CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tf('O''Reilly','')",
		"CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func rewriteOID(t *testing.T, p *fixture.Pair, c *sql.Conn, trigger bool) int64 {
	t.Helper()
	q := "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'"
	if trigger {
		q = "SELECT oid::bigint FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'"
	}
	var id int64
	if err := c.QueryRowContext(p.Ctx, q).Scan(&id); err != nil || id <= 0 {
		t.Fatal("registered identity", id, err)
	}
	return id
}

func rewriteText(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want any, args ...any) {
	t.Helper()
	s := fixture.DeparseRelationsTextStep(t, b, q, want, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func TestReferenceDeparseRewriteSignaturesMetadata(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		rewriteSeed(t, p, b, c)
		for _, tc := range []struct {
			f, catalog, predicate, definition, pretty string
			trigger                                   bool
		}{
			{"pg_get_triggerdef", "pg_trigger", "tgrelid='t'::regclass AND tgname='tr'", rewriteTriggerDefault, rewriteTriggerPretty, true},
			{"pg_get_ruledef", "pg_rewrite", "ev_class='src'::regclass AND rulename='r'", rewriteRuleDefault, rewriteRulePretty, false},
		} {
			id := rewriteOID(t, p, c, tc.trigger)
			for _, prefix := range []string{"", "pg_catalog."} {
				for _, suffix := range []string{"", ",false", ",true"} {
					want := tc.definition
					if suffix == ",true" {
						want = tc.pretty
					}
					q := "SELECT " + prefix + tc.f + "(oid" + suffix + ") AS definition FROM " + tc.catalog + " WHERE " + tc.predicate
					rewriteText(t, p, b, c, q, want)
					q = fmt.Sprintf("SELECT %s%s(%d::oid%s) AS definition", prefix, tc.f, id, suffix)
					rewriteText(t, p, b, c, q, want)
				}
			}
			for _, tc2 := range []struct {
				call string
				args []any
				want string
			}{
				{tc.f + "($1)", []any{id}, tc.definition},
				{tc.f + "($1,$2)", []any{id, false}, tc.definition},
				{tc.f + "($1,$2)", []any{id, true}, tc.pretty},
			} {
				rewriteText(t, p, b, c, "SELECT "+tc2.call+" AS definition", tc2.want, tc2.args...)
			}
			empty := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.f+"(oid) AS definition FROM "+tc.catalog+" WHERE false", nil)
			empty.Rows = nil
			fixture.RunStep(t, p.Ctx, c, b.Recorder, empty)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(empty))
		}
		// The fixed builtin inventory, not generated user-object OIDs.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT oid::bigint,proname,pronargs,prorettype::bigint,proisstrict,provolatile FROM pg_proc WHERE oid IN (1573,1662,2504,2730) ORDER BY oid", []string{"oid", "proname", "pronargs", "prorettype", "proisstrict", "provolatile"}, []string{"INT8", "NAME", "INT2", "INT8", "BOOL", "CHAR"}, [][]any{
			{int64(1573), "pg_get_ruledef", int64(1), int64(25), true, "s"},
			{int64(1662), "pg_get_triggerdef", int64(1), int64(25), true, "s"},
			{int64(2504), "pg_get_ruledef", int64(2), int64(25), true, "s"},
			{int64(2730), "pg_get_triggerdef", int64(2), int64(25), true, "s"},
		}))
	})
}

func TestReferenceDeparseRewriteNullsAndMissing(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		rewriteSeed(t, p, b, c)
		for _, trigger := range []bool{true, false} {
			f, drop, create := "pg_get_ruledef", "DROP RULE r ON src", "CREATE RULE r AS ON INSERT TO src DO NOTHING"
			if trigger {
				f, drop, create = "pg_get_triggerdef", "DROP TRIGGER tr ON t", "CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION tf()"
			}
			id := rewriteOID(t, p, c, trigger)
			for _, args := range []string{"0::oid", "4294967295::oid", "NULL", "NULL::oid", "NULL,true", "NULL,false", "NULL,NULL", "NULL::oid,NULL::boolean", fmt.Sprintf("%d::oid,NULL", id), fmt.Sprintf("%d::oid,NULL::boolean", id)} {
				rewriteText(t, p, b, c, "SELECT "+f+"("+args+") AS definition", nil)
			}
			rewriteText(t, p, b, c, "SELECT "+f+"(oid) AS definition FROM pg_class WHERE oid='t'::regclass", nil)
			rewriteText(t, p, b, c, "SELECT "+f+"($1::oid) AS definition", nil, rewriteOID(t, p, c, !trigger))
			for _, args := range [][]any{{nil}, {nil, true}, {id, nil}, {nil, nil}} {
				call := f + "($1)"
				if len(args) == 2 {
					call = f + "($1,$2)"
				}
				rewriteText(t, p, b, c, "SELECT "+call+" AS definition", nil, args...)
			}
			q := "SELECT " + f + "($1::oid) AS definition"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(drop, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(create, 0))
			if rewriteOID(t, p, c, trigger) == id {
				t.Fatal("recreation revived retired identity")
			}
			rewriteText(t, p, b, c, q, nil, id)
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, nil, id))
		}
	})
}
