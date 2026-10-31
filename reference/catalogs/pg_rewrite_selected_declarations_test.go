package reference_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func rewriteSelectedTree(t *testing.T, b fixture.Backend, action string, id int64, window bool) {
	t.Helper()
	if b.PG {
		field := ":aggfnoid "
		if window {
			field = ":winfnoid "
		}
		if !strings.Contains(action, field+strconv.FormatInt(id, 10)+" ") {
			t.Fatal("missing selected PostgreSQL callable", id, action)
		}
	} else {
		pattern := `(?:^|[[:space:]/])` + strconv.FormatInt(id, 10) + `(?:[[:space:]/}]|$)`
		if !regexp.MustCompile(pattern).MatchString(action) {
			t.Fatal("native tree omits selected declaration identity", id, action)
		}
	}
}

func TestReferencePGRewriteSelectedDeclarations(t *testing.T) {
	for _, window := range []bool{false, true} {
		name := "aggregate"
		if window {
			name = "window"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "rw_stage_a", "rw_stage_b")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(context.Background(), "SET search_path TO public")
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE SCHEMA rw_stage_a", "CREATE SCHEMA rw_stage_b", "CREATE TABLE public.rw_stage_input(id integer,v integer)", "CREATE AGGREGATE rw_stage_a.pick(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE rw_stage_b.pick(integer)(SFUNC=pg_catalog.int4larger,STYPE=integer,INITCOND='0')"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO public.rw_stage_input VALUES(1,1),(2,2)", 2))
					var selected [2]int64
					for i, schema := range []string{"rw_stage_a", "rw_stage_b"} {
						if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='"+schema+"' AND p.proname='pick' AND p.prokind='a'").Scan(&selected[i]); err != nil || selected[i] <= 0 {
							t.Fatal(selected, err)
						}
					}
					if selected[0] == selected[1] {
						t.Fatal("distinct declarations share an identity")
					}
					view, selectSQL := "public.rw_stage_view", "SELECT pick(v) AS result FROM public.rw_stage_input"
					if window {
						view, selectSQL = "public.rw_stage_window", "SELECT id,pick(v) OVER() AS result FROM public.rw_stage_input"
					}
					definition := "CREATE OR REPLACE VIEW " + view + " AS " + selectSQL
					checkResult := func(total int64) {
						t.Helper()
						q := "SELECT * FROM " + view
						columns, types, rows := []string{"result"}, []string{"INT4"}, [][]any{{total}}
						if window {
							q += " ORDER BY id"
							columns, types, rows = []string{"id", "result"}, []string{"INT4", "INT4"}, [][]any{{int64(1), total}, {int64(2), total}}
						}
						rewriteRows(t, p, b, fixture.Select(q, columns, types, rows))
					}
					checkEdge := func(id int64, n int64) {
						t.Helper()
						rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_proc'::regclass AND r.ev_class='"+view+"'::regclass AND d.refobjid="+strconv.FormatInt(id, 10)+"::oid AND d.deptype='n'", n)
					}
					checkSource := func() {
						t.Helper()
						rewriteCount(t, p, b, "SELECT count(*) FROM (SELECT DISTINCT d.refobjid FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='"+view+"'::regclass AND d.refobjid='public.rw_stage_input'::regclass AND d.deptype='n') edges", 1)
					}
					checkSelected := func(action string, id int64) { t.Helper(); rewriteSelectedTree(t, b, action, id, window) }
					run(fixture.Exec("SET search_path TO rw_stage_a,public,pg_catalog", 0))
					statement, err := c.PrepareContext(p.Ctx, definition)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: definition}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(definition, 0))
					id := rewriteOID(t, p, b, view, "_RETURN")
					var relationID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT '"+view+"'::regclass::bigint").Scan(&relationID); err != nil {
						t.Fatal(err)
					}
					first := rewriteTrees(t, p, b, view, "_RETURN")
					checkResult(3)
					checkEdge(selected[0], 1)
					checkEdge(selected[1], 0)
					checkSource()
					retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='"+view+"'::regclass AND rulename='_RETURN'")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					run(fixture.Exec("SET search_path TO rw_stage_b,public,pg_catalog", 0))
					// A path edit alone does not re-resolve an installed definition.
					checkResult(3)
					if got := rewriteTrees(t, p, b, view, "_RETURN"); got != first {
						t.Fatal("path edit rewrote captured definition", got, first)
					}
					// Prepare again after the path switch: frozen CREATE syntax
					// deliberately retains its previously selected namespace.
					replacement, err := c.PrepareContext(p.Ctx, definition)
					if err != nil {
						t.Fatal(err)
					}
					defer replacement.Close()
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: replacement, SQL: definition}, b.Recorder, fixture.Exec(definition, 0))
					second := rewriteTrees(t, p, b, view, "_RETURN")
					checkResult(2)
					checkEdge(selected[0], 0)
					checkEdge(selected[1], 1)
					checkSource()
					var replacementID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT '"+view+"'::regclass::bigint").Scan(&replacementID); err != nil {
						t.Fatal(err)
					}
					if id != rewriteOID(t, p, b, view, "_RETURN") || relationID != replacementID {
						t.Fatal("replacement changed relation/rewrite identity")
					}
					if first == second {
						t.Fatal("same-spelling calls selected different declarations but emitted identical definition trees", first)
					}
					checkSelected(first[1], selected[0])
					checkSelected(second[1], selected[1])
					run(fixture.Exec("ALTER AGGREGATE rw_stage_b.pick(integer) RENAME TO renamed_pick", 0))
					if got := rewriteTrees(t, p, b, view, "_RETURN"); got != second {
						t.Fatal("callee rename changed selected identity", got, second)
					}
					rewriteRows(t, p, b, fixture.Select("SELECT strpos(pg_get_viewdef('"+view+"'::regclass),'renamed_pick')>0 AS current_name", []string{"current_name"}, []string{"BOOL"}, [][]any{{true}}))
					checkResult(2)
					run(fixture.Exec("SET search_path TO public", 0))
					restrict := fixture.ImplicitError("DROP AGGREGATE rw_stage_b.renamed_pick(integer) RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function rw_stage_b.renamed_pick(integer) because other objects depend on it", Detail: "view " + strings.TrimPrefix(view, "public.") + " depends on function rw_stage_b.renamed_pick(integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					run(restrict)
					if got := rewriteTrees(t, p, b, view, "_RETURN"); got != second {
						t.Fatal("RESTRICT changed captured definition")
					}
					checkResult(2)
					run(fixture.Exec("DROP AGGREGATE rw_stage_a.pick(integer) RESTRICT", 0))
					run(fixture.Exec("DROP AGGREGATE rw_stage_b.renamed_pick(integer) CASCADE", 0))
					rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+strconv.FormatInt(id, 10), 0)
					rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_stage_input", 2)
					if got := rewriteTreeRows(t, b, retained); got != first {
						t.Fatal("retained predecessor re-resolved dropped declaration", got, first)
					}
				})
			}
		})
	}
	for _, window := range []bool{false, true} {
		name := "rule_aggregate"
		if window {
			name = "rule_window"
		}
		t.Run(name, func(t *testing.T) {
			kind := "aggregate"
			if window {
				kind = "window"
			}
			rewriteSelectedRule(t, kind)
		})
	}
}

func rewriteSelectedRule(t *testing.T, kind string) {
	t.Helper()
	window := kind == "window" || kind == "cte_window"
	modifier := strings.HasPrefix(kind, "cte_")
	fixture.SchemaReferenceCleanup(t, "rw_rule_a", "rw_rule_b")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "SET search_path TO public")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA rw_rule_a", "CREATE SCHEMA rw_rule_b", "CREATE TABLE public.rw_rule_input(v integer)", "CREATE TABLE public.rw_rule_event(v integer)", "CREATE TABLE public.rw_rule_sink(result integer)", "CREATE AGGREGATE rw_rule_a.pick(integer)(SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE rw_rule_b.pick(integer)(SFUNC=pg_catalog.int4larger,STYPE=integer,INITCOND='0')"} {
				run(fixture.Exec(q, 0))
			}
			if modifier {
				run(fixture.Exec("CREATE TABLE public.rw_rule_aux(result integer PRIMARY KEY)", 0))
			}
			run(fixture.Exec("INSERT INTO public.rw_rule_input VALUES(1),(2)", 2))
			expression := "pick(v)"
			if window {
				expression += " OVER()"
			}
			definition := "CREATE OR REPLACE RULE r AS ON INSERT TO public.rw_rule_event DO ALSO INSERT INTO public.rw_rule_sink SELECT " + expression + " FROM public.rw_rule_input"
			if modifier {
				source := "SELECT " + expression + " FROM public.rw_rule_input"
				if kind == "cte_subquery" {
					source = "SELECT (SELECT pick(v) FROM public.rw_rule_input)"
				}
				conflict := ""
				if kind == "cte_conflict" {
					conflict = " ON CONFLICT(result) DO UPDATE SET result=EXCLUDED.result"
				}
				mutation := "INSERT INTO public.rw_rule_aux(result) " + source + conflict + " RETURNING result"
				if kind == "cte_merge" {
					mutation = "MERGE INTO public.rw_rule_aux USING (SELECT pick(v) AS result FROM public.rw_rule_input) q ON FALSE WHEN NOT MATCHED THEN INSERT(result) VALUES(q.result) RETURNING rw_rule_aux.result"
				}
				// Window results share the same sum/max; the modifier must retain
				// both rows rather than turn the witness into a uniqueness error.
				if window {
					run(fixture.Exec("ALTER TABLE public.rw_rule_aux DROP CONSTRAINT rw_rule_aux_pkey", 0))
				}
				definition = "CREATE OR REPLACE RULE r AS ON INSERT TO public.rw_rule_event DO ALSO WITH moved AS (" + mutation + ") INSERT INTO public.rw_rule_sink SELECT result FROM moved"
			}
			var selected [2]int64
			for i, schema := range []string{"rw_rule_a", "rw_rule_b"} {
				if err := c.QueryRowContext(p.Ctx, "SELECT '"+schema+".pick(integer)'::regprocedure::bigint").Scan(&selected[i]); err != nil {
					t.Fatal(err)
				}
			}
			checkEdge := func(oid int64, n int64) {
				t.Helper()
				rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_proc'::regclass AND r.ev_class='public.rw_rule_event'::regclass AND r.rulename='r' AND d.refobjid="+strconv.FormatInt(oid, 10)+"::oid AND d.deptype='n'", n)
			}
			checkSink := func(total int64) {
				t.Helper()
				rows := [][]any{{total}}
				if window {
					rows = append(rows, []any{total})
				}
				rewriteRows(t, p, b, fixture.Select("SELECT result FROM public.rw_rule_sink", []string{"result"}, []string{"INT4"}, rows))
			}
			run(fixture.Exec("SET search_path TO rw_rule_a,public,pg_catalog", 0))
			run(fixture.Exec(definition, 0))
			id := rewriteOID(t, p, b, "public.rw_rule_event", "r")
			first := rewriteTrees(t, p, b, "public.rw_rule_event", "r")
			checkEdge(selected[0], 1)
			checkEdge(selected[1], 0)
			run(fixture.Exec("INSERT INTO public.rw_rule_event VALUES(10)", 1))
			checkSink(3)
			if modifier {
				checkAux := [][]any{{int64(3)}}
				if window {
					checkAux = append(checkAux, []any{int64(3)})
				}
				rewriteRows(t, p, b, fixture.Select("SELECT result FROM public.rw_rule_aux", []string{"result"}, []string{"INT4"}, checkAux))
			}
			retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='public.rw_rule_event'::regclass AND rulename='r'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			run(fixture.Exec("TRUNCATE public.rw_rule_sink", 0))
			if modifier {
				run(fixture.Exec("TRUNCATE public.rw_rule_aux", 0))
			}
			run(fixture.Exec("SET search_path TO rw_rule_b,public,pg_catalog", 0))
			run(fixture.Prepared(fixture.Exec(definition, 0)))
			second := rewriteTrees(t, p, b, "public.rw_rule_event", "r")
			checkEdge(selected[0], 0)
			checkEdge(selected[1], 1)
			run(fixture.Prepared(fixture.Exec("INSERT INTO public.rw_rule_event VALUES(20)", 1)))
			checkSink(2)
			if id != rewriteOID(t, p, b, "public.rw_rule_event", "r") || first == second {
				t.Fatal("actual rule replacement failed to distinguish selected stage ownership", first, second)
			}
			rewriteSelectedTree(t, b, first[1], selected[0], window)
			rewriteSelectedTree(t, b, second[1], selected[1], window)
			run(fixture.Exec("DROP AGGREGATE rw_rule_a.pick(integer) RESTRICT", 0))
			run(fixture.Exec("DROP AGGREGATE rw_rule_b.pick(integer) CASCADE", 0))
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+strconv.FormatInt(id, 10), 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_rule_event", 2)
			if got := rewriteTreeRows(t, b, retained); got != first {
				t.Fatal("rule predecessor lost dropped selected owner", got, first)
			}
		})
	}
}

func TestReferencePGRewriteMutationCTE(t *testing.T) {
	for _, kind := range []string{"cte_aggregate", "cte_window", "cte_subquery", "cte_conflict", "cte_merge"} {
		t.Run(kind, func(t *testing.T) { rewriteSelectedRule(t, kind) })
	}
}
