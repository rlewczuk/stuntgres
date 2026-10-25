package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func ruleNamedDependencyFixture(t *testing.T, modifier, inferred bool) (*core.Engine, *session.Session, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE src(id INT)", "CREATE TABLE sink(id INT CONSTRAINT sink_key UNIQUE)", "CREATE TABLE log(id INT)", "CREATE RULE keep_r AS ON DELETE TO src DO ALSO NOTHING"} {
		sessionSQL(t, s, q)
	}
	conflict := "ON CONFLICT ON CONSTRAINT sink_key DO NOTHING"
	if inferred {
		conflict = "ON CONFLICT(id) DO NOTHING"
	}
	action := "INSERT INTO sink VALUES(NEW.id) " + conflict
	if modifier {
		action = "WITH moved AS (INSERT INTO sink VALUES(7) " + conflict + " RETURNING id) INSERT INTO log SELECT NEW.id FROM moved"
	}
	sessionSQL(t, s, "CREATE RULE r AS ON INSERT TO src DO ALSO "+action)
	return e, s, alterConstraintDescriptor(t, e, "src"), alterConstraintDescriptor(t, e, "sink")
}

func ruleNamedDependencyAction(t *testing.T, event *catalog.Relation, modifier bool) (stored.Rule, *analyze.InsertConflict) {
	t.Helper()
	for _, r := range stored.RulesOf(event) {
		if r.Name != "r" {
			continue
		}
		b := r.Actions()[0].Binding()
		if modifier {
			for _, definition := range b.CTEs {
				if definition.Mutation != nil && cteMutationOf(definition).Insert.Conflict != nil {
					return r, cteMutationOf(definition).Insert.Conflict
				}
			}
			t.Fatal("analyzed modifying WITH lost conflict body")
		}
		if b.Insert.Conflict == nil {
			t.Fatal("analyzed action lost conflict")
		}
		return r, b.Insert.Conflict
	}
	t.Fatal("analyzed rule missing")
	return stored.Rule{}, nil
}

func TestRuleNamedConstraintDependencies(t *testing.T) {
	for _, modifier := range []bool{false, true} {
		for _, inferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("modifier=%v/inferred=%v", modifier, inferred), func(t *testing.T) {
				e, _, event, parent := ruleNamedDependencyFixture(t, modifier, inferred)
				catalogView := e.CatalogRead()
				r, conflict := ruleNamedDependencyAction(t, event, modifier)
				constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
				backing := parent.Constraints.Unique[0].Index
				want := constraint
				if inferred {
					want = ids.ObjectAddress{}
				}
				if conflict.Constraint != want || !inferred && conflict.NamedIndex != backing {
					t.Fatal("named constraint/physical arbiter identity lost", conflict.Constraint, conflict.NamedIndex)
				}
				refs, err := view.RuleReferences(context.Background(), catalogView, r)
				if err != nil {
					t.Fatal(err)
				}
				if slices.Contains(refs, constraint) == inferred || slices.Contains(refs, backing) {
					t.Fatal("actual collector confused named dependency with ordinary inference/backing index", refs)
				}
				stage := e.BeginCatalogStage(catalogView)
				defer e.DiscardCatalogStage(stage)
				edges, err := core.DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{constraint, backing})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, edge := range edges {
					if edge.Address == event.Address() {
						t.Fatal("constraint dependency retired event relation")
					}
					if edge.Address == (ids.ObjectAddress{ClassID: 2618, ObjectID: r.OID}) {
						found = true
						if edge.Target != constraint || edge.Root != constraint || edge.Relation != event || edge.Automatic {
							t.Fatal("constraint edge lost normal rule-owned authority", edge)
						}
					}
				}
				if found == inferred {
					t.Fatal("central closure confused explicit constraint with replaceable inferred index", edges)
				}
			})
		}
	}
}

func TestRuleNamedConstraintRetirement(t *testing.T) {
	for _, modifier := range []bool{false, true} {
		for _, inferred := range []bool{false, true} {
			for _, cascade := range []bool{false, true} {
				t.Run(fmt.Sprintf("modifier=%v/inferred=%v/cascade=%v", modifier, inferred, cascade), func(t *testing.T) {
					e, session, event, parent := ruleNamedDependencyFixture(t, modifier, inferred)
					before := e.Catalog.State
					op := alterConstraintOperation(t, session)
					auth, err := core.OperationAuthorization(op)
					if err != nil {
						t.Fatal(err)
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(context.Canceled)
					mode := "RESTRICT"
					if cascade {
						mode = "CASCADE"
					}
					actions := statement(t, "ALTER TABLE sink DROP CONSTRAINT sink_key "+mode).(*parser.AlterTable).Actions
					next, err := table.NotNullCandidate(op, auth.Catalog, parent, actions[0], table.NotNullDirect)
					if err != nil {
						t.Fatal(err)
					}
					_, stage, err := stageDroppedConstraintIndexes(e, scope, auth, nil, parent, next, actions, nil)
					defer e.DiscardCatalogStage(stage)
					if !inferred && !cascade {
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop constraint sink_key on table sink because other objects depend on it" || diagnostic.Detail != "rule r on table src depends on constraint sink_key on table sink" || diagnostic.Hint != "Use DROP ... CASCADE to drop the dependent objects too." {
							t.Fatal("canonical retirement missed constraint RESTRICT", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						current := e.StageRead(stage).MustRelation(event.Address())
						rules := len(stored.RulesOf(current))
						want := 1
						if inferred {
							want = 2
						}
						if rules != want || stored.RulesOf(current)[0].Name != "keep_r" || len(stage.Get(parent.Address()).(*catalog.Relation).Constraints.Unique) != 0 {
							t.Fatal("retirement changed unrelated rule or missed provider", stored.RulesOf(current))
						}
						removal, removed := stage.Removal(parent.Constraints.Unique[0].Index)
						if !removed || removal.Ref.Address != parent.Constraints.Unique[0].Index {
							t.Fatal("constraint retirement lost backing lifecycle")
						}
					}
					if e.Catalog.State != before || len(stored.RulesOf(event)) != 2 || len(parent.Constraints.Unique) != 1 {
						t.Fatal("private retirement mutated published catalog")
					}
				})
			}
		}
	}
}

func TestRuleNamedConstraintKinds(t *testing.T) {
	for _, kind := range []string{"unique", "exclusion", "check", "not_null"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			declaration := map[string]string{
				"unique":    "id INT CONSTRAINT provider UNIQUE",
				"exclusion": "id INT,CONSTRAINT provider EXCLUDE(id WITH =)",
				"check":     "id INT CONSTRAINT provider CHECK(id>0)",
				"not_null":  "id INT CONSTRAINT provider NOT NULL",
			}[kind]
			sessionSQL(t, s, "CREATE TABLE src(id INT)")
			sessionSQL(t, s, "CREATE TABLE sink("+declaration+")")
			sessionSQL(t, s, "CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id) ON CONFLICT ON CONSTRAINT provider DO NOTHING")
			event := alterConstraintDescriptor(t, e, "src")
			parent := alterConstraintDescriptor(t, e, "sink")
			r, conflict := ruleNamedDependencyAction(t, event, false)
			var id ids.OID
			switch kind {
			case "unique":
				id = parent.Constraints.Unique[0].OID
			case "exclusion":
				id = parent.Constraints.Exclusion[0].OID
			case "check":
				id = parent.Constraints.Check[0].OID
			case "not_null":
				id = parent.Constraints.NotNull[0].OID
			}
			provider := ids.ObjectAddress{ClassID: 2606, ObjectID: id}
			if conflict.Constraint != provider || !slices.Contains(r.References(), provider) {
				t.Fatal("recognized named kind lost actual captured constraint", conflict.Constraint, r.References())
			}
			view := e.CatalogRead()
			if got := stored.DependencyObject(view, provider, nil); got != "constraint provider on table sink" {
				t.Fatal("canonical constraint subject lost current parent metadata", got)
			}
			err := dml.NewInsertConflictState(conflict.Clone()).SelectArbiters(nil, parent, view)
			if kind == "check" || kind == "not_null" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != "constraint in ON CONFLICT clause has no associated index" {
					t.Fatal("identity cutover changed existing no-index diagnostic", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if kind == "exclusion" {
				sessionSQL(t, s, "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id) ON CONFLICT ON CONSTRAINT provider DO UPDATE SET id=EXCLUDED.id")
				event = alterConstraintDescriptor(t, e, "src")
				_, conflict = ruleNamedDependencyAction(t, event, false)
				err = dml.NewInsertConflictState(conflict.Clone()).SelectArbiters(nil, parent, e.CatalogRead())
				var diagnostic *sqlstate.Error
				if conflict.Constraint != provider || !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != "ON CONFLICT DO UPDATE not supported with exclusion constraints" {
					t.Fatal("identity cutover changed existing exclusion UPDATE route", err)
				}
			}
		})
	}
}

func TestRuleNamedConstraintRemap(t *testing.T) {
	for _, modifier := range []bool{false, true} {
		t.Run(fmt.Sprintf("modifier=%v", modifier), func(t *testing.T) {
			e, _, event, parent := ruleNamedDependencyFixture(t, modifier, false)
			oldRule, oldConflict := ruleNamedDependencyAction(t, event, modifier)
			oldConstraint := oldConflict.Constraint
			unchanged := catalog.CloneState(e.Catalog.State)
			e.Mu.Lock()
			err := control.RemapDefinitionOIDs(e.Catalog.Read(unchanged), e.Catalog.AllocateOIDLocked)
			e.Mu.Unlock()
			if err != nil || unchanged.Get(event.Address()) != event || unchanged.Get(parent.Address()) != parent {
				t.Fatal("no-collision import copied unchanged rule/provider owners", err)
			}
			if allocations := testing.AllocsPerRun(10, func() {
				result, err := stored.RemapRuleConstraintIdentities(e.CatalogRead(), event, nil)
				if err != nil || result != event {
					t.Fatal("no-change constraint visitor lost descriptor identity", err)
				}
			}); allocations != 0 {
				t.Fatal("no-change constraint visitor allocated", allocations)
			}
			next := catalog.CloneState(e.Catalog.State)
			// Model an imported attached definition colliding with a descriptor
			// identity. The real analyzed rule/provider graphs remain intact;
			// the canonical import remint pass must propagate its chosen OID.
			collision := *alterConstraintDescriptor(t, e, "log")
			collision.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oldConstraint.ObjectID}
			collision.ObjectName = "collision"
			next.SetDescriptor(collision.Address(), &collision)
			e.Mu.Lock()
			err = control.RemapDefinitionOIDs(e.Catalog.Read(next), e.Catalog.AllocateOIDLocked)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			catalogView := e.Catalog.Read(next)
			mappedParent := next.MustRelation(parent.Address())
			provider := ids.ObjectAddress{ClassID: 2606, ObjectID: mappedParent.Constraints.Unique[0].OID}
			if provider == oldConstraint {
				t.Fatal("actual import collision did not remint constraint")
			}
			mappedEvent := next.MustRelation(event.Address())
			mappedRule, mappedConflict := ruleNamedDependencyAction(t, mappedEvent, modifier)
			if mappedConflict.Constraint != provider || mappedConflict.NamedIndex != mappedParent.Constraints.Unique[0].Index || !slices.Contains(mappedRule.References(), provider) || slices.Contains(mappedRule.References(), oldConstraint) {
				t.Fatal("canonical import failed constraint/binding/normal-edge cutover", mappedConflict.Constraint, mappedRule.References())
			}
			if modifier {
				body := mappedRule.Actions()[0].Binding()
				var definition *plan.CTEDefinition
				for _, candidate := range body.CTEs {
					if candidate.Mutation != nil && cteMutationOf(candidate).Insert.Conflict == mappedConflict {
						definition = candidate
					}
				}
				if definition == nil {
					t.Fatal("reminted modifier lost its actual definition owner")
				}
				foundScan := false
				var scan func(plan.Node)
				scan = func(node plan.Node) {
					if node == nil {
						return
					}
					if derived, ok := node.(*plan.DerivedNode); ok && derived.CTE != nil && derived.CTE.Name == definition.Name {
						foundScan = true
						if derived.CTE != definition || derived.Query != definition.Query {
							t.Fatal("constraint remint forked modifier definition/consumer spool")
						}
					}
					left, right := plan.QueryInputs(node)
					scan(left)
					scan(right)
				}
				scan(body.Query.Root)
				if !foundScan {
					t.Fatal("analyzed modifier consumer scan missing")
				}
			}
			if err := stored.ValidateRules(catalogView, mappedEvent); err != nil {
				t.Fatal(err)
			}
			refs, err := view.RuleReferences(context.Background(), catalogView, mappedRule)
			if err != nil || !slices.Contains(refs, provider) || slices.Contains(refs, oldConstraint) {
				t.Fatal("remapped canonical collector retained obsolete identity", refs, err)
			}
			stage := e.BeginCatalogStage(catalogView)
			defer e.DiscardCatalogStage(stage)
			edges, err := core.DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{provider})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, edge := range edges {
				if edge.Address == (ids.ObjectAddress{ClassID: 2618, ObjectID: mappedRule.OID}) {
					found = edge.Target == provider && edge.Root == provider && !edge.Automatic
				}
			}
			if !found {
				t.Fatal("remapped provider lost canonical retirement edge", edges)
			}
			if oldConflict.Constraint != oldConstraint || !slices.Contains(oldRule.References(), oldConstraint) || parent.Constraints.Unique[0].OID != oldConstraint.ObjectID {
				t.Fatal("import remint mutated analyzed predecessor")
			}
		})
	}
}
