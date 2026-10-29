package enginetests_test

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func ltreeLqueryOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session, direct bool) *plan.ProjectNode {
	t.Helper()
	query := "SELECT a ~ b,b ~ a FROM lq_owner"
	if direct {
		query = "SELECT public.ltq_regex(a,b),public.ltq_rregex(b,a) FROM lq_owner"
	}
	ast := enginetest.Statement(t, query)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	binding, err := analyze.Describe(op.Context(), e.AnalysisHost(op), ast, core.AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, nil, false)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project, ok := binding.Query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != int(sqltype.LtreeLqueryOperationCount) {
		t.Fatal("selected projection absent")
	}
	return project
}
func ltreeLqueryOwnershipCall(t *testing.T, node expr.Expr) functions.LtreeLquery {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator == nil || selected.Ref.Address != selected.Operator.Address() {
			t.Fatal("duplicate operator authority")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.LtreeLquery)
	if !ok {
		t.Fatalf("selected provider absent: %T", node)
	}
	return call
}
func ltreeLqueryOwnershipSetup(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree")
	enginetest.SessionSQL(t, s, "CREATE TABLE lq_owner(a ltree,b lquery)")
	return s
}
func TestLtreeLqueryStoredOwnership(t *testing.T) {
	e := engine.New()
	s := ltreeLqueryOwnershipSetup(t, e)
	view := e.CatalogRead()
	for _, direct := range []bool{false, true} {
		project := ltreeLqueryOwnershipBinding(t, e, s, direct)
		identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
		for i, node := range project.Expressions {
			call := ltreeLqueryOwnershipCall(t, node)
			if call.Operation != sqltype.LtreeLqueryOperation(i) || call.Member != uint8(extension.LtreeLqueryProcedureStart+i) || call.Info.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain {
				t.Fatal("selected occurrence/role lost")
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
				next, err := rebuild(call.Info, call.Children())
				if err != nil || !reflect.DeepEqual(next, call) {
					t.Fatal("rebuild changed immutable occurrence", err)
				}
			}
			for _, kind := range []expr.CaptureKind{expr.CaptureFunction, expr.CaptureOperator} {
				capture, ok, err := call.Capture(kind)
				if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Args, call.Children()) {
					t.Fatal("capture lost source order", err)
				}
			}
			spec, err := call.Render()
			if err != nil || len(spec.Args) != 2 || spec.Args[0].TransportType != call.Formals[0].Usage() || spec.Args[1].TransportType != call.Formals[1].Usage() {
				t.Fatal("formal rendering lost", err)
			}
			if call.IntrinsicVolatility() != expr.Immutable || call.PlanningFold() != expr.FoldConstant || call.StrictNull() != expr.StrictAfter {
				t.Fatal("planning policy")
			}
			next, changed, err := stored.RetagTypes(view, node, identity)
			if err != nil || changed || !reflect.DeepEqual(next, node) {
				t.Fatal("identity retag", changed, err)
			}
			next, err = stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil || !reflect.DeepEqual(next, node) {
				t.Fatal("identity reference remap", err)
			}
			historical := catalog.CloneState(view.State())
			advanced := *call.RoutineReference.(*catalog.Procedure)
			advanced.ObjectRevision = advanced.Revision() + 1
			historical.SetDescriptor(advanced.Address(), &advanced)
			if _, changed, err := stored.RetagTypes(view.WithState(historical), node, identity); err != nil || changed {
				t.Fatal("historical unchanged declaration rejected after presentation revision", changed, err)
			}
			for corruption, corrupt := range []func(*functions.LtreeLquery){
				func(n *functions.LtreeLquery) { n.Member++ }, func(n *functions.LtreeLquery) { n.Operation ^= 1 },
				func(n *functions.LtreeLquery) { foreign := *n.Formals[0]; n.Formals[0] = &foreign },
				func(n *functions.LtreeLquery) { foreign := *n.Formals[1]; n.Formals[1] = &foreign },
				func(n *functions.LtreeLquery) { foreign := *n.Usage.Type; n.Usage.Type = &foreign },
				func(n *functions.LtreeLquery) { n.Usage.Type = n.Formals[0] },
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Display.Source = "ltree_out"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					copied := *foreign.Origin.Installation
					foreign.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: foreign.Origin.Declaration}
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Origin = nil
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Owner = (&authorization.Domain{}).Principal(foreign.Owner.ID())
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) { n.Ref.Epoch++ },
				func(n *functions.LtreeLquery) { n.Ref.Epoch = 0 },
				func(n *functions.LtreeLquery) { n.Ref = (&ids.Domain{}).Ref(n.Ref.Epoch, n.Ref.Address) },
				func(n *functions.LtreeLquery) {
					n.Ref = view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: n.Ref.Address.ObjectID + 1})
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Implementation = extension.StockCitext
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.ObjectRevision++
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Signature = "25,25"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeLquery) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Result = 25
					n.RoutineReference = &foreign
				},
			} {
				bad := call
				corrupt(&bad)
				if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
					t.Fatalf("forged captured provider admitted: role=%d corruption=%d", i, corruption)
				}
				if !reflect.DeepEqual(ltreeLqueryOwnershipCall(t, node), call) {
					t.Fatal("failed admission mutated captured source")
				}
			}
			other := ltreeLqueryOwnershipCall(t, project.Expressions[(i+1)%len(project.Expressions)])
			if call.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("different role grouped")
			}
		}
	}
}
func TestLtreeLqueryStoredRemapOwnership(t *testing.T) {
	source, target := engine.New(), engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss, ts := ltreeLqueryOwnershipSetup(t, source), ltreeLqueryOwnershipSetup(t, target)
	enginetest.SessionSQL(t, ts, `CREATE EXTENSION "uuid-ossp"`)
	bound := ltreeLqueryOwnershipBinding(t, source, ss, false)
	_ = ts
	sv, tv := source.CatalogRead(), target.CatalogRead()
	installed := func(v catalog.Read) *catalog.Extension {
		return v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	}
	se, te := installed(sv), installed(tv)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, a := range se.Installation.Members {
		mapping[a] = te.Installation.Members[i]
	}
	for _, slot := range []int{0, 3, 6} {
		a, b := sv.MustType(se.Installation.Members[slot]), tv.MustType(te.Installation.Members[slot])
		mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(a.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(b.Array)}
	}
	relation := func(v catalog.Read) ids.ObjectAddress {
		return v.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "lq_owner"})
	}
	mapping[relation(sv)] = relation(tv)
	address := func(old ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[old]; ok {
			return next
		}
		return old
	}
	reference := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		return tv.Owner().Domain.Ref(tv.Epoch(), address(old.Address))
	}
	types := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		next, ok := tv.LookupType(sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}).ObjectID))
		if !ok {
			return old
		}
		return next
	}
	principal := func(old authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if old != (authorization.PrincipalRef{}) && old != sv.Owner().Principals.Principal(old.ID()) {
			return authorization.PrincipalRef{}, fmt.Errorf("foreign source principal")
		}
		return tv.Owner().Principals.Principal(old.ID()), nil
	}
	principals := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(old authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(old)
	}})
	procedure := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		if old == nil {
			return nil, nil
		}
		if err := extension.StockLtree.ValidateProcedure(sv, old); err != nil {
			return nil, err
		}
		return principals(extension.RemapDescriptor(old, mapping).(*catalog.Procedure))
	}
	mapper := stored.ViewQueryRemapper{Catalog: tv, Ref: reference, Type: types, Procedure: procedure}
	mapped, err := mapper.Nested(bound)
	if err != nil {
		t.Fatal("authorized source remap", err)
	}
	project, ok := mapped.(*plan.ProjectNode)
	if !ok {
		t.Fatal("lost project")
	}
	for i, node := range project.Expressions {
		old, next := ltreeLqueryOwnershipCall(t, bound.Expressions[i]), ltreeLqueryOwnershipCall(t, node)
		if next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Formals[0] != types(old.Formals[0]) || next.Formals[1] != types(old.Formals[1]) || next.Formals == old.Formals || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Operation != old.Operation {
			t.Fatal("remap lost exact ownership")
		}
		captured, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || captured == old.RoutineReference || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()), captured) {
			t.Fatal("remapped captured declaration", err)
		}
		if old.Ref.Domain() != sv.Owner().Domain || ltreeLqueryOwnershipCall(t, bound.Expressions[i]).Formals != old.Formals {
			t.Fatal("source changed")
		}
	}
	receiver := tv.MustExtension(tv.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	for _, absent := range []bool{false, true} {
		for _, foreign := range []bool{false, true} {
			current := ltreePathQuerySurvivorView(t, tv, te, receiver, absent, foreign)
			for _, direct := range []bool{false, true} {
				input := bound
				if direct {
					input = ltreeLqueryOwnershipBinding(t, source, ss, true)
				}
				survivor := mapper.Fresh()
				survivor.Catalog = current
				root, err := survivor.Nested(input)
				if err != nil {
					t.Fatal("surviving destination remap", absent, foreign, direct, err)
				}
				result := root.(*plan.ProjectNode)
				for i, node := range result.Expressions {
					old, next := ltreeLqueryOwnershipCall(t, input.Expressions[i]), ltreeLqueryOwnershipCall(t, node)
					if next.Ref != reference(old.Ref) || next.Member != old.Member || next.Operation != old.Operation || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Formals[0] != types(old.Formals[0]) || next.Formals[1] != types(old.Formals[1]) {
						t.Fatal("surviving remap changed capture")
					}
					if _, _, err := stored.RetagTypes(current, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
						t.Fatal("surviving remap not current", err)
					}
				}
			}
		}
	}
	for _, input := range []*plan.ProjectNode{bound, ltreeLqueryOwnershipBinding(t, source, ss, true)} {
		// A mapper owns a multi-root operation. A changed destination policy starts
		// a fresh operation so source/current validators authenticate it again.
		accepted, err := mapper.Nested(input)
		if err != nil {
			t.Fatal("reused authorized remap", err)
		}
		again, err := mapper.Nested(input)
		if err != nil || again != accepted {
			t.Fatal("accepted root identity lost", err)
		}
		fresh := mapper.Fresh()
		distinct, err := fresh.Nested(input)
		if err != nil || distinct == accepted {
			t.Fatal("fresh operation inherited accepted root", err)
		}
		for _, slot := range []int{0, 3} {
			scalar := sv.MustType(se.Installation.Members[slot]).SQLType
			candidates := []*sqltype.Descriptor{scalar}
			for _, candidate := range candidates {
				foreign := *types(candidate)
				bad := mapper.Fresh()
				bad.Type = func(old *sqltype.Descriptor) *sqltype.Descriptor {
					next := types(old)
					if next.ID() == foreign.ID() {
						return &foreign
					}
					return next
				}
				if _, err := bad.Nested(input); err == nil {
					t.Fatal("foreign same-OID target generation admitted")
				}
			}
		}
		bad := mapper.Fresh()
		bad.Ref = func(ref ids.ObjectRef) ids.ObjectRef {
			next := reference(ref)
			if next == (ids.ObjectRef{}) {
				return next
			}
			return sv.Owner().Domain.Ref(next.Epoch, next.Address)
		}
		if _, err := bad.Nested(input); err == nil {
			t.Fatal("foreign reference domain admitted")
		}
		bad = mapper.Fresh()
		bad.Ref = func(ref ids.ObjectRef) ids.ObjectRef {
			next := reference(ref)
			if next != (ids.ObjectRef{}) {
				next.Epoch = 0
			}
			return next
		}
		if _, err := bad.Nested(input); err == nil {
			t.Fatal("foreign reference epoch admitted")
		}
		state := catalog.CloneState(tv.State())
		broken := *tv.MustProcedure(te.Installation.Members[extension.LtreeLqueryProcedureStart])
		broken.Result = 25
		state.SetDescriptor(broken.Address(), &broken)
		bad = mapper.Fresh()
		bad.Catalog = target.Catalog.Read(state)
		if _, err := bad.Nested(input); err == nil {
			t.Fatal("changed destination procedure declaration admitted")
		}
		bad = mapper.Fresh()
		bad.Procedure = func(old *catalog.Procedure) (*catalog.Procedure, error) {
			return nil, fmt.Errorf("rejected destination procedure policy")
		}
		if _, err := bad.Nested(input); err == nil {
			t.Fatal("changed procedure policy bypassed")
		}
		// A failure must not publish placeholders into a later valid operation or
		// mutate the accepted graph in the original operation.
		recovered := bad.Fresh()
		recovered.Procedure = procedure
		if _, err := recovered.Nested(input); err != nil {
			t.Fatal("failed operation poisoned fresh valid remap", err)
		}
		retained, err := mapper.Nested(input)
		if err != nil || retained != accepted {
			t.Fatal("rejected remap poisoned authorized traversal", err)
		}
	}
	if installed(sv) != se || installed(tv) != te {
		t.Fatal("rejected remap published mutation")
	}
}

func TestLtreeLqueryStoredSurvivorAuthority(t *testing.T) {
	e := engine.New()
	s := ltreeLqueryOwnershipSetup(t, e)
	enginetest.SessionSQL(t, s, `CREATE EXTENSION "uuid-ossp"`)
	view := e.CatalogRead()
	origin := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, direct := range []bool{false, true} {
		project := ltreeLqueryOwnershipBinding(t, e, s, direct)
		for _, absent := range []bool{false, true} {
			for _, foreign := range []bool{false, true} {
				current := ltreePathQuerySurvivorView(t, view, origin, receiver, absent, foreign)
				for _, node := range project.Expressions {
					call := ltreeLqueryOwnershipCall(t, node)
					rebound, changed, err := stored.RetagTypes(current, node, identity)
					var expected expr.Expr = call
					selected, operator := node.(plan.SelectedOperatorExpr)
					if operator {
						selected.Operator, _ = current.Operator(selected.Ref.Address)
						selected.Provider = call
						expected = selected
					}
					if err != nil || changed != operator || !reflect.DeepEqual(rebound, expected) {
						t.Fatal("surviving query carrier changed", call.Operation, absent, foreign, direct, changed, err)
					}
					if !reflect.DeepEqual(ltreeLqueryOwnershipCall(t, node), call) {
						t.Fatal("survivor mutated source")
					}
					for _, cause := range []string{"principal", "provider", "declaration", "seal", "missing", "left_generation", "right_generation", "result_generation"} {
						state := catalog.CloneState(current.State())
						bad := *current.MustProcedure(call.Ref.Address)
						switch cause {
						case "principal":
							bad.Owner = (&authorization.Domain{}).Principal(bad.Owner.ID())
						case "provider":
							bad.Implementation = extension.StockCitext
						case "declaration":
							bad.Signature = "16"
						case "seal":
							copied := *bad.Origin.Installation
							bad.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: bad.Origin.Declaration}
						case "missing":
							state.RemoveDescriptor(bad.Address())
						case "left_generation", "right_generation", "result_generation":
							typ := call.Formals[0]
							if cause == "right_generation" {
								typ = call.Formals[1]
							}
							if cause == "result_generation" {
								typ = call.Usage.Type
							}
							address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
							base := *current.MustType(address)
							generation := *base.SQLType
							base.SQLType = &generation
							state.SetDescriptor(address, &base)
						}
						if cause != "missing" && cause != "left_generation" && cause != "right_generation" && cause != "result_generation" {
							state.SetDescriptor(bad.Address(), &bad)
						}
						if _, _, err := stored.RetagTypes(current.WithState(state), node, identity); err == nil {
							t.Fatal("invalid current query authority admitted", cause, call.Operation)
						}
					}
				}
			}
		}
	}
}
