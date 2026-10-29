package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparseRewriteTriggerDefinition(t *testing.T) {
	for _, pretty := range []bool{false, true} {
		t.Run(fmt.Sprint(pretty), func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				rewriteSeed(t, p, b, c)
				q := fmt.Sprintf("SELECT pg_get_triggerdef(oid,%v) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", pretty)
				want := rewriteTriggerDefault
				if pretty {
					want = rewriteTriggerPretty
				}
				rewriteText(t, p, b, c, q, want)
				definition := fixture.DeparseRelationsRead(t, p, b, c, q)
				for _, s := range []fixture.Step{
					fixture.Exec("DROP TRIGGER tr ON t", 0), fixture.Exec(definition, 0), fixture.Exec("INSERT INTO t VALUES(5)", 1),
					fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}),
					fixture.Select("SELECT count(*) FROM pg_trigger WHERE tgrelid='t'::regclass AND tgname='tr'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
}

func TestReferenceDeparseRewriteTriggerClauses(t *testing.T) {
	cases := []struct{ name, ddl, want string }{
		{"canonical_events", "CREATE TRIGGER tr BEFORE UPDATE OR DELETE OR INSERT ON t FOR EACH ROW EXECUTE FUNCTION tf()", "CREATE TRIGGER tr BEFORE INSERT OR DELETE OR UPDATE ON public.t FOR EACH ROW EXECUTE FUNCTION tf()"},
		{"after_statement", "CREATE TRIGGER tr AFTER UPDATE OR TRUNCATE ON t FOR EACH STATEMENT EXECUTE FUNCTION tf()", "CREATE TRIGGER tr AFTER UPDATE OR TRUNCATE ON public.t FOR EACH STATEMENT EXECUTE FUNCTION tf()"},
		{"instead", "CREATE TRIGGER tr INSTEAD OF INSERT OR UPDATE ON v FOR EACH ROW EXECUTE FUNCTION tf()", "CREATE TRIGGER tr INSTEAD OF INSERT OR UPDATE ON public.v FOR EACH ROW EXECUTE FUNCTION tf()"},
		{"attributes", "CREATE TRIGGER tr BEFORE UPDATE OF c,a ON t FOR EACH ROW WHEN(OLD.a<NEW.a) EXECUTE FUNCTION tf()", "CREATE TRIGGER tr BEFORE UPDATE OF c, a ON public.t FOR EACH ROW WHEN ((old.a < new.a)) EXECUTE FUNCTION tf()"},
		{"quoted", `CREATE TRIGGER "Mixed Trigger" BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION tf('é','O''Reilly','')`, `CREATE TRIGGER "Mixed Trigger" BEFORE INSERT ON public.t FOR EACH ROW EXECUTE FUNCTION tf('é', 'O''Reilly', '')`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE TABLE t(a INT,b INT,c INT)", "CREATE VIEW v AS SELECT a,b,c FROM t", "CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", tc.ddl} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				target := "t"
				if tc.name == "instead" {
					target = "v"
				}
				q := "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='" + target + "'::regclass"
				rewriteText(t, p, b, c, q, tc.want)
				if tc.name == "attributes" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ALTER TABLE t RENAME COLUMN c TO "Last Column"`, 0))
					rewriteText(t, p, b, c, q, `CREATE TRIGGER tr BEFORE UPDATE OF "Last Column", a ON public.t FOR EACH ROW WHEN ((old.a < new.a)) EXECUTE FUNCTION tf()`)
				}
			})
		})
	}
	t.Run("clones", func(t *testing.T) {
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE parent(a INT) PARTITION BY RANGE(a)", "CREATE TABLE child PARTITION OF parent FOR VALUES FROM(0) TO(10)", "CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER clone BEFORE INSERT ON parent FOR EACH ROW EXECUTE FUNCTION tf()"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.relname,t.tgparentid<>0 AS cloned,t.oid<>t.tgparentid AS distinct_identity FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relname IN ('parent','child') ORDER BY c.relname", []string{"relname", "cloned", "distinct_identity"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"child", true, true}, {"parent", false, true}}))
			for _, target := range []string{"parent", "child"} {
				for _, suffix := range []string{"", ",false", ",true"} {
					relation := "public." + target
					if suffix == ",true" {
						relation = target
					}
					rewriteText(t, p, b, c, "SELECT pg_get_triggerdef(oid"+suffix+") AS definition FROM pg_trigger WHERE tgrelid='"+target+"'::regclass", "CREATE TRIGGER clone BEFORE INSERT ON "+relation+" FOR EACH ROW EXECUTE FUNCTION tf()")
				}
			}
		})
	})
	t.Run("string_policy", func(t *testing.T) {
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", `CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION tf(E'a\\b')`} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			q := "SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='t'::regclass"
			rewriteText(t, p, b, c, q, `CREATE TRIGGER tr BEFORE INSERT ON public.t FOR EACH ROW EXECUTE FUNCTION tf('a\b')`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET standard_conforming_strings TO off", 0))
			rewriteText(t, p, b, c, q, `CREATE TRIGGER tr BEFORE INSERT ON public.t FOR EACH ROW EXECUTE FUNCTION tf('a\\b')`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET standard_conforming_strings", 0))
		})
	})
	t.Run("catalog_visibility", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rewrite_reader"})
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			rewriteSeed(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE rewrite_reader", 0))
			id := rewriteOID(t, p, c, true)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE rewrite_reader", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='t'", []string{"trigger_name"}, []string{"NAME"}, nil))
			// Both fixtures must hide public independently of their schema ACLs.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path TO pg_catalog", 0))
			rewriteText(t, p, b, c, "SELECT pg_get_triggerdef($1::oid) AS definition", "CREATE TRIGGER tr BEFORE INSERT ON public.t FOR EACH ROW WHEN ((new.v > 0)) EXECUTE FUNCTION public.tf('O''Reilly', '')", id)
			rewriteText(t, p, b, c, "SELECT pg_get_triggerdef($1::oid,true) AS definition", "CREATE TRIGGER tr BEFORE INSERT ON public.t FOR EACH ROW WHEN (new.v > 0) EXECUTE FUNCTION public.tf('O''Reilly', '')", id)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET search_path", 0))
		})
	})
}

func TestReferenceDeparseRewriteForeignKeyTriggers(t *testing.T) {
	for _, action := range []struct {
		sql, fn  string
		deferred bool
	}{{"NO ACTION", "noaction", true}, {"RESTRICT", "restrict", false}, {"CASCADE", "cascade", false}, {"SET NULL", "setnull", false}, {"SET DEFAULT", "setdefault", false}} {
		t.Run(action.sql, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{"CREATE TABLE parent(id INT PRIMARY KEY)", "CREATE TABLE child(id INT)", "ALTER TABLE child ADD CONSTRAINT fk FOREIGN KEY(id) REFERENCES parent(id) ON DELETE " + action.sql + " ON UPDATE " + action.sql + " DEFERRABLE INITIALLY DEFERRED"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				rows, err := c.QueryContext(p.Ctx, "SELECT t.oid::bigint,t.tgname,p.proname,c.relname FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_class c ON c.oid=t.tgrelid WHERE t.tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='fk' AND conrelid='child'::regclass) ORDER BY p.proname")
				if err != nil {
					t.Fatal(err)
				}
				type trigger struct {
					id              int64
					name, fn, table string
				}
				var triggers []trigger
				for rows.Next() {
					var tr trigger
					if err := rows.Scan(&tr.id, &tr.name, &tr.fn, &tr.table); err != nil {
						t.Fatal(err)
					}
					triggers = append(triggers, tr)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if len(triggers) != 4 {
					t.Fatalf("FK identities: %#v", triggers)
				}
				wantFns := map[string]string{"RI_FKey_check_ins": "INSERT", "RI_FKey_check_upd": "UPDATE", "RI_FKey_" + action.fn + "_del": "DELETE", "RI_FKey_" + action.fn + "_upd": "UPDATE"}
				seen := map[int64]bool{}
				for _, tr := range triggers {
					event, ok := wantFns[tr.fn]
					if !ok || tr.id <= 0 || seen[tr.id] {
						t.Fatal("invalid FK inventory", tr)
					}
					seen[tr.id] = true
					delete(wantFns, tr.fn)
					check := tr.fn == "RI_FKey_check_ins" || tr.fn == "RI_FKey_check_upd"
					table, other, kind := "parent", "child", "a"
					deferClause := " NOT DEFERRABLE INITIALLY IMMEDIATE"
					if check {
						table, other, kind = "child", "parent", "c"
					}
					if check || action.deferred {
						deferClause = " DEFERRABLE INITIALLY DEFERRED"
					}
					if tr.table != table || tr.name != fmt.Sprintf("RI_ConstraintTrigger_%s_%d", kind, tr.id) {
						t.Fatal("FK ownership/name", tr)
					}
					for _, suffix := range []string{"", ",false", ",true"} {
						// FROM follows counterpart visibility even when ON is forced qualified.
						rel, from := "public."+table, other
						if suffix == ",true" {
							rel = table
						}
						want := fmt.Sprintf("CREATE CONSTRAINT TRIGGER %q AFTER %s ON %s FROM %s%s FOR EACH ROW EXECUTE FUNCTION %q()", tr.name, event, rel, from, deferClause, tr.fn)
						rewriteText(t, p, b, c, "SELECT pg_get_triggerdef($1::oid"+suffix+") AS definition", want, tr.id)
					}
				}
				if len(wantFns) != 0 {
					t.Fatal("missing FK functions", wantFns)
				}
			})
		})
	}
}
