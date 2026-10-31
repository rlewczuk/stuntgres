package core_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTriggerCoreWhenProvider(t *testing.T) {
	e, scope, _, relation, trigger := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	helper := triggerUnitProcedure(t, e, scope, "trigger_when_helper", 9500004, boolean, proc.OrdinaryInvocation, "BEGIN RETURN true; END")
	read := e.CatalogRead()
	syntax, err := parser.Parse("SELECT public.trigger_when_helper() AND NEW.v>5")
	if err != nil {
		t.Fatal(err)
	}
	raw := syntax[0].(*parser.Select).Targets[0]
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	var condition expr.Expr
	var query *parser.Select
	var binding analyze.Binding
	if err == nil {
		var h view.Host = viewcmds.ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op}}}
		condition, query, binding, err = h.BindTriggerWhen(auth, &analyze.RuleNamespace{Relation: relation, Ref: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: "UPDATE", Trigger: true}, raw, scope.Input)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := triggerUnitDefinition(t, read, relation, trigger, "captured_helper", stored.TriggerUpdate)
	parts := definition.Parts()
	parts.When = condition
	parts.Binding = &binding
	parts.Presentation, err = stored.FreezeQueryPresentation(read, ids.SessionToken{}, query, binding, false, false, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	references, err := stored.ExpressionDependRefs(scope.Op.Context(), read, condition)
	if err != nil {
		t.Fatal(err)
	}
	parts.References = append(parts.References, references...)
	definition, err = stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	payload := stored.TriggersPayload([]stored.Trigger{definition})
	if err := payload.Validate(read, relation); err != nil {
		t.Fatal("healthy captured helper rejected", err)
	}
	for _, kind := range []string{"missing", "changed_definition", "changed_result", "changed_language"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(read.State())
			changed := *helper
			switch kind {
			case "missing":
				state.RemoveDescriptor(helper.Address())
			case "changed_definition":
				copy := *helper.SQL
				changed.SQL = &copy
				state.SetDescriptor(helper.Address(), &changed)
			case "changed_result":
				changed.Result = ids.OID(sqltype.Int4)
				state.SetDescriptor(helper.Address(), &changed)
			case "changed_language":
				changed.Language = ids.ObjectAddress{}
				state.SetDescriptor(helper.Address(), &changed)
			}
			if err := payload.Validate(catalog.NewRead(read.Owner(), state), relation); err == nil {
				t.Fatal("invalid captured helper admitted", kind)
			}
		})
	}
	for _, kind := range []string{"missing_dependency", "foreign_epoch"} {
		t.Run(kind, func(t *testing.T) {
			p := definition.Parts()
			if kind == "missing_dependency" {
				p.References = nil
				for _, reference := range definition.Parts().References {
					if reference != helper.Address() {
						p.References = append(p.References, reference)
					}
				}
			} else {
				copy := *p.Binding
				copy.Dependencies = append([]catalog.ExpressionDependency(nil), copy.Dependencies...)
				found := false
				for i := range copy.Dependencies {
					if copy.Dependencies[i].Ref.Address == helper.Address() {
						copy.Dependencies[i].Ref.Epoch++
						found = true
					}
				}
				if !found {
					t.Fatal("healthy helper has no execution dependency")
				}
				p.Binding = &copy
			}
			bad, err := stored.NewTrigger(p)
			if err == nil {
				err = stored.TriggersPayload([]stored.Trigger{bad}).Validate(read, relation)
			}
			if err == nil {
				t.Fatal("invalid helper dependency admitted", kind)
			}
		})
	}
	if err := payload.Validate(read, relation); err != nil {
		t.Fatal("negative checks mutated the healthy source", err)
	}
	t.Run("shared_capture_graph", func(t *testing.T) {
		state := catalog.CloneState(read.State())
		destination := catalog.NewRead(read.Owner(), state)
		remap := stored.NewRoutineRemap(destination, nil, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, false)
		if err := routine.RebindRoutineDefinitions(state, remap); err != nil {
			t.Fatal(err)
		}
		owner := *relation
		owner.Triggers = payload
		mapped, err := stored.RemapTriggers(destination, &owner, nil, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, remap.Type, remap.Collation, remap.Procedure)
		if err != nil {
			t.Fatal(err)
		}
		if err := remap.Finish(); err != nil {
			t.Fatal(err)
		}
		captured, err := remap.Procedure(helper)
		if err != nil {
			t.Fatal(err)
		}
		published, ok := destination.Procedure(helper.Address())
		if !ok || !exec.SameRoutineDeclaration(published, captured) || published.SQL != captured.SQL || published.SQLBody != captured.SQLBody || published.SQLDefaults != captured.SQLDefaults {
			t.Fatal("trigger helper declaration and frozen captures lost shared payload identity")
		}
		check := func(label string, expression expr.Expr) bool {
			t.Helper()
			found := false
			plan.WalkExpression(expression, func(node expr.Expr) {
				if call, ok := node.(plan.SQLRoutineExpr); ok && call.Procedure.Address() == helper.Address() {
					found = true
					if call.Procedure != captured || call.RoutineReference != captured {
						t.Fatal(label, "did not retain the exact operation-wide mapped handle")
					}
				}
			})
			return found
		}
		p := stored.TriggersOf(mapped)[0].Parts()
		if !check("WHEN", p.When) {
			t.Fatal("WHEN lost captured helper")
		}
		for label, query := range map[string]*plan.Query{"binding": p.Binding.Query, "presentation": p.Presentation.Query()} {
			found := false
			plan.VisitCTEQuery(query.Root, func(plan.Node) {}, func(node expr.Expr) {
				if check(label, node) {
					found = true
				}
			}, true)
			if !found {
				t.Fatal(label, "lost captured helper")
			}
		}
		if err := mapped.Triggers.Validate(destination, mapped); err != nil {
			t.Fatal(err)
		}
		if err := payload.Validate(read, relation); err != nil {
			t.Fatal("mapping mutated source capture", err)
		}
	})
}
