package reference_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRewriteAssignmentTargets(t *testing.T) {
	for _, kind := range []string{"insert", "insert_source_subquery", "update", "conflict", "update_slice", "update_subquery", "update_member", "cte_insert", "cte_update", "cte_update_subquery", "cte_conflict", "cte_merge_update"} {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(kind+"/"+route, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "rw_target_a", "rw_target_b")
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
						for _, q := range []string{
							"CREATE SCHEMA rw_target_a", "CREATE SCHEMA rw_target_b",
							"CREATE FUNCTION rw_target_a.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 1",
							"CREATE FUNCTION rw_target_b.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 2",
							"CREATE TABLE public.rw_target_event(id integer,v integer)",
							"CREATE TABLE public.rw_target_log(id integer)",
							"CREATE TABLE public.rw_target_input(id integer,v integer)",
						} {
							run(fixture.Exec(q, 0))
						}
						member := kind == "update_member"
						if member {
							run(fixture.Exec("CREATE TYPE public.rw_target_box AS (a integer[])", 0))
							run(fixture.Exec("CREATE TABLE public.rw_target_sink(id integer PRIMARY KEY,b public.rw_target_box)", 0))
						} else {
							run(fixture.Exec("CREATE TABLE public.rw_target_sink(id integer PRIMARY KEY,a integer[] DEFAULT ARRAY[5,6])", 0))
						}
						run(fixture.Exec("INSERT INTO public.rw_target_input VALUES(1,99)", 1))
						partialInsert := kind == "insert" || kind == "cte_insert" || kind == "insert_source_subquery"
						resetSink := func() {
							t.Helper()
							run(fixture.Exec("TRUNCATE public.rw_target_sink,public.rw_target_log", 0))
							if !partialInsert {
								seed := "INSERT INTO public.rw_target_sink VALUES(1,ARRAY[5,6])"
								if member {
									seed = "INSERT INTO public.rw_target_sink VALUES(1,ROW(ARRAY[5,6])::public.rw_target_box)"
								}
								run(fixture.Exec(seed, 1))
							}
						}
						resetSink()
						lhs, rhs := "a[pick_index()]", "99"
						if kind == "update_slice" {
							lhs, rhs = "a[pick_index():pick_index()+0]", "ARRAY[99]"
						}
						if kind == "update_subquery" || kind == "cte_update_subquery" {
							lhs = "a[(SELECT pick_index())]"
						}
						if member {
							lhs = "b.a[pick_index()]"
						}
						mutation := "UPDATE public.rw_target_sink SET " + lhs + "=" + rhs + " WHERE id=1"
						switch kind {
						case "insert", "cte_insert":
							mutation = "INSERT INTO public.rw_target_sink(id,a[pick_index()]) VALUES(1,99)"
						case "insert_source_subquery":
							mutation = "INSERT INTO public.rw_target_sink(id,a[(SELECT pick_index())]) SELECT 1,99"
						case "conflict", "cte_conflict":
							mutation = "INSERT INTO public.rw_target_sink VALUES(1,ARRAY[99,99]) ON CONFLICT(id) DO UPDATE SET a[pick_index()]=EXCLUDED.a[1]"
						case "cte_merge_update":
							mutation = "MERGE INTO public.rw_target_sink USING public.rw_target_input s ON rw_target_sink.id=s.id WHEN MATCHED THEN UPDATE SET a[pick_index()]=s.v"
						}
						modifier := strings.HasPrefix(kind, "cte_")
						if modifier {
							mutation += " RETURNING rw_target_sink.id"
							mutation = "WITH moved AS (" + mutation + ") INSERT INTO public.rw_target_log SELECT id FROM moved"
						}
						definition := "CREATE OR REPLACE RULE r AS ON INSERT TO public.rw_target_event DO ALSO " + mutation
						var selected [2]int64
						for i, schema := range []string{"rw_target_a", "rw_target_b"} {
							if err := c.QueryRowContext(p.Ctx, "SELECT '"+schema+".pick_index()'::regprocedure::bigint").Scan(&selected[i]); err != nil || selected[i] <= 0 {
								t.Fatal(selected, err)
							}
						}
						checkEdge := func(id, count int64) {
							t.Helper()
							rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_proc'::regclass AND r.ev_class='public.rw_target_event'::regclass AND r.rulename='r' AND d.refobjid="+strconv.FormatInt(id, 10)+"::oid AND d.deptype='n'", count)
						}
						checkWrite := func(stage int) {
							t.Helper()
							var first, second any = int64(99), int64(6)
							if stage == 1 {
								first, second = int64(5), int64(99)
							}
							if partialInsert {
								if stage == 0 { second = nil } else { first = nil }
							}
							expressions := "a[1] AS first,a[2] AS second"
							if member { expressions = "((b).a)[1] AS first,((b).a)[2] AS second" }
							rewriteRows(t, p, b, fixture.Select("SELECT "+expressions+" FROM public.rw_target_sink", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{first, second}}))
							count := int64(0)
							if modifier { count = 1 }
							rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_target_log", count)
						}
						run(fixture.Exec("SET search_path TO rw_target_a,public,pg_catalog", 0))
						step := fixture.Exec(definition, 0)
						step.Prepared = prepared
						run(step)
						id := rewriteOID(t, p, b, "public.rw_target_event", "r")
						retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='public.rw_target_event'::regclass AND rulename='r'")
						if err != nil { t.Fatal(err) }
						defer retained.Close()
						first := rewriteTrees(t, p, b, "public.rw_target_event", "r")
						checkEdge(selected[0], 1)
						checkEdge(selected[1], 0)
						step = fixture.Exec("INSERT INTO public.rw_target_event VALUES(10,99)", 1)
						step.Prepared = prepared
						run(step)
						checkWrite(0)
						run(fixture.Exec("SET search_path TO rw_target_b,public,pg_catalog", 0))
						if got := rewriteTrees(t, p, b, "public.rw_target_event", "r"); got != first { t.Fatal("path edit reselected an installed target bound", got, first) }
						resetSink()
						// This is a fresh Prepare after the path change, never a held
						// already-bound CREATE that is expected to reselect.
						step = fixture.Exec(definition, 0)
						step.Prepared = prepared
						run(step)
						second := rewriteTrees(t, p, b, "public.rw_target_event", "r")
						if id != rewriteOID(t, p, b, "public.rw_target_event", "r") { t.Fatal("target-bound replacement changed rule identity") }
						checkEdge(selected[0], 0)
						checkEdge(selected[1], 1)
						step = fixture.Exec("INSERT INTO public.rw_target_event VALUES(20,99)", 1)
						step.Prepared = prepared
						run(step)
						checkWrite(1)
						run(fixture.Exec("ALTER FUNCTION rw_target_b.pick_index() RENAME TO renamed_index", 0))
						if got := rewriteTrees(t, p, b, "public.rw_target_event", "r"); got != second { t.Fatal("callee rename mutated captured target paths", got, second) }
						rewriteRows(t, p, b, fixture.Select("SELECT strpos(pg_get_ruledef(oid),'renamed_index')>0 AS current_name FROM pg_rewrite WHERE ev_class='public.rw_target_event'::regclass AND rulename='r'", []string{"current_name"}, []string{"BOOL"}, [][]any{{true}}))
						run(fixture.Exec("SET search_path TO public,pg_catalog", 0))
						run(fixture.ImplicitError("DROP FUNCTION rw_target_b.renamed_index() RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function rw_target_b.renamed_index() because other objects depend on it", Detail: "rule r on table rw_target_event depends on function rw_target_b.renamed_index()", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
						if got := rewriteTrees(t, p, b, "public.rw_target_event", "r"); got != second { t.Fatal("RESTRICT changed target metadata") }
						checkWrite(1)
						run(fixture.Exec("DROP FUNCTION rw_target_a.pick_index() RESTRICT", 0))
						run(fixture.Exec("DROP FUNCTION rw_target_b.renamed_index() CASCADE", 0))
						rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+strconv.FormatInt(id, 10), 0)
						rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_target_event", 2)
						if got := rewriteTreeRows(t, b, retained); got != first { t.Fatal("retained target-bound predecessor lost original owner", got, first) }
						resetSink()
						run(fixture.Exec("INSERT INTO public.rw_target_event VALUES(30,99)", 1))
						rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_target_event", 3)
						rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_target_log", 0)
						if partialInsert {
							rewriteCount(t, p, b, "SELECT count(*) FROM public.rw_target_sink", 0)
						} else {
							expressions := "a[1] AS first,a[2] AS second"
							if member { expressions = "((b).a)[1] AS first,((b).a)[2] AS second" }
							rewriteRows(t, p, b, fixture.Select("SELECT "+expressions+" FROM public.rw_target_sink", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(6)}}))
						}
						// Effects and dependency assertions precede metadata completeness.
						for i, tree := range [2][2]string{first, second} {
							witness := "SQLRoutine " + strconv.FormatInt(selected[i], 10) + " "
							if b.PG { witness = ":funcid " + strconv.FormatInt(selected[i], 10) + " " }
							if !strings.Contains(tree[1], witness) { t.Fatal("action metadata omitted callable selected only in assignment target", witness, tree[1]) }
						}
						if first == second { t.Fatal("target-only selected declaration replacement collapsed", first, second) }
					})
				}
			})
		}
	}
}

func rewriteAssignmentTargetSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rw_target_smoke_a", "rw_target_smoke_b")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			defer c.ExecContext(context.Background(), "SET search_path TO public")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA rw_target_smoke_a", "CREATE SCHEMA rw_target_smoke_b",
				"CREATE FUNCTION rw_target_smoke_a.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 1",
				"CREATE FUNCTION rw_target_smoke_b.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 2",
				"CREATE TABLE public.rw_target_smoke_event(id integer)",
				"CREATE TABLE public.rw_target_smoke_sink(id integer PRIMARY KEY,a integer[])",
			} { run(fixture.Exec(q, 0)) }
			run(fixture.Exec("INSERT INTO public.rw_target_smoke_sink VALUES(1,ARRAY[5,6])", 1))
			var rule int64
			var first [2]string
			for phase, schema := range []string{"rw_target_smoke_a", "rw_target_smoke_b"} {
				run(fixture.Exec("SET search_path TO "+schema+",public,pg_catalog", 0))
				step := fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO public.rw_target_smoke_event DO ALSO UPDATE public.rw_target_smoke_sink SET a[pick_index()]=99 WHERE id=1", 0)
				step.Prepared = phase == 1
				run(step)
				current := rewriteOID(t, p, b, "public.rw_target_smoke_event", "r")
				if phase == 0 { rule = current } else if current != rule { t.Fatal("target replacement changed rule identity") }
				var selected int64
				if err := c.QueryRowContext(p.Ctx, "SELECT '"+schema+".pick_index()'::regprocedure::bigint").Scan(&selected); err != nil { t.Fatal(err) }
				tree := rewriteTrees(t, p, b, "public.rw_target_smoke_event", "r")
				witness := "SQLRoutine "+strconv.FormatInt(selected, 10)+" "
				if b.PG { witness = ":funcid "+strconv.FormatInt(selected, 10)+" " }
				if !strings.Contains(tree[1], witness) { t.Fatal("target-only selected declaration missing", tree) }
				if phase == 0 { first = tree } else if tree == first { t.Fatal("target selection collapsed") }
				run(fixture.Exec("UPDATE public.rw_target_smoke_sink SET a=ARRAY[5,6]", 1))
				run(fixture.Exec("INSERT INTO public.rw_target_smoke_event VALUES(1)", 1))
				want := []any{int64(99), int64(6)}
				if phase == 1 { want = []any{int64(5), int64(99)} }
				rewriteRows(t, p, b, fixture.Select("SELECT a[1] AS first,a[2] AS second FROM public.rw_target_smoke_sink", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{want}))
			}
		})
	}
}

// Current defaults belong only to outputs not supplied by the composed
// producer. The sink's array default must not become EXCLUDED or overwrite a
// partial result; omitted/identity-discard inputs still request defaults.
func TestReferencePGRewriteAssignmentDefaults(t *testing.T) {
	for _, kind := range []string{"supplied", "reordered", "explicit_null", "partial", "omitted", "identity_discard", "event_default", "event_default_multi", "action_default", "action_default_multi", "action_default_absent", "action_null", "action_default_sequence", "action_identity_discard", "action_default_error", "action_default_current"} {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared { route = "prepared" }
			t.Run(kind+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						if strings.HasPrefix(kind, "action_") {
							rewriteAssignmentActionDefaults(t, p, b, prepared, kind)
							return
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE rw_default_event(id integer DEFAULT 101)", 0))
						run(fixture.Exec("CREATE TABLE rw_default_sink(id integer GENERATED BY DEFAULT AS IDENTITY,a integer[] DEFAULT ARRAY[5,6],stamp integer DEFAULT 7)", 0))
						action := "INSERT INTO rw_default_sink(id,a) VALUES(101,ARRAY[99,99])"
						var id any = int64(101)
						var first any = int64(99)
						var second any = int64(99)
						event := "INSERT INTO rw_default_event VALUES(101)"
						count := int64(1)
						switch kind {
						case "reordered":
							action = "INSERT INTO rw_default_sink(a,id) VALUES(ARRAY[99,99],101)"
						case "explicit_null":
							action = "INSERT INTO rw_default_sink(id,a) VALUES(101,NULL)"
							first, second = nil, nil
						case "partial":
							action = "INSERT INTO rw_default_sink(id,a[1]) VALUES(101,99)"
							second = nil
						case "omitted":
							action = "INSERT INTO rw_default_sink(a) VALUES(ARRAY[99,99])"
							id = int64(1)
						case "identity_discard":
							action = "INSERT INTO rw_default_sink(id,a) OVERRIDING USER VALUE VALUES(101,ARRAY[99,99])"
							id = int64(1)
						case "event_default", "event_default_multi":
							action = "INSERT INTO rw_default_sink(id,a) VALUES(NEW.id,ARRAY[99,99])"
							event = "INSERT INTO rw_default_event VALUES(DEFAULT)"
							if kind == "event_default_multi" {
								event = "INSERT INTO rw_default_event VALUES(DEFAULT),(DEFAULT)"
								count = 2
							}
						}
						step := fixture.Exec("CREATE RULE r AS ON INSERT TO rw_default_event DO ALSO "+action, 0)
						step.Prepared = prepared
						run(step)
						step = fixture.Exec(event, count)
						step.Prepared = prepared
						run(step)
						want := [][]any{{id, first, second, int64(7)}}
						if count == 2 { want = append(want, want[0]) }
						rewriteRows(t, p, b, fixture.Select("SELECT id,a[1] AS first,a[2] AS second,stamp FROM rw_default_sink", []string{"id", "first", "second", "stamp"}, []string{"INT4", "INT4", "INT4", "INT4"}, want))
						rewriteCount(t, p, b, "SELECT count(*) FROM rw_default_event", count)
					})
				}
			})
		}
	}
}

// Unlike event_default, these execute DEFAULT inside the stored INSERT action.
// The action's real query producer must consume its destination's default
// sentinel before the executor reads a projected expression or VALUES cell.
func rewriteAssignmentActionDefaults(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, kind string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE rw_default_event(id integer)", 0))
	sink := "CREATE TABLE rw_default_sink(id integer DEFAULT 17,v integer DEFAULT 101)"
	if kind == "action_default_absent" {
		sink = "CREATE TABLE rw_default_sink(id integer DEFAULT 17,v integer)"
	}
	if kind == "action_default_sequence" {
		run(fixture.Exec("CREATE SEQUENCE rw_default_seq START WITH 101", 0))
		sink = "CREATE TABLE rw_default_sink(id integer DEFAULT 17,v integer DEFAULT nextval('rw_default_seq'))"
	}
	if kind == "action_identity_discard" {
		sink = "CREATE TABLE rw_default_sink(id integer GENERATED BY DEFAULT AS IDENTITY(START WITH 7),v integer DEFAULT 101)"
	}
	if kind == "action_default_error" {
		sink = "CREATE TABLE rw_default_sink(id integer DEFAULT 17,v integer DEFAULT 1/0)"
	}
	run(fixture.Exec(sink, 0))
	action := "INSERT INTO rw_default_sink(v,id) VALUES(DEFAULT,7)"
	want := [][]any{{int64(7), int64(101)}}
	switch kind {
	case "action_default_multi":
		action = "INSERT INTO rw_default_sink(v,id) VALUES(DEFAULT,7),(202,8)"
		want = append(want, []any{int64(8), int64(202)})
	case "action_default_absent":
		want = [][]any{{int64(7), nil}}
	case "action_null":
		action = "INSERT INTO rw_default_sink(v,id) VALUES(NULL,7)"
		want = [][]any{{int64(7), nil}}
	case "action_default_sequence":
		action = "INSERT INTO rw_default_sink(v,id) VALUES(DEFAULT,7),(202,8),(DEFAULT,9)"
		want = [][]any{{int64(7), int64(101)}, {int64(8), int64(202)}, {int64(9), int64(102)}}
	case "action_identity_discard":
		action = "INSERT INTO rw_default_sink(v,id) OVERRIDING USER VALUE VALUES(DEFAULT,999),(202,1000)"
		want = [][]any{{int64(7), int64(101)}, {int64(8), int64(202)}}
	case "action_default_error":
		want = nil
	case "action_default_current":
		want = [][]any{{int64(7), int64(303)}}
	}
	step := fixture.Exec("CREATE RULE r AS ON INSERT TO rw_default_event DO ALSO "+action, 0)
	step.Prepared = prepared
	run(step)
	id := rewriteOID(t, p, b, "rw_default_event", "r")
	before := rewriteTrees(t, p, b, "rw_default_event", "r")
	retained, err := b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='rw_default_event'::regclass AND rulename='r'")
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	event := "INSERT INTO rw_default_event VALUES(101)"
	var eventExecutor fixture.Executor = b.DB
	if kind == "action_default_current" {
		if prepared {
			held, err := b.DB.PrepareContext(p.Ctx, event)
			if err != nil { t.Fatal(err) }
			defer held.Close()
			eventExecutor = fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: event}
		}
		run(fixture.Exec("ALTER TABLE rw_default_sink ALTER COLUMN v SET DEFAULT 303", 0))
	}
	step = fixture.Exec(event, 1)
	step.Prepared = prepared && kind != "action_default_current"
	eventRows := [][]any{{int64(101)}}
	eventCount := int64(1)
	if kind == "action_default_error" {
		step = fixture.ImplicitError(event, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		step.Prepared = prepared
		eventRows, eventCount = nil, 0
	}
	fixture.RunStep(t, p.Ctx, eventExecutor, b.Recorder, step)
	rewriteRows(t, p, b, fixture.Select("SELECT id,v FROM rw_default_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
	rewriteRows(t, p, b, fixture.Select("SELECT id FROM rw_default_event", []string{"id"}, []string{"INT4"}, eventRows))
	rewriteCount(t, p, b, "SELECT count(*) FROM rw_default_event", eventCount)
	if kind == "action_default_sequence" {
		rewriteRows(t, p, b, fixture.Select("SELECT currval('rw_default_seq') AS consumed", []string{"consumed"}, []string{"INT8"}, [][]any{{int64(102)}}))
	}
	if kind == "action_identity_discard" {
		run(fixture.Exec("INSERT INTO rw_default_sink(v) VALUES(303)", 1))
		want = append(want, []any{int64(9), int64(303)})
	}
	if got := rewriteTrees(t, p, b, "rw_default_event", "r"); got != before {
		t.Fatal("executing action defaults mutated the stored owner", got, before)
	}
	if current := rewriteOID(t, p, b, "rw_default_event", "r"); current != id {
		t.Fatal("executing action defaults changed rule identity", current, id)
	}
	run(fixture.Exec("DROP RULE r ON rw_default_event", 0))
	rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+strconv.FormatInt(id, 10), 0)
	if got := rewriteTreeRows(t, b, retained); got != before {
		t.Fatal("retained action-default predecessor lost its owner", got, before)
	}
	rewriteRows(t, p, b, fixture.Select("SELECT id,v FROM rw_default_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
}
