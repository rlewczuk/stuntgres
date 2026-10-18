package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Public answers live in reference; these exercise genuine original authority
// and COW catalog seams with independently fixed released identities.
func TestIntaggRoutineOriginalAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE TABLE strict_null_rows(v integer)")
	roleExecute(t, e, op, "CREATE SEQUENCE strict_null_steps START 1")
	roleExecute(t, e, op, "CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)")
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "intagg", ""))
	other, otherOp, otherScope := strictNullUnitFixture(t)
	extensionUnitPublish(t, other, otherOp, intarrayUnitStage(t, other, otherOp, "intagg", ""))
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	// The real stored consumer supplies graft ancestry, not a fabricated recipe.
	roleExecute(t, e, op, "CREATE VIEW intagg_authority_recipe AS SELECT int_array_enum('{1,NULL,2}'::integer[])")
	recipeRef := roleTableRef(t, e, "intagg_authority_recipe")
	recipe := e.CatalogRead().state.descriptors[recipeRef.Address].(*relationDescriptor)
	// Complete catalog setup before retaining statement-owner admission. Calling
	// Engine.Execute with this operation while scope is held would reenter it.
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	for _, tc := range []struct {
		name, text, signature string
		kind                  routineKind
		result                ids.OID
		strict                bool
	}{
		{"aggregate", "int_array_aggregate(v)", "23", aggregateRoutine, 1007, false},
		{"enum", "int_array_enum('{1,NULL,2}'::integer[])", "1007", functionRoutine, 23, true},
		{"enum_null", "int_array_enum(NULL::integer[])", "1007", functionRoutine, 23, true},
		{"enum_parameter", "int_array_enum($1::integer[])", "1007", functionRoutine, 23, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.text)
			var captured *procedureDescriptor
			var ref ids.ObjectRef
			aggregate := tc.kind == aggregateRoutine
			if aggregate {
				if len(binder.aggregates) != 1 {
					t.Fatal("missing actual aggregate")
				}
				call := binder.aggregates[0]
				captured, ref = call.procedure, call.routine
				if ref != call.ref || ref == (ids.ObjectRef{}) {
					t.Fatal("aggregate lost original compact authority")
				}
				if carrier, err := expressionRoutineDescriptor(node); err != nil || carrier != nil {
					t.Fatal("aggregate acquired scalar admission carrier", err)
				}
			} else {
				enum, ok := node.(unnestExpression)
				if !ok || !validFunctionSourceCall(enum) || !binder.queryHasTargetSet {
					t.Fatal("lost genuine set carrier")
				}
				captured, ref = enum.procedure, enum.ref
				carrier, err := expressionRoutineDescriptor(node)
				if err != nil || carrier != captured || !captured.returnsSet() || strictScalarRoutine(captured) {
					t.Fatal("enum selected declaration/set classification", err)
				}
				if tc.name == "enum_parameter" {
					evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.parameters[1].Type)}
					folded, changed, err := foldStrictParameterNull(&evaluation, node)
					if err != nil || changed || !reflect.DeepEqual(folded, node) {
						t.Fatal("NULL enum folded into scalar", changed, err)
					}
				}
				if tc.name == "enum_null" || tc.name == "enum_parameter" {
					cursor, err := openFunctionCall(&evaluation, enum)
					if err != nil {
						t.Fatal(err)
					}
					_, present, err := cursor.next(evaluation.Context, enum.usage.Type)
					cursor.close()
					if err != nil || present {
						t.Fatal("NULL enum is not an empty set", present, err)
					}
				}
			}
			expectedName := "int_array_enum"
			if aggregate {
				expectedName = "int_array_aggregate"
			}
			ext := evaluation.catalog.state.descriptors[captured.extension].(*extensionDescriptor)
			if captured.name != expectedName || captured.signature != procedureSignature(tc.signature) || captured.routineKind != tc.kind || captured.result != tc.result || captured.strict != tc.strict || captured.volatility != Immutable || captured.intagg != stockIntagg || captured.builtin != nil || captured.revision == 0 || len(captured.body) != 0 || ext.name != "intagg" || ext.version != "1.1" || len(ext.members) != 2 || captured.namespace != ext.targetNamespace || evaluation.catalog.state.descriptors[ref.Address] != captured || ref.Domain() != e.domain || ref.Epoch != evaluation.catalog.Epoch() {
				t.Fatal("released full identity/original installation", captured, ref)
			}
			original := append([]expressionDependency(nil), binder.dependencies...)
			found := false
			for _, dep := range original {
				if dep.ref.Address == ref.Address {
					found = true
					if dep.ref != ref || dep.revision != captured.revision {
						t.Fatal("original ref/revision mismatch")
					}
				} else if dep.ref.Address.ClassID == ProcedureClassID {
					t.Fatal("invented core/helper dependency", dep)
				}
			}
			if !found {
				t.Fatal("missing original manifest")
			}
			admit := func(c *EvalContext, n BoundExpr, r ids.ObjectRef) error {
				if aggregate {
					return admitRoutineReference(c, r, "aggregate")
				}
				return admitRoutineCall(c, n, false, false)
			}
			if err := admit(&evaluation, node, ref); err != nil {
				t.Fatal("original admission", err)
			}
			if aggregate {
				if err := admitRoutineReference(&evaluation, ids.ObjectRef{}, "aggregate"); err == nil {
					t.Fatal("missing aggregate identity admitted")
				}
			}
			before := node
			if enum, ok := node.(unnestExpression); ok {
				enum.args = append([]BoundExpr(nil), enum.args...)
				before = enum
			}
			for _, mode := range []string{"nil", "empty", "missing", "epoch", "revision", "foreign_ref"} {
				probe := evaluation
				deps := append([]expressionDependency(nil), original...)
				switch mode {
				case "nil":
					deps = nil
				case "empty":
					deps = []expressionDependency{}
				case "missing":
					deps = deps[:0]
					for _, dep := range original {
						if dep.ref.Address != ref.Address {
							deps = append(deps, dep)
						}
					}
				default:
					for i := range deps {
						if deps[i].ref.Address == ref.Address {
							switch mode {
							case "epoch":
								deps[i].ref.Epoch++
							case "revision":
								deps[i].revision++
							case "foreign_ref":
								deps[i].ref = other.domain.Ref(deps[i].ref.Epoch, deps[i].ref.Address)
							}
						}
					}
				}
				probe.routineChecks = deps
				if err := admit(&probe, node, ref); err == nil {
					t.Fatal("repaired absent/stale original authority", mode)
				}
			}
			probe := evaluation
			probe.routineAuthority = foreign.routineAuthority
			if err := admit(&probe, node, ref); err == nil {
				t.Fatal("foreign engine became original authority")
			}
			for _, mutate := range []func(*procedureDescriptor){
				func(d *procedureDescriptor) { d.intagg = nil },
				func(d *procedureDescriptor) { d.intagg = &intaggProvider{version: "1.1"} },
				func(d *procedureDescriptor) { d.routineKind = procedureRoutine },
			} {
				auth := *evaluation.routineAuthority
				auth.catalog.state = cloneCatalog(auth.catalog.state)
				bad := *captured
				mutate(&bad)
				auth.catalog.state.descriptors[ref.Address] = &bad
				if _, err := validateRoutineDescriptor(auth.catalog, auth.roles, captured); err == nil {
					t.Fatal("incompatible provider/kind declaration accepted")
				}
				if !aggregate {
					probe = evaluation
					probe.routineAuthority = &auth
					if err := admit(&probe, node, ref); err == nil {
						t.Fatal("enum admitted incompatible current declaration")
					}
				}
			}
			if !aggregate {
				grafted, _, err := mapBoundExpression(evaluation.catalog, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, recipe, nil)
				if err != nil {
					t.Fatal("graft real selected enum", err)
				}
				graftProbe := evaluation
				graftProbe.routineChecks = nil
				if err := admitRoutineCall(&graftProbe, grafted, false, false); err != nil {
					t.Fatal("graft did not select actual owning manifest", err)
				}
				if boundExpressionInfo(grafted).routineRecipe != recipe || boundExpressionInfo(node).routineRecipe != nil {
					t.Fatal("graft ancestry lost or mutated source")
				}
				if tc.name == "enum_parameter" {
					if _, err := remapBoundExpression(node, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil); err == nil || !strings.Contains(err.Error(), "unpersistable scalar expression engine.parameterExpression") {
						t.Fatal("parameter persistence boundary changed", err)
					}
					rewritten, _, err := mapBoundExpression(evaluation.catalog, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, nil)
					if err != nil || !reflect.DeepEqual(rewritten, node) {
						t.Fatal("structural parameter rewrite lost original carrier", err)
					}
					if err := admitRoutineCall(&evaluation, rewritten, false, false); err != nil {
						t.Fatal("structural parameter rewrite lost original authority", err)
					}
					foreignEnum := node.(unnestExpression)
					foreignEnum.ref = other.domain.Ref(foreignEnum.ref.Epoch, foreignEnum.ref.Address)
					foreignEnum.ref.Epoch = foreign.catalog.Epoch()
					foreignNode, _, err := retagBoundTypes(foreign.catalog, foreignEnum, func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == nil {
							return nil
						}
						current, _ := foreign.catalog.LookupType(d.ID())
						return current
					})
					if err != nil {
						t.Fatal("structural foreign parameter retag", err)
					}
					probe = evaluation
					probe.routineAuthority = foreign.routineAuthority
					if err := admitRoutineCall(&probe, foreignNode, false, false); err == nil {
						t.Fatal("foreign parameter retag acquired original authority")
					}
				} else {
					text, err := (pgRenderer{view: evaluation.catalog}).sql(context.Background(), node, pgRenderParent{})
					if err != nil {
						t.Fatal("literal enum rendering", err)
					}
					rebound, _, reboundEvaluation := strictNullUnitBind(t, e, op, scope, text)
					reboundDescriptor, err := expressionRoutineDescriptor(rebound)
					if err != nil || reboundDescriptor != captured {
						t.Fatal("literal render/rebind changed selected declaration", err)
					}
					if err := admitRoutineCall(&reboundEvaluation, rebound, false, false); err != nil {
						t.Fatal("literal render/rebind authority", err)
					}
					// Transfer the actual selected ref before descriptor retagging; no old OID
					// can resolve in this relocated catalog.
					mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.address: {ExtensionClassID, 900000, 0}}
					for i, a := range ext.members {
						mapping[a] = ids.ObjectAddress{ProcedureClassID, ids.OID(900001 + i), 0}
					}
					state := cloneCatalog(evaluation.catalog.state)
					for a := range mapping {
						delete(state.names, descriptorNameKey(state.descriptors[a]))
						delete(state.descriptors, a)
					}
					for a, dest := range mapping {
						d := remapExtensionDescriptor(evaluation.catalog.state.descriptors[a], mapping)
						state.descriptors[dest] = d
						state.names[descriptorNameKey(d)] = dest
					}
					state.epoch++
					view := CatalogRead{owner: e, state: state}
					remap := func(r ids.ObjectRef) ids.ObjectRef {
						r.Epoch = state.epoch
						if a, ok := mapping[r.Address]; ok {
							r.Address = a
						}
						return r
					}
					relocated, err := remapBoundExpression(node, remap, nil)
					if err != nil {
						t.Fatal(err)
					}
					relocated, _, err = retagBoundTypes(view, relocated, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
					if err != nil {
						t.Fatal("remapped selected-ref retag", err)
					}
					current, err := expressionRoutineDescriptor(relocated)
					if err != nil || current != state.descriptors[mapping[ref.Address]] {
						t.Fatal("retag resolved stale captured address", err)
					}
					// Use the existing private catalog-authority constructor: the paired
					// catalog and role read must both belong to this relocated root.
					auth := catalogAuthorization(e, view)
					probe = evaluation
					probe.catalog = view
					probe.routineAuthority = &auth
					if err := admitRoutineCall(&probe, relocated, false, false); err == nil {
						t.Fatal("retag manufactured original manifest")
					}
					deps := append([]expressionDependency(nil), original...)
					for i := range deps {
						deps[i].ref = remap(deps[i].ref)
					}
					probe.routineChecks = deps
					if err := admitRoutineCall(&probe, relocated, false, false); err != nil {
						t.Fatal("transferred original authority", err)
					}
					foreignNode, err := remapBoundExpression(node, func(r ids.ObjectRef) ids.ObjectRef {
						r = other.domain.Ref(r.Epoch, r.Address)
						r.Epoch = foreign.catalog.Epoch()
						return r
					}, nil)
					if err != nil {
						t.Fatal(err)
					}
					foreignNode, _, err = retagBoundTypes(foreign.catalog, foreignNode, func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == nil {
							return nil
						}
						v, _ := foreign.catalog.LookupType(d.ID())
						return v
					})
					if err != nil {
						t.Fatal("foreign retag fixture", err)
					}
					probe = evaluation
					probe.routineAuthority = foreign.routineAuthority
					if err := admitRoutineCall(&probe, foreignNode, false, false); err == nil {
						t.Fatal("foreign retag acquired original authority")
					}
				}
			}
			if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.dependencies, original) {
				t.Fatal("authority probes mutated source")
			}
			if err := admit(&evaluation, node, ref); err != nil {
				t.Fatal("source authority lost", err)
			}
		})
	}
}

func TestIntaggRoutineIndependentOwners(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	source := roleAttributeCreate(t, e, op, "intagg_owner_source", false, parser.RoleOptions{})
	target := roleAttributeCreate(t, e, op, "intagg_owner_target", false, parser.RoleOptions{})
	original := e.CatalogRead()
	member := original.state.descriptors[ext.members[0]].(*procedureDescriptor)
	sibling := original.state.descriptors[ext.members[1]]
	stage := e.BeginCatalogStage(original)
	defer e.DiscardCatalogStage(stage)
	if err := stageOwnership(stage, member, source.Ref); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	transferred := e.state.descriptors[member.address].(*procedureDescriptor)
	if transferred.owner != source.Ref || transferred == member || e.state.descriptors[ext.address] != ext || e.state.descriptors[ext.members[1]] != sibling {
		t.Fatal("single-member ownership was not independent COW")
	}
	if err := stockIntagg.validate(e.CatalogRead(), ext); err != nil {
		t.Fatal("valid divergent owners rejected", err)
	}
	metadata, err := extensionOwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := decodeExtensionOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found || record.Owner != "postgres" || len(record.Members) != 2 {
		t.Fatal("named independent owner manifest", record, err)
	}
	expected := map[string]string{"int_array_aggregate": "intagg_owner_source", "int_array_enum": "postgres"}
	for _, m := range record.Members {
		if m.Kind != "procedure" || m.Owner != expected[m.Name] {
			t.Fatal("member owner manifest", m)
		}
		delete(expected, m.Name)
	}
	if len(expected) != 0 {
		t.Fatal("missing named member")
	}
	imported := e.restoreStage()
	importOp, finish := roleOperation(t, imported, "postgres")
	defer finish()
	if err := extensionUnitExecute(t, imported, importOp, &parser.CreateExtension{Name: parser.Identifier{Text: "intagg"}}); err != nil {
		t.Fatal(err)
	}
	if err := imported.importExtensionOwners(context.Background(), importOp, []extensionOwnerRecord{record}); err != nil {
		t.Fatal("independent owner import", err)
	}
	installed := imported.state.descriptors[imported.state.names[ids.NameKey{ClassID: ExtensionClassID, Name: "intagg"}]].(*extensionDescriptor)
	if err := stockIntagg.validate(imported.CatalogRead(), installed); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := extensionOwnerMetadata(imported.CatalogRead(), imported.Roles(), installed)
	if err != nil || roundtrip != metadata {
		t.Fatal("named owner roundtrip", roundtrip, err)
	}
	foreign := New()
	foreignOwner := foreign.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, owner := range []authorization.PrincipalRef{{}, foreignOwner} {
		state := cloneCatalog(e.state)
		for _, a := range ext.members {
			bad := *state.descriptors[a].(*procedureDescriptor)
			bad.owner = owner
			state.descriptors[a] = &bad
		}
		if err := stockIntagg.validate(CatalogRead{owner: e, state: state}, ext); err == nil {
			t.Fatal("uniform zero/foreign owners accepted")
		}
	}
	if err := stockIntagg.validate(CatalogRead{state: e.state}, ext); err == nil {
		t.Fatal("missing owning engine accepted")
	}
	if result, err := e.Execute(op, &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "intagg_owner_source"}}, Target: parser.RoleSpec{Name: "intagg_owner_target"}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
		t.Fatal("member reassignment", result, err)
	}
	reassigned := e.state.descriptors[member.address].(*procedureDescriptor)
	if reassigned.owner != target.Ref || transferred.owner != source.Ref || member.owner != ext.owner || e.state.descriptors[ext.members[1]] != sibling || original.state.descriptors[member.address] != member {
		t.Fatal("ownership generations mutated")
	}
	if err := stockIntagg.validate(e.CatalogRead(), ext); err != nil {
		t.Fatal(err)
	}
}
