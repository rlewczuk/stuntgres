package enginetests_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
)

func TestGroupDependencyRoutineConstraintReplacement(t *testing.T) {
	for _, kind := range []string{"removed", "deferrable", "not_primary", "attributes", "same_name_new_oid"} {
		t.Run(kind, func(t *testing.T) {
			e, read, d, p := groupDependencyRoutineFixture(t, "SELECT s FROM gd_routine_t GROUP BY id")
			enginetest.Run(t, e, "CREATE FUNCTION gd_routine_default() RETURNS text RETURN 'default'::text")
			enginetest.Run(t, e, "CREATE FUNCTION gd_routine_with_default(x text DEFAULT gd_routine_default()) RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT s FROM gd_routine_t GROUP BY id; END")
			read = e.CatalogRead()
			d, p = groupDependencyRoutine(t, read, "gd_routine_with_default")
			provider, _ := groupDependencyRoutine(t, read, "gd_routine_default")
			defaults := slices.Clone(d.SQLDefaults.Dependencies())
			body, defaultBody := d.SQLBody, d.SQLDefaults
			checkBoundaries := func(t *testing.T, authority catalog.Read, invalid bool) {
				t.Helper()
				e.Mu.Lock()
				auth := enginetest.CatalogAuthorization(e, authority)
				e.Mu.Unlock()
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "group_dependency"})
				t.Run("pg_depend", func(t *testing.T) {
					edges, err := pgcatalog.CollectDepend(t.Context(), authority, runtime)
					if invalid {
						if err == nil || edges != nil {
							t.Error("incomplete projection returned successful/partial dependencies", edges, err)
						}
						return
					}
					if err != nil || !slices.Contains(edges, pgcatalog.DependEdge{Dependent: d.Address(), Referenced: provider.Address(), DepType: 'n'}) {
						t.Fatal("valid projection lost the post-body default edge", err)
					}
				})
				t.Run("routine_usage", func(t *testing.T) {
					source := authority.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoutineRoutineUsageOID})
					produce, ok := pgcatalog.ProducerFor(source.Address().ObjectID)
					if !ok {
						t.Fatal("missing routine usage producer")
					}
					captured, err := catalogrt.CaptureRows(pgcatalog.Input{Context: t.Context(), Catalog: authority, Roles: auth.Roles, Owner: authority.Owner(), Ref: authority.Domain().Ref(authority.Epoch(), source.Address()), Source: source, Runtime: runtime}, produce)
					if captured != nil {
						defer captured.Close()
					}
					if invalid {
						if err == nil || captured != nil {
							t.Error("incomplete projection published routine usage", captured, err)
						}
					} else if err != nil || captured == nil || len(captured.Anonymous) != 1 {
						t.Error("valid default-only routine usage", captured, err)
					}
				})
				t.Run("dump", func(t *testing.T) {
					graph := dump.Graph{}
					err := routine.RoutineDumpEvents(control.Resolver{}, authority, []*catalog.Procedure{d}, graph)
					if invalid {
						if err == nil {
							t.Error("incomplete projection admitted a partial replay graph", graph)
						}
					} else if err != nil || !slices.Contains(graph[dump.Event{OID: d.Address().ObjectID}], dump.Event{OID: provider.Address().ObjectID}) {
						t.Error("valid replay lost default predecessor", graph, err)
					}
				})
				t.Run("drop", func(t *testing.T) {
					stage := e.BeginCatalogStage(authority)
					defer e.DiscardCatalogStage(stage)
					reader := core.StageDropProvider(t.Context(), stage, nil, nil, nil)
					edges, err := view.DropClosure(reader, []ids.ObjectAddress{provider.Address()})
					if invalid {
						if err == nil || edges != nil {
							t.Error("incomplete projection returned successful/partial drop closure", edges, err)
						}
					} else if err != nil || !slices.ContainsFunc(edges, func(edge table.DomainDependency) bool {
						return edge.Address == d.Address() && edge.Target == provider.Address() && edge.Normal
					}) {
						t.Error("matched default target was mistaken for incomplete projection", edges, err)
					}
				})
			}
			t.Run("unchanged", func(t *testing.T) { checkBoundaries(t, read, false) })
			parent := enginetest.PrimaryKeyUnitRelation(t, e, "gd_routine_t")
			constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
			var phases []ids.ObjectAddress
			if !catalog.VisitProcedureCapturedDefinitionDependencies(read, read, d, func(a ids.ObjectAddress) bool {
				phases = append(phases, a)
				return true
			}) || slices.Index(phases, constraint) < 0 || slices.Index(phases, provider.Address()) <= slices.Index(phases, constraint) {
				t.Fatal("valid projection lost body-before-default ordering", phases)
			}
			t.Run("matched_supporting_key", func(t *testing.T) {
				stage := e.BeginCatalogStage(read)
				defer e.DiscardCatalogStage(stage)
				edges, err := view.DropClosure(core.StageDropProvider(t.Context(), stage, nil, nil, nil), []ids.ObjectAddress{constraint})
				if err != nil || !slices.ContainsFunc(edges, func(edge table.DomainDependency) bool {
					return edge.Address == d.Address() && edge.Target == constraint && edge.Normal
				}) {
					t.Error("matched grouping constraint was mistaken for incomplete projection", edges, err)
				}
			})
			t.Run("callback_error", func(t *testing.T) {
				missing := catalog.CloneState(read.State())
				missing.RemoveDescriptor(provider.Address())
				authority := read.WithState(missing)
				if !p.VisitGroupingConstraintDependencies(authority, func(ids.ObjectAddress) bool { return true }) {
					t.Fatal("callback-error control corrupted grouping authority")
				}
				if err := routine.RoutineDumpEvents(control.Resolver{}, authority, []*catalog.Procedure{d}, dump.Graph{}); err == nil {
					t.Error("actual missing default predecessor callback error was discarded")
				}
				e.Mu.Lock()
				auth := enginetest.CatalogAuthorization(e, authority)
				e.Mu.Unlock()
				source := authority.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoRoutineRoutineUsageOID})
				produce, ok := pgcatalog.ProducerFor(source.Address().ObjectID)
				if !ok {
					t.Fatal("missing routine usage producer")
				}
				captured, err := catalogrt.CaptureRows(pgcatalog.Input{Context: t.Context(), Catalog: authority, Roles: auth.Roles, Owner: authority.Owner(), Ref: authority.Domain().Ref(authority.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "group_dependency"})}, produce)
				if captured != nil {
					defer captured.Close()
				}
				if err == nil || captured != nil {
					t.Error("actual missing routine callback error published usage", captured, err)
				}
			})
			before := slices.Clone(p.Dependencies())
			state := catalog.CloneState(read.State())
			replacement := *parent
			replacement.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
			switch kind {
			case "removed":
				replacement.Constraints.Unique = nil
			case "deferrable":
				replacement.Constraints.Unique[0].Deferrable = true
			case "not_primary":
				replacement.Constraints.Unique[0].Primary = false
			case "attributes":
				replacement.Constraints.Unique[0].Attributes = []int32{parent.Attributes[1]}
			case "same_name_new_oid":
				replacement.Constraints.Unique[0].OID++
			}
			replacement.ObjectRevision = parent.Revision() + 1
			state.SetDescriptor(replacement.Address(), &replacement)
			current := catalog.NewRead(read.Owner(), state)
			if err := p.ValidateMetadata(current); err == nil {
				t.Error("metadata retained stale key authority")
			}
			if err := p.ValidateAction(current, 0); err == nil {
				t.Error("action retained stale key authority")
			}
			if err := p.Validate(current); err == nil {
				t.Error("publication retained stale key authority")
			}
			if err := (stored.ProcedureProvider{}).Validate(current, d); err == nil {
				t.Error("provider retained stale key authority")
			}
			if _, err := p.Rebind(current, nil, stored.NewRoutineRemap(current, nil, nil, nil, false)); err == nil {
				t.Error("rebind substituted a replacement key")
			}
			calls := 0
			if p.VisitGroupingConstraintDependencies(current, func(ids.ObjectAddress) bool { calls++; return true }) || calls != 0 {
				t.Fatal("unauthenticated constraint emitted", calls)
			}
			defaultVisited := false
			if catalog.VisitProcedureCapturedDefinitionDependencies(current, current, d, func(a ids.ObjectAddress) bool {
				defaultVisited = defaultVisited || a == provider.Address()
				return true
			}) || defaultVisited {
				t.Error("failed body projection completed or moved defaults ahead of authentication")
			}
			t.Run("corrupt_authority", func(t *testing.T) { checkBoundaries(t, current, true) })
			if d.SQLBody != body || d.SQLDefaults != defaultBody || !slices.Equal(defaults, d.SQLDefaults.Dependencies()) || read.Get(parent.Address()) != parent {
				t.Error("boundary projection mutated retained body/default/catalog authority")
			}
			if err := p.Validate(read); err != nil || !slices.Equal(p.Dependencies(), before) {
				t.Fatal("replacement mutated predecessor", err)
			}
		})
	}
}

func TestGroupDependencyRoutineRemap(t *testing.T) {
	e, read, _, p := groupDependencyRoutineFixture(t, "SELECT s FROM gd_routine_t GROUP BY id; WITH q AS (UPDATE gd_routine_sink SET s=(SELECT s FROM gd_routine_t GROUP BY id) RETURNING s) SELECT s FROM q")
	parent := enginetest.PrimaryKeyUnitRelation(t, e, "gd_routine_t")
	destination := enginetest.PrimaryKeyUnitRelation(t, e, "gd_routine_other")
	oldConstraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
	newConstraint := ids.ObjectAddress{ClassID: 2606, ObjectID: destination.Constraints.Unique[0].OID}
	before := slices.Clone(p.Dependencies())
	oldRoot := p.Action(0).Presentation().Query().Root
	oldAggregate := groupDependencyStoredAggregate(t, oldRoot)
	oldProof := oldAggregate.Dependencies[0]
	oldModifiers := p.Action(1).ModifierPresentations()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{parent.Address(): destination.Address()}
	if _, err := p.Rebind(read, mapping, stored.NewRoutineRemap(read, mapping, nil, nil, false)); err == nil {
		t.Fatal("parent-only mapping accepted old constraint ownership")
	}
	mapping[oldConstraint] = newConstraint
	rebound, err := p.Rebind(read, mapping, stored.NewRoutineRemap(read, mapping, nil, nil, false))
	if err != nil {
		t.Fatal("paired destination remap", err)
	}
	next := rebound.(*stored.RoutineProgram)
	if err := next.ValidateMetadata(read); err != nil {
		t.Fatal("destination metadata", err)
	}
	if err := next.Validate(read); err != nil {
		t.Fatal("destination admission", err)
	}
	if got := groupDependencyRoutineEdges(t, read, next); !slices.Equal(got, []ids.ObjectAddress{newConstraint}) {
		t.Fatal("destination projection", got)
	}
	for _, dep := range next.Dependencies() {
		if dep.Ref.Address.ClassID == 2606 || dep.Ref.Address.ClassID == parent.Address().ClassID && dep.Ref.Address.ObjectID == parent.Address().ObjectID {
			t.Fatal("destination retained old/attached descriptor authority", dep)
		}
	}
	mapped := groupDependencyStoredAggregate(t, next.Action(0).Presentation().Query().Root)
	if mapped == oldAggregate || &mapped.Dependencies[0] == &oldAggregate.Dependencies[0] {
		t.Fatal("remap shares changed proof backing")
	}
	if mapped.Dependencies[0].Parent.Address != destination.Address() || mapped.Dependencies[0].Constraint != newConstraint {
		t.Fatal("remap lost exact destination proof")
	}
	if p.Action(0).Presentation().Query().Root != oldRoot || oldAggregate.Dependencies[0].Parent != oldProof.Parent || oldAggregate.Dependencies[0].Constraint != oldProof.Constraint || !slices.Equal(p.Dependencies(), before) {
		t.Fatal("remap mutated immutable predecessor")
	}
	for statement, presentation := range oldModifiers {
		if p.Action(1).ModifierPresentations()[statement] != presentation || next.Action(1).ModifierPresentations()[statement] == presentation {
			t.Fatal("modifier owner was mutated or not remapped")
		}
	}
	if got := groupDependencyRoutineEdges(t, read, p); !slices.Equal(got, []ids.ObjectAddress{oldConstraint}) {
		t.Fatal("predecessor projection changed", got)
	}
	if err := p.Validate(read); err != nil {
		t.Fatal("predecessor invalidated", err)
	}
}
