package core_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// SQL supplies every descriptor and recipe. Only the selected original manifest
// is damaged in each negative probe; a current equal OID and the intact action
// are deliberately still available to catch unauthorized provenance repair.
func TestRuleDefaultOriginalManifest(t *testing.T) {
	for _, probe := range []string{"original", "missing_call", "empty", "foreign_domain", "stale_epoch", "revision", "declaration"} {
		t.Run(probe, func(t *testing.T) {
			e := engine.New()
			retired := e.CatalogRead()
			if probe == "stale_epoch" {
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			op, finish := enginetest.RoleOperation(t, e, "postgres")
			defer finish()
			for _, query := range []string{
				"CREATE TABLE public.manifest_source(id integer,n integer DEFAULT json_array_length('[1,2]'::json))",
				"CREATE TABLE public.manifest_sink(id integer,n integer)",
				"CREATE RULE manifest_action AS ON INSERT TO public.manifest_source DO ALSO INSERT INTO public.manifest_sink VALUES(NEW.id,NEW.n)",
			} {
				enginetest.RoleExecute(t, e, op, query)
			}
			ref := enginetest.RoleTableRef(t, e, "manifest_source")
			sourceView := e.CatalogRead()
			source := sourceView.MustRelation(ref.Address)
			attribute := source.Attributes[1]
			definition := source.ColumnMeta.Defaults[attribute]
			originalRefs := slices.Clone(definition.Dependencies)
			call, err := exec.ExpressionRoutineDescriptor(definition.Expression)
			if err != nil || call == nil || call.Name() != "json_array_length" || call.Signature != "114" || call.Result != 23 || call.Revision() == 0 {
				t.Fatal("missing genuine json to INT4 DEFAULT descriptor", call, err)
			}
			if len(call.Body) != 0 {
				t.Fatal("fixture needs a bodyless builtin so body-walk success cannot mask outer-call failure")
			}
			var original ids.ObjectRef
			for _, dependency := range originalRefs {
				if dependency.Address == call.Address() {
					original = dependency
				}
			}
			if original.Domain() != e.Catalog.Domain || original.Epoch != e.CatalogRead().Epoch() || original.Address.ClassID != ids.ProcedureClassID {
				t.Fatal("DEFAULT lacks engine-owned original callable ref", original)
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), call); err != nil {
				t.Fatal("invalid released declaration", err)
			}
			beforeSQL, err := stored.DefaultSQL(context.Background(), e.CatalogRead(), definition.Expression)
			if err != nil {
				t.Fatal(err)
			}
			rules := stored.RulesOf(source)
			if len(rules) != 1 || len(rules[0].Actions()) != 1 {
				t.Fatal("missing actual stored rule")
			}
			action := rules[0].Actions()[0]
			actionDependencies := slices.Clone(action.Binding().Dependencies)
			input, _ := op.Session.Settings.Presentation()
			syntax := enginetest.Statement(t, "INSERT INTO public.manifest_source(id) VALUES(1)")
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			binding, err := core.Describe(context.Background(), e.AnalysisHost(op), syntax, auth, ref, input, nil, nil)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if binding.Relation != source || len(binding.Insert.Defaults) <= 1 || binding.Insert.Defaults[1] == nil {
				t.Fatal("statement does not demand the authentic source DEFAULT")
			}
			bindingDependencies := slices.Clone(binding.Dependencies)
			actor := auth.State
			other := engine.New()
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			changed := *source
			changed.ColumnMeta.Defaults = maps.Clone(source.ColumnMeta.Defaults)
			candidate := definition
			candidate.Dependencies = slices.Clone(originalRefs)
			switch probe {
			case "missing_call":
				candidate.Dependencies = slices.DeleteFunc(candidate.Dependencies, func(ref ids.ObjectRef) bool { return ref.Address == call.Address() })
			case "empty":
				candidate.Dependencies = []ids.ObjectRef{}
			case "foreign_domain":
				for i, dependency := range candidate.Dependencies {
					if dependency.Address == call.Address() {
						candidate.Dependencies[i] = other.Catalog.Domain.Ref(other.CatalogRead().Epoch(), dependency.Address)
					}
				}
			case "stale_epoch":
				// Only this original procedure ref belongs to the real retired
				// lifetime. The current statement and stored action remain valid.
				for i, dependency := range candidate.Dependencies {
					if dependency.Address == call.Address() {
						candidate.Dependencies[i] = e.Catalog.Domain.Ref(retired.Epoch(), dependency.Address)
					}
				}
			case "revision", "declaration":
				current := *call
				revision, reserveErr := e.Catalog.RevisionLocked()
				if reserveErr != nil {
					e.Mu.Unlock()
					t.Fatal(reserveErr)
				}
				current.ObjectRevision = revision
				if probe == "declaration" {
					current.Strict = !current.Strict
				}
				state.SetDescriptor(current.Address(), &current)
			}
			changed.ColumnMeta.Defaults[attribute] = candidate
			state.SetDescriptor(changed.Address(), &changed)
			e.Catalog.PublishLocked(state)
			auth, err = e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			// Select the published containing recipe explicitly. A retained
			// valid source pointer is not a damaged-original witness.
			probeBinding := binding
			probeBinding.Ref = e.Catalog.Domain.Ref(state.Epoch(), ref.Address)
			probeBinding.Relation = &changed
			if auth.Catalog.MustRelation(ref.Address) != &changed {
				e.Mu.Unlock()
				t.Fatal("rewrite authority did not select published DEFAULT recipe")
			}
			if probe == "revision" || probe == "declaration" {
				published, ok := auth.Catalog.Procedure(call.Address())
				if !ok || published == call || published.Revision() == call.Revision() {
					e.Mu.Unlock()
					t.Fatal("rewrite authority did not select changed effective routine revision")
				}
			}
			if probe == "missing_call" || probe == "empty" {
				// Compose consumes the explicitly captured parent recipe, unlike
				// Expand, whose RefreshRuleBinding selects the current relation.
				// Damage to the new descriptor must not corrupt the authentic
				// immutable old recipe when it is actually selected.
				if _, controlErr := view.ComposeRuleAction(auth, binding, "INSERT", rules[0], action); controlErr != nil {
					e.Mu.Unlock()
					t.Fatal("valid captured DEFAULT recipe rejected", controlErr)
				}
			}
			program := view.RuleProgram{Count: -1, Returning: -1}
			_, err = (view.RuleRewriter{Owner: &e.Catalog.Owner}).ExpandLocked(auth, syntax, probeBinding, nil, &program)
			e.Mu.Unlock()
			if probe != "original" {
				if err == nil {
					t.Fatal("graft repaired invalid original DEFAULT provenance", probe)
				}
				if (probe == "missing_call" || probe == "empty") && err.Error() != "stuntgres: missing rule DEFAULT routine manifest" {
					t.Fatal("missing-original provenance fence", err)
				}
				if probe == "revision" && err != (catalog.Conflict{}) {
					t.Fatal("changed effective revision did not reach the original-call fence", err)
				}
				if probe == "declaration" && err.Error() != "stuntgres: stale routine declaration carrier" {
					t.Fatal("changed declaration did not reach the original-call fence", err)
				}
			} else {
				if err != nil {
					t.Fatal("genuine DEFAULT graft rejected", err)
				}
				found := false
				for _, command := range program.Commands {
					if command.Source == view.RuleOriginal {
						continue
					}
					for _, dependency := range command.Binding.Dependencies {
						if dependency.Ref == original && dependency.Revision == call.Revision() {
							found = true
						}
						if len(action.Binding().Dependencies) != 0 && &command.Binding.Dependencies[0] == &action.Binding().Dependencies[0] {
							t.Fatal("DEFAULT loan shared stored action dependency backing")
						}
					}
				}
				if !found {
					t.Fatal("NEW graft did not loan exact original DEFAULT ref/revision")
				}
			}
			if !reflect.DeepEqual(definition.Dependencies, originalRefs) || !reflect.DeepEqual(action.Binding().Dependencies, actionDependencies) || !reflect.DeepEqual(binding.Dependencies, bindingDependencies) || !reflect.DeepEqual(auth.State, actor) {
				t.Fatal("rewrite mutated source, stored action, statement dependencies or actor")
			}
			afterSQL, renderErr := stored.DefaultSQL(context.Background(), sourceView, definition.Expression)
			// Render against the captured source view, not the deliberately
			// incompatible current declaration/lifetime used by the probe.
			if renderErr != nil || afterSQL != beforeSQL {
				t.Fatal("rewrite mutated original expression graph", afterSQL, renderErr)
			}
		})
	}
}
