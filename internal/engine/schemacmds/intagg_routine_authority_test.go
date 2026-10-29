package schemacmds_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Public answers live in reference; these exercise genuine original authority
// and COW catalog seams with independently fixed released identities.
func TestIntaggRoutineOriginalAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE TABLE strict_null_rows(v integer)")
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE strict_null_steps START 1")
	enginetest.RoleExecute(t, e, op, "CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)")
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "intagg", ""))
	other, otherOp, otherScope := strictNullUnitFixture(t)
	extensionUnitPublish(t, other, otherOp, intarrayUnitStage(t, other, otherOp, "intagg", ""))
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	// The real stored consumer supplies graft ancestry, not a fabricated recipe.
	enginetest.RoleExecute(t, e, op, "CREATE VIEW intagg_authority_recipe AS SELECT int_array_enum('{1,NULL,2}'::integer[])")
	recipeRef := enginetest.RoleTableRef(t, e, "intagg_authority_recipe")
	recipe := e.CatalogRead().MustRelation(recipeRef.Address)
	// Complete catalog setup before retaining statement-owner admission. Calling
	// Engine.Execute with this operation while scope is held would reenter it.
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	for _, tc := range []struct {
		name, text, signature string
		kind                  catalog.RoutineKind
		result                ids.OID
		strict                bool
	}{
		{"aggregate", "int_array_aggregate(v)", "23", catalog.AggregateRoutine, 1007, false},
		{"enum", "int_array_enum('{1,NULL,2}'::integer[])", "1007", catalog.FunctionRoutine, 23, true},
		{"enum_null", "int_array_enum(NULL::integer[])", "1007", catalog.FunctionRoutine, 23, true},
		{"enum_parameter", "int_array_enum($1::integer[])", "1007", catalog.FunctionRoutine, 23, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.text)
			var captured *catalog.Procedure
			var ref ids.ObjectRef
			aggregate := tc.kind == catalog.AggregateRoutine
			if aggregate {
				if len(binder.Grouping.Aggregates) != 1 {
					t.Fatal("missing actual aggregate")
				}
				call := binder.Grouping.Aggregates[0]
				captured, ref = call.Procedure, call.Routine
				if ref != call.Ref || ref == (ids.ObjectRef{}) {
					t.Fatal("aggregate lost original compact authority")
				}
				if carrier, err := exec.ExpressionRoutineDescriptor(node); err != nil || carrier != nil {
					t.Fatal("aggregate acquired scalar admission carrier", err)
				}
			} else {
				enum, ok := node.(plan.UnnestExpr)
				if !ok || !exec.SourceAdmitted(enum) || !binder.Clause.QueryHasTargetSet {
					t.Fatal("lost genuine set carrier")
				}
				captured, ref = enum.Procedure, enum.Ref
				carrier, err := exec.ExpressionRoutineDescriptor(node)
				if err != nil || carrier != captured || !core.ProcedureReturnsSet(captured) || exec.StrictScalarRoutine(captured) {
					t.Fatal("enum selected declaration/set classification", err)
				}
				if tc.name == "enum_parameter" {
					evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
					folded, changed, err := exec.FoldStrictParameterNull(&evaluation, node)
					if err != nil || changed || !reflect.DeepEqual(folded, node) {
						t.Fatal("NULL enum folded into scalar", changed, err)
					}
				}
				if tc.name == "enum_null" || tc.name == "enum_parameter" {
					cursor, err := exec.OpenFunctionSource(&evaluation, enum)
					if err != nil {
						t.Fatal(err)
					}
					_, present, err := cursor.Next(evaluation.Context)
					cursor.Close()
					if err != nil || present {
						t.Fatal("NULL enum is not an empty set", present, err)
					}
				}
			}
			expectedName := "int_array_enum"
			if aggregate {
				expectedName = "int_array_aggregate"
			}
			ext := evaluation.Catalog.MustExtension(captured.Extension)
			if captured.Name() != expectedName || captured.Signature != catalog.ProcedureSignature(tc.signature) || captured.RoutineKind != tc.kind || captured.Result != tc.result || captured.Strict != tc.strict || captured.Volatility != expr.Immutable || extension.IntaggProviderOf(captured) != extension.StockIntagg || captured.Builtin != nil || captured.Revision() == 0 || len(captured.Body) != 0 || ext.Name() != "intagg" || ext.Version != "1.1" || len(ext.Members) != 2 || captured.Namespace() != ext.TargetNamespace || evaluation.Catalog.Get(ref.Address) != captured || ref.Domain() != e.Catalog.Domain || ref.Epoch != evaluation.Catalog.Epoch() {
				t.Fatal("released full identity/original installation", captured, ref)
			}
			original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			found := false
			for _, dep := range original {
				if dep.Ref.Address == ref.Address {
					found = true
					if dep.Ref != ref || dep.Revision != captured.Revision() {
						t.Fatal("original ref/revision mismatch")
					}
				} else if dep.Ref.Address.ClassID == ids.ProcedureClassID {
					t.Fatal("invented core/helper dependency", dep)
				}
			}
			if !found {
				t.Fatal("missing original manifest")
			}
			admit := func(c *exec.Context, n expr.Expr, r ids.ObjectRef) error {
				if aggregate {
					return exec.AdmitRoutineReference(c, r, captured, "aggregate")
				}
				return exec.AdmitRoutineCall(c, n, false, false)
			}
			if err := admit(&evaluation, node, ref); err != nil {
				t.Fatal("original admission", err)
			}
			if aggregate {
				if err := exec.AdmitRoutineReference(&evaluation, ids.ObjectRef{}, captured, "aggregate"); err == nil {
					t.Fatal("missing aggregate identity admitted")
				}
			}
			before := node
			if enum, ok := node.(plan.UnnestExpr); ok {
				enum.Args = append([]expr.Expr(nil), enum.Args...)
				before = enum
			}
			for _, mode := range []string{"nil", "empty", "missing", "epoch", "revision", "foreign_ref"} {
				probe := evaluation
				deps := append([]catalog.ExpressionDependency(nil), original...)
				switch mode {
				case "nil":
					deps = nil
				case "empty":
					deps = []catalog.ExpressionDependency{}
				case "missing":
					deps = deps[:0]
					for _, dep := range original {
						if dep.Ref.Address != ref.Address {
							deps = append(deps, dep)
						}
					}
				default:
					for i := range deps {
						if deps[i].Ref.Address == ref.Address {
							switch mode {
							case "epoch":
								deps[i].Ref.Epoch++
							case "revision":
								deps[i].Revision++
							case "foreign_ref":
								deps[i].Ref = other.Catalog.Domain.Ref(deps[i].Ref.Epoch, deps[i].Ref.Address)
							}
						}
					}
				}
				probe.RoutineChecks = deps
				if err := admit(&probe, node, ref); err == nil {
					t.Fatal("repaired absent/stale original authority", mode)
				}
			}
			probe := evaluation
			probe.RoutineAuthority = foreign.RoutineAuthority
			if err := admit(&probe, node, ref); err == nil {
				t.Fatal("foreign engine became original authority")
			}
			for _, mutate := range []func(*catalog.Procedure){
				func(d *catalog.Procedure) { d.Implementation = nil },
				func(d *catalog.Procedure) {
					d.Implementation = new(*extension.StockIntagg)
				},
				func(d *catalog.Procedure) { d.RoutineKind = catalog.ProcedureRoutine },
			} {
				auth := *core.RoutineAuthorityView(&evaluation)
				auth.Catalog = auth.Catalog.WithState(catalog.CloneState(auth.Catalog.State()))
				bad := *captured
				mutate(&bad)
				auth.Catalog.State().SetDescriptor(ref.Address, &bad)
				if _, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured); err == nil {
					t.Fatal("incompatible provider/kind declaration accepted")
				}
				probe = evaluation
				probe.RoutineAuthority = &auth
				if err := admit(&probe, node, ref); err == nil {
					t.Fatal("call admitted incompatible current declaration")
				}
			}
			if !aggregate {
				grafted, _, err := stored.MapExpression(evaluation.Catalog, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, recipe, nil)
				if err != nil {
					t.Fatal("graft real selected enum", err)
				}
				graftProbe := evaluation
				graftProbe.RoutineChecks = nil
				if err := exec.AdmitRoutineCall(&graftProbe, grafted, false, false); err != nil {
					t.Fatal("graft did not select actual owning manifest", err)
				}
				if expr.InfoOf(grafted).RoutineRecipeReference != recipe || expr.InfoOf(node).RoutineRecipeReference != nil {
					t.Fatal("graft ancestry lost or mutated source")
				}
				if tc.name == "enum_parameter" {
					if _, err := stored.RemapExpression(node, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil); err == nil || !strings.Contains(err.Error(), "unpersistable scalar expression plan.ParameterExpr") {
						t.Fatal("parameter persistence boundary changed", err)
					}
					rewritten, _, err := stored.MapExpression(evaluation.Catalog, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, nil)
					if err != nil || !reflect.DeepEqual(rewritten, node) {
						t.Fatal("structural parameter rewrite lost original carrier", err)
					}
					if err := exec.AdmitRoutineCall(&evaluation, rewritten, false, false); err != nil {
						t.Fatal("structural parameter rewrite lost original authority", err)
					}
					foreignEnum := node.(plan.UnnestExpr)
					foreignEnum.Ref = other.Catalog.Domain.Ref(foreignEnum.Ref.Epoch, foreignEnum.Ref.Address)
					foreignEnum.Ref.Epoch = foreign.Catalog.Epoch()
					_, _, err = stored.RetagTypes(foreign.Catalog, foreignEnum, func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == nil {
							return nil
						}
						current, _ := foreign.Catalog.LookupType(d.ID())
						return current
					})
					if err == nil {
						t.Fatal("structural foreign parameter retag acquired principal authority")
					}
					probe = evaluation
					probe.RoutineAuthority = foreign.RoutineAuthority
					if err := exec.AdmitRoutineCall(&probe, foreignEnum, false, false); err == nil {
						t.Fatal("foreign parameter retag acquired original authority")
					}
				} else {
					text, err := (stored.Deparser{View: evaluation.Catalog}).SQL(context.Background(), node, stored.DeparseParent{})
					if err != nil {
						t.Fatal("literal enum rendering", err)
					}
					rebound, _, reboundEvaluation := strictNullUnitBind(t, e, op, scope, text)
					reboundDescriptor, err := exec.ExpressionRoutineDescriptor(rebound)
					if err != nil || reboundDescriptor != captured {
						t.Fatal("literal render/rebind changed selected declaration", err)
					}
					if err := exec.AdmitRoutineCall(&reboundEvaluation, rebound, false, false); err != nil {
						t.Fatal("literal render/rebind authority", err)
					}
					// Map the actual original declaration and its paired selected ref;
					// no old OID can resolve in this relocated catalog.
					mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ids.ExtensionClassID, 900000, 0}}
					for i, a := range ext.Members {
						mapping[a] = ids.ObjectAddress{ids.ProcedureClassID, ids.OID(900001 + i), 0}
					}
					state := catalog.CloneState(evaluation.Catalog.State())
					for a := range mapping {
						state.UnbindName(catalog.DescriptorNameKey(state.Get(a)))
						state.RemoveDescriptor(a)
					}
					for a, dest := range mapping {
						d := extension.RemapDescriptor(evaluation.Catalog.Get(a), mapping)
						state.SetDescriptor(dest, d)
						state.BindName(catalog.DescriptorNameKey(d), dest)
					}
					state.SetEpoch(state.Epoch() + 1)
					view := e.Catalog.Read(state)
					remap := func(r ids.ObjectRef) ids.ObjectRef {
						r.Epoch = state.Epoch()
						if a, ok := mapping[r.Address]; ok {
							r.Address = a
						}
						return r
					}
					mapper := stored.ViewQueryRemapper{Catalog: view, Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), nil, nil)}
					query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "value", Usage: node.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{node}}
					remapped, err := mapper.Query(query)
					if err != nil {
						t.Fatal("remap original selected enum", err)
					}
					relocated := remapped.(*plan.ProjectNode).Expressions[0]
					current, err := exec.ExpressionRoutineDescriptor(relocated)
					if err != nil || !exec.SameRoutineDeclaration(state.MustProcedure(mapping[ref.Address]), current) || current.Revision() != captured.Revision() {
						t.Fatal("remap lost original selected declaration", err)
					}
					// Use the existing private catalog-authority constructor: the paired
					// catalog and role read must both belong to this relocated root.
					auth := enginetest.CatalogAuthorization(e, view)
					probe = evaluation
					probe.Catalog = view
					probe.RoutineAuthority = &auth
					if err := exec.AdmitRoutineCall(&probe, relocated, false, false); err == nil {
						t.Fatal("retag manufactured original manifest")
					}
					deps := append([]catalog.ExpressionDependency(nil), original...)
					for i := range deps {
						deps[i].Ref = remap(deps[i].Ref)
					}
					probe.RoutineChecks = deps
					if err := exec.AdmitRoutineCall(&probe, relocated, false, false); err != nil {
						t.Fatal("transferred original authority", err)
					}
					foreignNode, err := stored.RemapExpression(node, func(r ids.ObjectRef) ids.ObjectRef {
						r = other.Catalog.Domain.Ref(r.Epoch, r.Address)
						r.Epoch = foreign.Catalog.Epoch()
						return r
					}, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, _, err = stored.RetagTypes(foreign.Catalog, foreignNode, func(d *sqltype.Descriptor) *sqltype.Descriptor {
						if d == nil {
							return nil
						}
						v, _ := foreign.Catalog.LookupType(d.ID())
						return v
					})
					if err == nil {
						t.Fatal("foreign structural retag acquired principal authority")
					}
					probe = evaluation
					probe.RoutineAuthority = foreign.RoutineAuthority
					if err := exec.AdmitRoutineCall(&probe, foreignNode, false, false); err == nil {
						t.Fatal("foreign retag acquired original authority")
					}
				}
			}
			if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, original) {
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
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	source := roleAttributeCreate(t, e, op, "intagg_owner_source", false, parser.RoleOptions{})
	target := roleAttributeCreate(t, e, op, "intagg_owner_target", false, parser.RoleOptions{})
	original := e.CatalogRead()
	member := original.MustProcedure(ext.Members[0])
	sibling := original.Get(ext.Members[1])
	stage := e.BeginCatalogStage(original)
	defer e.DiscardCatalogStage(stage)
	if err := role.StageOwnership(stage, member, source.Ref); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	transferred := e.Catalog.State.MustProcedure(member.Address())
	if transferred.Owner != source.Ref || transferred == member || e.Catalog.State.Get(ext.Address()) != ext || e.Catalog.State.Get(ext.Members[1]) != sibling {
		t.Fatal("single-member ownership was not independent COW")
	}
	if err := extension.StockIntagg.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal("valid divergent owners rejected", err)
	}
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
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
	imported := e.RestoreStage()
	importOp, finish := enginetest.RoleOperation(t, imported, "postgres")
	defer finish()
	if err := extensionUnitExecute(t, imported, importOp, &parser.CreateExtension{Name: parser.Identifier{Text: "intagg"}}); err != nil {
		t.Fatal(err)
	}
	if err := schemacmds.ImportExtensionOwners(imported, context.Background(), importOp, []extension.OwnerRecord{record}); err != nil {
		t.Fatal("independent owner import", err)
	}
	installed := imported.Catalog.State.MustExtension(imported.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intagg"}))
	if err := extension.StockIntagg.ValidateExtension(imported.CatalogRead(), installed); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := extension.OwnerMetadata(imported.CatalogRead(), imported.Roles(), installed)
	if err != nil || roundtrip != metadata {
		t.Fatal("named owner roundtrip", roundtrip, err)
	}
	foreign := engine.New()
	foreignOwner := foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, owner := range []authorization.PrincipalRef{{}, foreignOwner} {
		state := catalog.CloneState(e.Catalog.State)
		for _, a := range ext.Members {
			bad := *state.MustProcedure(a)
			bad.Owner = owner
			state.SetDescriptor(a, &bad)
		}
		if err := extension.StockIntagg.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
			t.Fatal("uniform zero/foreign owners accepted")
		}
	}
	if err := extension.StockIntagg.ValidateExtension(catalog.NewRead(nil, e.Catalog.State), ext); err == nil {
		t.Fatal("missing owning engine accepted")
	}
	if result, err := e.Execute(op, &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "intagg_owner_source"}}, Target: parser.RoleSpec{Name: "intagg_owner_target"}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
		t.Fatal("member reassignment", result, err)
	}
	reassigned := e.Catalog.State.MustProcedure(member.Address())
	if reassigned.Owner != target.Ref || transferred.Owner != source.Ref || member.Owner != ext.Owner || e.Catalog.State.Get(ext.Members[1]) != sibling || original.Get(member.Address()) != member {
		t.Fatal("ownership generations mutated")
	}
	if err := extension.StockIntagg.ValidateExtension(e.CatalogRead(), ext); err != nil {
		t.Fatal(err)
	}
}

func ownershipUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
