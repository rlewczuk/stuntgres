package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteAssignmentTargets(t *testing.T) {
	for _, kind := range []string{"insert", "insert_source_subquery", "update", "conflict", "update_slice", "update_subquery", "update_member", "cte_insert", "cte_update", "cte_update_subquery", "cte_conflict", "cte_merge_update"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE SCHEMA target_a", "CREATE SCHEMA target_b",
				"CREATE FUNCTION target_a.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 1",
				"CREATE FUNCTION target_b.pick_index() RETURNS integer LANGUAGE SQL VOLATILE RETURN 2",
				"CREATE TABLE public.target_event(id integer,v integer)",
				"CREATE TABLE public.target_log(id integer)",
				"CREATE TABLE public.target_input(id integer,v integer)",
			} {
				enginetest.SessionSQL(t, session, q)
			}
			if kind == "update_member" {
				enginetest.SessionSQL(t, session, "CREATE TYPE public.target_box AS (a integer[],other integer[])")
				enginetest.SessionSQL(t, session, "CREATE TABLE public.target_sink(id integer PRIMARY KEY,b public.target_box,other public.target_box)")
			} else if kind == "update_slice" {
				enginetest.SessionSQL(t, session, "CREATE TABLE public.target_sink(id integer PRIMARY KEY,a integer[],other integer[])")
			} else {
				enginetest.SessionSQL(t, session, "CREATE TABLE public.target_sink(id integer PRIMARY KEY,a integer[])")
			}
			lhs, rhs := "a[pick_index()]", "99"
			if kind == "update_slice" {
				lhs, rhs = "a[pick_index():pick_index()+0]", "ARRAY[99]"
			}
			if kind == "update_subquery" || kind == "cte_update_subquery" {
				lhs = "a[(SELECT pick_index())]"
			}
			if kind == "update_member" {
				lhs = "b.a[pick_index()]"
			}
			mutation := "UPDATE public.target_sink SET " + lhs + "=" + rhs + " WHERE id=1"
			switch kind {
			case "insert", "cte_insert":
				mutation = "INSERT INTO public.target_sink(id,a[pick_index()]) VALUES(1,99)"
			case "insert_source_subquery":
				mutation = "INSERT INTO public.target_sink(id,a[(SELECT pick_index())]) SELECT 1,99"
			case "conflict", "cte_conflict":
				mutation = "INSERT INTO public.target_sink VALUES(1,ARRAY[99,99]) ON CONFLICT(id) DO UPDATE SET a[pick_index()]=EXCLUDED.a[1]"
			case "cte_merge_update":
				mutation = "MERGE INTO public.target_sink USING public.target_input s ON target_sink.id=s.id WHEN MATCHED THEN UPDATE SET a[pick_index()]=s.v"
			}
			modifier := strings.HasPrefix(kind, "cte_")
			if modifier {
				mutation = "WITH moved AS (" + mutation + " RETURNING target_sink.id) INSERT INTO public.target_log SELECT id FROM moved"
			}
			ddl := "CREATE OR REPLACE RULE r AS ON INSERT TO public.target_event DO ALSO " + mutation
			var values [2]sqltype.Value
			var output [2]string
			var selected [2]ids.ObjectAddress
			var ruleOID ids.OID
			for phase, schema := range []string{"target_a", "target_b"} {
				enginetest.SessionSQL(t, session, "SET search_path TO "+schema+",public,pg_catalog")
				enginetest.SessionSQL(t, session, ddl)
				read := e.CatalogRead()
				owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "target_event").Address)
				list := owner.Rules.(stored.RuleList)
				if len(list) != 1 || len(list[0].Parts().Actions) != 1 {
					t.Fatal("missing genuine installed action owner", list)
				}
				if phase == 0 { ruleOID = list[0].OID } else if ruleOID != list[0].OID { t.Fatal("replacement changed rule identity") }
				binding := list[0].Parts().Actions[0].Binding()
				paths := 0
				calls := 0
				var inspectExpression func(expr.Expr)
				var inspectQuery func(plan.Node)
				inspectQuery = func(node plan.Node) {
					if node == nil { return }
					plan.QueryExpressions(node, inspectExpression)
					left, right := plan.QueryInputs(node)
					inspectQuery(left)
					inspectQuery(right)
				}
				inspectExpression = func(expression expr.Expr) {
					plan.WalkExpression(expression, func(node expr.Expr) {
						switch n := node.(type) {
						case plan.SQLRoutineExpr:
							calls++
							if n.Procedure == nil || n.Ref.Address != n.Procedure.Address() || read.Get(n.Ref.Address) != n.Procedure || n.Ref.Address.ClassID != ids.ProcedureClassID {
								t.Fatal("target bound lost its genuine original declaration", n)
							}
							if selected[phase] != (ids.ObjectAddress{}) && selected[phase] != n.Ref.Address { t.Fatal("slice selected inconsistent callable owners") }
							selected[phase] = n.Ref.Address
						case plan.SubqueryExpr:
							if n.QueryPlan != nil { inspectQuery(n.QueryPlan.Root) }
						}
					})
				}
				add := func(relation *catalog.Relation, slots []int, targets []*plan.ArrayAssignmentTarget) {
					for ordinal, target := range targets {
						if target == nil { continue }
						paths++
						if relation == nil || ordinal >= len(slots) || relation.Attributes[slots[ordinal]] != 2 { t.Fatal("assignment target detached from logical root attribute", ordinal, slots, target) }
						if kind == "update_member" {
							if target.Attribute != 1 || !target.Container.Type.BaseType().IsComposite() || target.Next == nil { t.Fatal("nested member target chain lost", target) }
						}
						for step := target; step != nil; step = step.Next {
							if step.Container.Type == nil || step.Destination.Type == nil { t.Fatal("missing retained target usages", step) }
							if step.Attribute != 0 { continue }
							if step.Container.Type.ID() != 1007 || len(step.Node.Subscripts) != 1 { t.Fatal("missing array target container/dimension", step) }
							want := sqltype.Int4
							if kind == "update_slice" { want = 1007 }
							if step.Destination.Type.ID() != want || step.Node.Slice != (kind == "update_slice") { t.Fatal("target destination/slice meaning lost", step) }
							for _, sub := range step.Node.Subscripts {
								if sub.Upper == nil || (sub.Lower != nil) != (kind == "update_slice") { t.Fatal("bound lower/upper association lost", sub) }
								inspectExpression(sub.Lower)
								inspectExpression(sub.Upper)
							}
						}
					}
				}
				var collect func(analyze.Binding)
				collect = func(body analyze.Binding) {
					add(body.Relation, body.Targets, body.ArrayTargets)
					if body.Update != nil { add(body.Relation, body.Targets, body.Update.ArrayTargets) }
					if body.Insert.Conflict != nil && body.Insert.Conflict.Update != nil { add(body.Relation, body.Insert.Conflict.UpdateTargets, body.Insert.Conflict.Update.ArrayTargets) }
					if body.Merge != nil { for _, arm := range body.Merge.Arms { collect(arm.Input) } }
				}
				collect(binding)
				modifiers := 0
				if err := stored.VisitRuleModifiers(binding, func(_ *plan.CTEDefinition, body *analyze.Binding) error { modifiers++; collect(*body); return nil }); err != nil { t.Fatal(err) }
				wantCalls := 1
				// Frozen scalar-query projections retain executable and analysis
				// expressions; a slice owns two distinct lower/upper roots.
				if kind == "update_slice" || strings.Contains(kind, "subquery") { wantCalls = 2 }
				if paths != 1 || calls != wantCalls || (modifiers != 0) != modifier || selected[phase].ObjectID == 0 { t.Fatal("selected callable was not owned only by the actual assignment path", paths, calls, modifiers, selected) }
				typ, _ := read.LookupType(sqltype.PgNodeTree)
				_, value, err := stored.RewriteTrees(t.Context(), read, owner, typ, &list[0], nil)
				values[phase] = value
				if err != nil { t.Fatal(err) }
				carrier, _ := values[phase].Tree()
				ctx, cancel := context.WithCancel(t.Context())
				walk := &rewriteWalkContext{Context: ctx, cancel: cancel, after: 8}
				text, err := values[phase].Type().Output(walk, values[phase])
				cancel()
				if !errors.Is(err, context.Canceled) || text != "" { t.Fatal("canceled assignment owner output published a prefix", text, err) }
				output[phase], err = carrier.StructuralOutput(t.Context())
				if err != nil { t.Fatal(err) }
				if phase == 0 && (kind == "update_slice" || kind == "update_member") {
					rewriteAssignmentFieldDistinctions(t, read, owner, typ, list[0], output[phase])
					// These ordinary UPDATE owners end with the target chain,
					// followed by five Err checks: chain completion, two nil
					// query roots, binding completion and carrier completion.
					// Cancel at the preceding bound-writer check, after real
					// syntax/RHS/target bytes, without timing or evaluation.
					counter := &rewriteWalkContext{Context: t.Context(), after: 1<<62}
					if _, err := carrier.StructuralOutput(counter); err != nil { t.Fatal(err) }
					ctx, stop := context.WithCancel(t.Context())
					duringBound := &rewriteWalkContext{Context: ctx, cancel: stop, after: counter.calls.Load()-5}
					text, err := carrier.StructuralOutput(duringBound)
					stop()
					if !errors.Is(err, context.Canceled) || text != "" { t.Fatal("target-bound cancellation published a prefix", text, err) }
					if text, err := carrier.StructuralOutput(t.Context()); err != nil || text != output[phase] { t.Fatal("target-bound cancellation poisoned predecessor", text, err) }
				}
			}
			if selected[0] == selected[1] { t.Fatal("fixture failed to select distinct target-only declarations", selected) }
			enginetest.SessionSQL(t, session, "DROP FUNCTION target_a.pick_index() RESTRICT")
			enginetest.SessionSQL(t, session, "DROP FUNCTION target_b.pick_index() CASCADE")
			original, _ := values[0].Tree()
			if text, err := original.StructuralOutput(t.Context()); err != nil || text != output[0] { t.Fatal("retained target chain re-resolved dropped callable", text, err) }
			for phase := range output {
				witness := fmt.Sprintf("SQLRoutine %d ", selected[phase].ObjectID)
				if !strings.Contains(output[phase], witness) { t.Fatal("action omitted bound target callable identity", witness, output[phase]) }
				role, ordinal := "update", 0
				if kind == "insert" || kind == "cte_insert" || kind == "insert_source_subquery" { role, ordinal = "input", 1 }
				if kind == "conflict" || kind == "cte_conflict" { role = "conflict" }
				association := fmt.Sprintf("assignment_target owner %q ordinal %d attribute 2", role, ordinal)
				if !strings.Contains(output[phase], association) { t.Fatal("destination lost actual owner association", association, output[phase]) }
				if modifier && !strings.Contains(output[phase], "{modifier owner ") { t.Fatal("destination escaped modifier owner", output[phase]) }
				if kind == "cte_merge_update" && !strings.Contains(output[phase], "{arm 0 attribute 2") { t.Fatal("destination escaped MERGE arm owner", output[phase]) }
				if kind == "update_slice" && strings.Count(output[phase], witness) != 2 {
					t.Fatal("slice lower and upper bound roots were not both serialized", output[phase])
				}
			}
		})
	}
}

// Exercise the private writer through its real carrier, borrowing an installed
// chain. Only detached copies enter a private catalog snapshot; the installed
// predecessor and all selected declaration owners remain untouched.
func rewriteAssignmentFieldDistinctions(t *testing.T, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, rule stored.Rule, baseline string) {
	t.Helper()
	action := rule.Parts().Actions[0]
	original := action.Binding().Update.ArrayTargets[0]
	copyPath := func() *plan.ArrayAssignmentTarget {
		var clone func(*plan.ArrayAssignmentTarget) *plan.ArrayAssignmentTarget
		clone = func(path *plan.ArrayAssignmentTarget) *plan.ArrayAssignmentTarget {
			if path == nil { return nil }
			next := *path
			next.Node.Subscripts = append([]plan.BoundArraySubscript(nil), path.Node.Subscripts...)
			next.Next = clone(path.Next)
			return &next
		}
		return clone(original)
	}
	changes := []struct {
		name string
		change func(*plan.ArrayAssignmentTarget)
	}{
		{"container_typmod", func(p *plan.ArrayAssignmentTarget) { p.Container.Typmod++ }},
		{"destination_typmod", func(p *plan.ArrayAssignmentTarget) { p.Destination.Typmod++ }},
		{"container_collation", func(p *plan.ArrayAssignmentTarget) { p.Container.Collation.ID++ }},
		{"destination_collation", func(p *plan.ArrayAssignmentTarget) { p.Destination.Collation.ID++ }},
		{"container_derivation", func(p *plan.ArrayAssignmentTarget) { p.Container.Collation.Derivation++ }},
		{"destination_derivation", func(p *plan.ArrayAssignmentTarget) { p.Destination.Collation.Derivation++ }},
		{"container_type", func(p *plan.ArrayAssignmentTarget) { p.Container.Type = p.Destination.Type }},
		{"destination_type", func(p *plan.ArrayAssignmentTarget) { p.Destination.Type = p.Container.Type }},
	}
	if original.Next != nil {
		changes = append(changes,
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"member", func(p *plan.ArrayAssignmentTarget) { p.Attribute++ }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"next", func(p *plan.ArrayAssignmentTarget) { p.Next = nil }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"nested_container", func(p *plan.ArrayAssignmentTarget) { p.Next.Container.Typmod++ }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"nested_destination", func(p *plan.ArrayAssignmentTarget) { p.Next.Destination.Typmod++ }})
	} else {
		// Slice container and destination have the same type, so type changes
		// above would not distinguish them. Use the genuine bound index type.
		indexType := original.Node.Subscripts[0].Lower.ResultUsage().Type
		changes[6].change = func(p *plan.ArrayAssignmentTarget) { p.Container.Type = indexType }
		changes[7].change = func(p *plan.ArrayAssignmentTarget) { p.Destination.Type = indexType }
		changes = append(changes,
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"bounds_order", func(p *plan.ArrayAssignmentTarget) { s := &p.Node.Subscripts[0]; s.Lower, s.Upper = s.Upper, s.Lower }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"absent_lower", func(p *plan.ArrayAssignmentTarget) { p.Node.Subscripts[0].Lower = nil }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"absent_upper", func(p *plan.ArrayAssignmentTarget) { p.Node.Subscripts[0].Upper = nil }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"slice", func(p *plan.ArrayAssignmentTarget) { p.Node.Slice = false }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"dimension_slice", func(p *plan.ArrayAssignmentTarget) { p.Node.Subscripts[0].Slice = !p.Node.Subscripts[0].Slice }},
			struct { name string; change func(*plan.ArrayAssignmentTarget) }{"dimension", func(p *plan.ArrayAssignmentTarget) { p.Node.Subscripts = append(p.Node.Subscripts, p.Node.Subscripts[0]) }})
	}
	render := func(path *plan.ArrayAssignmentTarget, ordinal int) string {
		t.Helper()
		binding := action.Binding()
		update := *binding.Update
		binding.Targets = append([]int(nil), binding.Targets...)
		update.ArrayTargets = []*plan.ArrayAssignmentTarget{path}
		if ordinal > 0 {
			update.ArrayTargets = []*plan.ArrayAssignmentTarget{nil, path}
			binding.Targets = append([]int{0}, binding.Targets...)
		}
		if ordinal < 0 {
			binding.Targets[0] = 2
		}
		binding.Update = &update
		parts := rule.Parts()
		parts.Actions = []stored.RuleAction{action.With(func(p *stored.RuleActionParts) { p.Binding = binding })}
		list := stored.RuleList{stored.NewRule(parts)}
		copied := *owner
		copied.Rules = list
		state := catalog.CloneState(read.State())
		state.SetDescriptor(owner.Address(), &copied)
		_, tree, err := stored.RewriteTrees(t.Context(), read.WithState(state), &copied, typ, &list[0], nil)
		if err != nil { t.Fatal(err) }
		text, err := tree.Type().Output(t.Context(), tree)
		if err != nil { t.Fatal(err) }
		return text
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			path := copyPath()
			tc.change(path)
			if got := render(path, 0); got == baseline { t.Fatal("private destination projection lost independent field", tc.name) }
		})
	}
	if got := render(copyPath(), 1); got == baseline || !strings.Contains(got, "ordinal 1 attribute 2") {
		t.Fatal("sparse destination ordinal lost its logical root association", got)
	}
	if got := render(copyPath(), -1); got == baseline || !strings.Contains(got, "ordinal 0 attribute 3") {
		t.Fatal("destination lost logical root attribute", got)
	}
	if got := render(copyPath(), 0); got != baseline { t.Fatal("field distinctions mutated original target chain", got, baseline) }
}

func TestPGRewriteAssignmentTargetsUnsupportedMergeInsert(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE target_sink(id integer,a integer[])")
	op, err := session.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	_, err = e.Execute(op, enginetest.Statement(t, "MERGE INTO target_sink USING (VALUES(1,99)) s(id,v) ON target_sink.id=s.id WHEN NOT MATCHED THEN INSERT(id,a[1]) VALUES(s.id,s.v)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "MERGE partial insertion targets are unsupported" {
		t.Fatal("existing native partial MERGE INSERT boundary changed", err)
	}
}

func TestPGRewriteAssignmentDefaults(t *testing.T) {
	for _, kind := range []string{"single", "multi", "null", "absent", "origin", "identity_discard"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE TABLE default_event(id integer)")
			target := "CREATE TABLE default_sink(id integer DEFAULT 17,v integer DEFAULT 101)"
			action := "INSERT INTO default_sink(v,id) VALUES(DEFAULT,7)"
			switch kind {
			case "multi":
				action = "INSERT INTO default_sink(v,id) VALUES(DEFAULT,7),(202,8)"
			case "null":
				action = "INSERT INTO default_sink(v,id) VALUES(NULL,7)"
			case "absent":
				target = "CREATE TABLE default_sink(id integer DEFAULT 17,v integer)"
			case "origin":
				target = "CREATE TABLE default_sink(id integer DEFAULT 17,v integer DEFAULT json_array_length('[1,2]'::json))"
			case "identity_discard":
				target = "CREATE TABLE default_sink(id integer GENERATED BY DEFAULT AS IDENTITY,v integer DEFAULT 101)"
				action = "INSERT INTO default_sink(v,id) OVERRIDING USER VALUE VALUES(99,7)"
			}
			enginetest.SessionSQL(t, session, target)
			enginetest.SessionSQL(t, session, "CREATE RULE r AS ON INSERT TO default_event DO ALSO "+action)
			read := e.CatalogRead()
			owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "default_event").Address)
			binding := owner.Rules.(stored.RuleList)[0].Parts().Actions[0].Binding()
			if len(binding.Targets) != 2 || binding.Targets[0] != 1 || binding.Targets[1] != 0 {
				t.Fatal("fixture lost real reordered destination mapping", binding.Targets)
			}
			before := binding.Expressions
			producer, err := view.MakeRuleProducer(read, binding, "INSERT")
			if err != nil { t.Fatal(err) }
			for _, value := range producer.New {
				if value == nil { t.Fatal("DEFAULT became a nil executable NEW recipe") }
			}
			switch kind {
			case "single":
				if before[0][0] != nil || !analyze.SameGroupExpression(producer.New[1], binding.Insert.Defaults[1]) {
					t.Fatal("single DEFAULT lost its genuine default owner")
				}
			case "multi":
				values, ok := producer.Root.(*plan.ValuesNode)
				if !ok || values.Rows[0][0] == nil || !analyze.SameGroupExpression(values.Rows[0][0], binding.Insert.Defaults[1]) ||
					!analyze.SameGroupExpression(values.Rows[1][0], before[1][0]) || before[0][0] != nil {
					t.Fatal("multirow DEFAULT failed copy-on-change owner mapping", producer.Root)
				}
				if &values.Rows[1][0] != &before[1][0] {
					t.Fatal("unchanged supplied row was copied")
				}
			case "null":
				value, ok := producer.New[1].(plan.LiteralExpr)
				if !ok || !value.Value.IsNull() || !analyze.SameGroupExpression(producer.New[1], before[0][0]) {
					t.Fatal("explicit SQL NULL was replaced by default", producer.New[1])
				}
			case "identity_discard":
				if !binding.Insert.IdentityDiscard[0] || binding.Insert.Defaults[0] == nil ||
					!analyze.SameGroupExpression(producer.New[0], binding.Insert.Defaults[0]) {
					t.Fatal("identity-discard input replaced its real default demand", producer.New[0])
				}
			}
			// MakeRuleProducer(action) alone does not exercise action-side
			// DEFAULT consumption. Inspect actual composition, then execute it.
			rule := owner.Rules.(stored.RuleList)[0]
			eventRef := enginetest.CatalogRef(t, e, read, "default_event")
			op, err := session.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			input, _ := session.Settings.Presentation()
			e.Mu.Lock()
			auth, describeErr := e.AuthorizationLocked(op)
			var composed analyze.Binding
			if describeErr == nil {
				var event analyze.Binding
				event, describeErr = core.Describe(t.Context(), e.AnalysisHost(op), enginetest.Statement(t, "INSERT INTO default_event VALUES(1)"), auth, eventRef, input, nil, nil)
				if describeErr == nil {
					composed, describeErr = view.ComposeRuleAction(auth, event, "INSERT", rule, rule.Parts().Actions[0])
				}
			}
			e.Mu.Unlock()
			op.Done()
			if describeErr != nil { t.Fatal(describeErr) }
			project := composed.Query.Root.(*plan.ProjectNode)
			for ordinal, value := range project.Expressions {
				if value == nil || value.ResultUsage() != project.Schema()[ordinal].Usage {
					t.Fatal("normalized action published nil/mistyped output", ordinal, value)
				}
			}
			var actionValues *plan.ValuesNode
			plan.VisitCTEQuery(project, func(node plan.Node) {
				if values, ok := node.(*plan.ValuesNode); ok {
					actionValues = values
					for _, row := range values.Rows {
						for ordinal, value := range row {
							if value == nil || value.ResultUsage() != values.Schema()[ordinal].Usage { t.Fatal("normalized action retained nil/mistyped VALUES cell", ordinal, value) }
						}
					}
				}
			}, func(expr.Expr) {}, true)
			if kind == "multi" {
				if actionValues == nil || &actionValues.Rows[1][0] != &before[1][0] || before[0][0] != nil {
					t.Fatal("action normalization copied a supplied row or changed captured DEFAULT", actionValues)
				}
			}
			if kind == "single" || kind == "multi" || kind == "origin" {
				c := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: read, FoldOrigin: exec.FoldOrigin{Owner: eventRef, Attribute: 42}}}
				if kind == "origin" {
					// A genuine stored builtin default must borrow its own
					// manifest, not this deliberately unrelated caller manifest.
					c.RoutineRecipe = true
					c.RoutineReferences = []ids.ObjectRef{eventRef}
					c.RoutineAuthority = &auth
					unowned := c
					if _, _, err := exec.PreflightCoercionsWithPolicy(&unowned, binding.Insert.Defaults[1], true); err == nil {
						t.Fatal("origin fixture accepted unrelated caller manifest")
					}
				}
				caller := c.FoldOrigin
				prepared, _, err := view.PreflightRuleInsertDefaults(&c, composed, project)
				if err != nil { t.Fatal(err) }
				var defaultCell expr.Expr
				plan.VisitCTEQuery(prepared.Query.Root, func(node plan.Node) {
					switch n := node.(type) {
					case *plan.ProjectNode:
						if kind != "multi" { defaultCell = n.Expressions[0] }
					case *plan.ValuesNode:
						if kind == "multi" { defaultCell = n.Rows[0][0] }
					}
				}, func(expr.Expr) {}, true)
				barrier, ok := defaultCell.(expr.Composite)
				if !ok || len(barrier.Children()) != 0 || defaultCell.(expr.Folding).PlanningFold() != expr.FoldNever ||
					defaultCell.(expr.Preflighting).PreflightFold() != expr.PreflightKeep {
					t.Fatal("owned default was exposed to caller-origin folding", defaultCell)
				}
				value, err := defaultCell.Eval(&c)
				if err != nil { t.Fatal(err) }
				text, err := value.Type().Output(t.Context(), value)
				wantText := "101"
				if kind == "origin" { wantText = "2" }
				if err != nil || text != wantText || c.FoldOrigin != caller || c.RoutineConsumer != nil || c.RoutineRecipe != (kind == "origin") {
					t.Fatal("default origin or caller restoration lost", text, err, c.FoldOrigin)
				}
				if kind == "origin" && (len(c.RoutineReferences) != 1 || c.RoutineReferences[0] != eventRef) {
					t.Fatal("default leaked its declaration manifest into caller")
				}
			}
			if result := enginetest.SessionSQL(t, session, "INSERT INTO default_event VALUES(1)"); result.Count != 1 {
				t.Fatal("action rowcount replaced event count", result.Count)
			}
			if (kind == "single" || kind == "multi" || kind == "absent") && binding.Expressions[0][0] != nil {
				t.Fatal("event execution mutated captured DEFAULT row")
			}
		})
	}
}
