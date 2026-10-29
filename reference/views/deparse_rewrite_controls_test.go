package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteNativeRoundTrip(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		rewriteSeed(t, p, b, c)
		for _, q := range []string{
			"CREATE VIEW v(label) AS SELECT v FROM t",
			"CREATE VIEW v_alias(label) AS SELECT explicit_source.v FROM t AS explicit_source",
			"CREATE VIEW v_named(label) AS SELECT __view_source_0.v FROM t AS __view_source_0",
			"CREATE TRIGGER disabled AFTER UPDATE OF v ON t FOR EACH ROW WHEN(OLD.v<NEW.v) EXECUTE FUNCTION tf('saved','')",
			"ALTER TABLE t DISABLE TRIGGER disabled",
			"CREATE RULE inactive AS ON DELETE TO src DO INSTEAD NOTHING",
			"ALTER TABLE src ENABLE REPLICA RULE inactive",
			"CREATE TABLE ordered_src(v INT)", "CREATE TABLE ordered_dst(v INT)",
			"CREATE RULE ordered AS ON INSERT TO ordered_src DO INSTEAD (INSERT INTO ordered_dst VALUES(NEW.v); INSERT INTO ordered_dst VALUES(NEW.v+1))",
			"CREATE TABLE parent(v INT) PARTITION BY RANGE(v)",
			"CREATE TABLE child PARTITION OF parent FOR VALUES FROM(0) TO(100)",
			"CREATE FUNCTION clone_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER clone BEFORE INSERT ON parent FOR EACH ROW EXECUTE FUNCTION clone_fn()",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO t VALUES(5)", 1), fixture.Exec("INSERT INTO src VALUES(7)", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		restored := false
		check := func() {
			rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", rewriteTriggerDefault)
			rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid) AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", rewriteRuleDefault)
			rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='disabled'", "CREATE TRIGGER disabled AFTER UPDATE OF v ON public.t FOR EACH ROW WHEN ((old.v < new.v)) EXECUTE FUNCTION tf('saved', '')")
			rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid) AS definition FROM pg_rewrite WHERE ev_class='ordered_src'::regclass AND rulename='ordered'", "CREATE RULE ordered AS\n    ON INSERT TO public.ordered_src DO INSTEAD ( INSERT INTO ordered_dst (v)\n  VALUES (new.v);\n INSERT INTO ordered_dst (v)\n  VALUES ((new.v + 1));\n);")
			// Native replay uses positional source bridges. Introspection
			// reconstructs that stored definition, not the original SQL text.
			for _, view := range []struct{ name, alias string }{
				{"v", ""},
				{"v_alias", " explicit_source"},
				{"v_named", " __view_source_0"},
			} {
				alias := view.alias
				if restored && view.name == "v" {
					alias = " __view_source_0"
				}
				for _, suffix := range []string{"", ",false", ",true"} {
					target := "public." + view.name
					if suffix == ",true" {
						target = view.name
					}
					definition := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO " + target + " DO INSTEAD  SELECT v AS label\n   FROM t" + alias + ";"
					rewriteText(t, p, b, c, "SELECT pg_get_ruledef(oid"+suffix+") AS definition FROM pg_rewrite WHERE ev_class='"+view.name+"'::regclass AND rulename='_RETURN'", definition)
				}
			}
			for _, target := range []string{"parent", "child"} {
				rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='"+target+"'::regclass AND tgname='clone'", "CREATE TRIGGER clone BEFORE INSERT ON public."+target+" FOR EACH ROW EXECUTE FUNCTION clone_fn()")
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}),
				fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, nil),
				fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}),
				fixture.Select("SELECT tgname,tgenabled FROM pg_trigger WHERE tgrelid='t'::regclass ORDER BY tgname", []string{"tgname", "tgenabled"}, []string{"NAME", "CHAR"}, [][]any{{"disabled", "D"}, {"tr", "O"}}),
				fixture.Select("SELECT rulename,ev_enabled FROM pg_rewrite WHERE ev_class='src'::regclass ORDER BY rulename", []string{"rulename", "ev_enabled"}, []string{"NAME", "CHAR"}, [][]any{{"inactive", "R"}, {"r", "O"}}),
				fixture.Select("SELECT t.tgparentid=p.oid AS linked FROM pg_trigger t JOIN pg_trigger p ON p.tgrelid='parent'::regclass AND p.tgname='clone' WHERE t.tgrelid='child'::regclass AND t.tgname='clone'", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}),
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		check()
		if !b.PG {
			q := "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'"
			stmt, err := c.PrepareContext(p.Ctx, "SELECT count(*) FROM pg_trigger WHERE tgname='tr'")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT count(*) FROM pg_trigger WHERE tgname='tr'"}
			oldRows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer oldRows.Close()
			fixture.DeparseRelationsMetadata(t, false, oldRows)
			for range 2 {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				restored = true
				check()
			}
			if !oldRows.Next() {
				t.Fatal("retained rows lost", oldRows.Err())
			}
			var raw any
			if err := oldRows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, false, "TEXT", raw)
			if fixture.Normalize(raw) != rewriteTriggerDefault || oldRows.Next() || oldRows.Err() != nil {
				t.Fatal("retained definition changed", raw, oldRows.Err())
			}
			oldRows.Close()
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, query := range []string{q, "SELECT pg_get_ruledef(oid) AS definition FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'"} {
				r, err := c.QueryContext(canceled, query)
				if r != nil {
					r.Close()
					t.Fatal("canceled deparse published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			check()
		}
		// Future effects after both native restore rounds (or the shared PG
		// baseline) prove the restored metadata is executable and not just text.
		for _, s := range []fixture.Step{
			fixture.Exec("INSERT INTO t VALUES(6)", 1), fixture.Exec("INSERT INTO src VALUES(8)", 1), fixture.Exec("INSERT INTO ordered_src VALUES(20)", 1), fixture.Exec("INSERT INTO parent VALUES(30)", 1),
			fixture.Select("SELECT label FROM v ORDER BY label", []string{"label"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(12)}}),
			fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}),
			fixture.Select("SELECT v FROM src", []string{"v"}, []string{"INT4"}, nil),
			fixture.Select("SELECT v FROM ordered_dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(21)}}),
			fixture.Select("SELECT v FROM child", []string{"v"}, []string{"INT4"}, [][]any{{int64(30)}}),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
