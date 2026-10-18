package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func ruleNamedDependencyFixture(t *testing.T, modifier, inferred bool) (*Engine, *Session, *relationDescriptor, *relationDescriptor) {
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

func ruleNamedDependencyAction(t *testing.T, event *relationDescriptor, modifier bool) (ruleDefinition, *boundInsertConflict) {
	t.Helper()
	for _, r := range event.rules {
		if r.Name != "r" {
			continue
		}
		b := r.actions[0].binding
		if modifier {
			for _, definition := range b.ctes {
				if definition.mutation != nil && definition.mutation.conflict != nil {
					return r, definition.mutation.conflict
				}
			}
			t.Fatal("analyzed modifying WITH lost conflict body")
		}
		if b.conflict == nil {
			t.Fatal("analyzed action lost conflict")
		}
		return r, b.conflict
	}
	t.Fatal("analyzed rule missing")
	return ruleDefinition{}, nil
}

func TestRuleNamedConstraintDependencies(t *testing.T) {
	for _, modifier := range []bool{false, true} {
		for _, inferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("modifier=%v/inferred=%v", modifier, inferred), func(t *testing.T) {
				e, _, event, parent := ruleNamedDependencyFixture(t, modifier, inferred)
				view := e.CatalogRead()
				r, conflict := ruleNamedDependencyAction(t, event, modifier)
				constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.uniqueConstraints[0].OID}
				backing := parent.uniqueConstraints[0].Index
				want := constraint
				if inferred {
					want = ids.ObjectAddress{}
				}
				if conflict.constraint != want || !inferred && conflict.namedIndex != backing {
					t.Fatal("named constraint/physical arbiter identity lost", conflict.constraint, conflict.namedIndex)
				}
				refs, err := ruleReferences(context.Background(), view, r)
				if err != nil {
					t.Fatal(err)
				}
				if slices.Contains(refs, constraint) == inferred || slices.Contains(refs, backing) {
					t.Fatal("actual collector confused named dependency with ordinary inference/backing index", refs)
				}
				stage := e.BeginCatalogStage(view)
				defer e.DiscardCatalogStage(stage)
				edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{constraint, backing})
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, edge := range edges {
					if edge.address == event.address {
						t.Fatal("constraint dependency retired event relation")
					}
					if edge.address == (ids.ObjectAddress{ClassID: 2618, ObjectID: r.OID}) {
						found = true
						if edge.target != constraint || edge.root != constraint || edge.relation != event || edge.automatic {
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
					before := e.state
					op := alterConstraintOperation(t, session)
					auth, err := op.Authorization()
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
					next, err := notNullCandidate(op, auth.catalog, parent, actions[0], notNullDirect)
					if err != nil {
						t.Fatal(err)
					}
					_, stage, err := e.stageDroppedConstraintIndexes(scope, auth, nil, parent, next, actions, nil)
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
						current := e.stageRead(stage).state.descriptors[event.address].(*relationDescriptor)
						rules := len(current.rules)
						want := 1
						if inferred {
							want = 2
						}
						if rules != want || current.rules[0].Name != "keep_r" || len(stage.descriptors[parent.address].(*relationDescriptor).uniqueConstraints) != 0 {
							t.Fatal("retirement changed unrelated rule or missed provider", current.rules)
						}
						if stage.removals[parent.uniqueConstraints[0].Index].ref.Address != parent.uniqueConstraints[0].Index {
							t.Fatal("constraint retirement lost backing lifecycle")
						}
					}
					if e.state != before || len(event.rules) != 2 || len(parent.uniqueConstraints) != 1 {
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
				id = parent.uniqueConstraints[0].OID
			case "exclusion":
				id = parent.exclusionConstraints[0].OID
			case "check":
				id = parent.checkConstraints[0].OID
			case "not_null":
				id = parent.notNullConstraints[0].OID
			}
			provider := ids.ObjectAddress{ClassID: 2606, ObjectID: id}
			if conflict.constraint != provider || !slices.Contains(r.references, provider) {
				t.Fatal("recognized named kind lost actual captured constraint", conflict.constraint, r.references)
			}
			view := e.CatalogRead()
			if got := dependencyObject(view, provider, nil); got != "constraint provider on table sink" {
				t.Fatal("canonical constraint subject lost current parent metadata", got)
			}
			err := conflict.instantiate().selectArbiters(nil, parent, view)
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
				err = conflict.instantiate().selectArbiters(nil, parent, e.CatalogRead())
				var diagnostic *sqlstate.Error
				if conflict.constraint != provider || !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != "ON CONFLICT DO UPDATE not supported with exclusion constraints" {
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
			oldConstraint := oldConflict.constraint
			unchanged := cloneCatalog(e.state)
			e.mu.Lock()
			err := e.remapDefinitionOIDsLocked(unchanged)
			e.mu.Unlock()
			if err != nil || unchanged.descriptors[event.address] != event || unchanged.descriptors[parent.address] != parent {
				t.Fatal("no-collision import copied unchanged rule/provider owners", err)
			}
			if allocations := testing.AllocsPerRun(10, func() {
				result, err := remapRuleConstraintIdentities(e.CatalogRead(), event, nil)
				if err != nil || result != event {
					t.Fatal("no-change constraint visitor lost descriptor identity", err)
				}
			}); allocations != 0 {
				t.Fatal("no-change constraint visitor allocated", allocations)
			}
			next := cloneCatalog(e.state)
			// Model an imported attached definition colliding with a descriptor
			// identity. The real analyzed rule/provider graphs remain intact;
			// the canonical import remint pass must propagate its chosen OID.
			collision := *alterConstraintDescriptor(t, e, "log")
			collision.address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oldConstraint.ObjectID}
			collision.name = "collision"
			next.descriptors[collision.address] = &collision
			e.mu.Lock()
			err = e.remapDefinitionOIDsLocked(next)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			view := CatalogRead{owner: e, state: next}
			mappedParent := next.descriptors[parent.address].(*relationDescriptor)
			provider := ids.ObjectAddress{ClassID: 2606, ObjectID: mappedParent.uniqueConstraints[0].OID}
			if provider == oldConstraint {
				t.Fatal("actual import collision did not remint constraint")
			}
			mappedEvent := next.descriptors[event.address].(*relationDescriptor)
			mappedRule, mappedConflict := ruleNamedDependencyAction(t, mappedEvent, modifier)
			if mappedConflict.constraint != provider || mappedConflict.namedIndex != mappedParent.uniqueConstraints[0].Index || !slices.Contains(mappedRule.references, provider) || slices.Contains(mappedRule.references, oldConstraint) {
				t.Fatal("canonical import failed constraint/binding/normal-edge cutover", mappedConflict.constraint, mappedRule.references)
			}
			if modifier {
				body := mappedRule.actions[0].binding
				var definition *cteDefinition
				for _, candidate := range body.ctes {
					if candidate.mutation != nil && candidate.mutation.conflict == mappedConflict {
						definition = candidate
					}
				}
				if definition == nil {
					t.Fatal("reminted modifier lost its actual definition owner")
				}
				foundScan := false
				var scan func(LogicalNode)
				scan = func(node LogicalNode) {
					if node == nil {
						return
					}
					if derived, ok := node.(*derivedNode); ok && derived.cte != nil && derived.cte.name == definition.name {
						foundScan = true
						if derived.cte != definition || derived.query != definition.query {
							t.Fatal("constraint remint forked modifier definition/consumer spool")
						}
					}
					left, right := queryInputs(node)
					scan(left)
					scan(right)
				}
				scan(body.query.Root)
				if !foundScan {
					t.Fatal("analyzed modifier consumer scan missing")
				}
			}
			if err := validateRules(view, mappedEvent); err != nil {
				t.Fatal(err)
			}
			refs, err := ruleReferences(context.Background(), view, mappedRule)
			if err != nil || !slices.Contains(refs, provider) || slices.Contains(refs, oldConstraint) {
				t.Fatal("remapped canonical collector retained obsolete identity", refs, err)
			}
			stage := e.BeginCatalogStage(view)
			defer e.DiscardCatalogStage(stage)
			edges, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{provider})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, edge := range edges {
				if edge.address == (ids.ObjectAddress{ClassID: 2618, ObjectID: mappedRule.OID}) {
					found = edge.target == provider && edge.root == provider && !edge.automatic
				}
			}
			if !found {
				t.Fatal("remapped provider lost canonical retirement edge", edges)
			}
			if oldConflict.constraint != oldConstraint || !slices.Contains(oldRule.references, oldConstraint) || parent.uniqueConstraints[0].OID != oldConstraint.ObjectID {
				t.Fatal("import remint mutated analyzed predecessor")
			}
		})
	}
}
