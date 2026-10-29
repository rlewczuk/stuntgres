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

func ltreePathOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session, direct bool) *plan.ProjectNode {
	t.Helper()
	query := "SELECT subltree(a,0,1),subpath(a,0,1),subpath(a,1),index(a,b),index(a,b,1),nlevel(a),ltree2text(a),text2ltree(t),lca(x),lca(a,b),lca(a,b,a),lca(a,b,a,a),lca(a,b,a,a,a),lca(a,b,a,a,a,a),lca(a,b,a,a,a,a,a),lca(a,b,a,a,a,a,a,a),"
	if direct {
		query += "public.ltree_addltree(a,b),public.ltree_addtext(a,t),public.ltree_textadd(t,a)"
	} else {
		query += "a || b,a || t,t || a"
	}
	query += " FROM lh_owner"
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
	if !ok || len(project.Expressions) != int(sqltype.LtreePathOperationCount) {
		t.Fatal("selected projection absent")
	}
	return project
}
func ltreePathOwnershipCall(t *testing.T, node expr.Expr) functions.LtreePath {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator == nil || selected.Ref.Address != selected.Operator.Address() {
			t.Fatal("duplicate operator authority")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.LtreePath)
	if !ok {
		t.Fatalf("selected provider absent: %T", node)
	}
	return call
}
func ltreePathOwnershipSetup(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree")
	enginetest.SessionSQL(t, s, "CREATE TABLE lh_owner(a ltree,b ltree,t text,x ltree[])")
	return s
}
func TestLtreePathStoredOwnership(t *testing.T) {
	e := engine.New()
	s := ltreePathOwnershipSetup(t, e)
	view := e.CatalogRead()
	for _, direct := range []bool{false, true} {
		project := ltreePathOwnershipBinding(t, e, s, direct)
		identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
		for i, node := range project.Expressions {
			call := ltreePathOwnershipCall(t, node)
			if call.Operation != sqltype.LtreePathOperation(i) || call.Member != uint8(extension.LtreePathProcedureStart+i) || call.Info.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain {
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
				if err != nil || !ok || capture.Count != call.Count || !reflect.DeepEqual(capture.Args, call.Children()) {
					t.Fatal("capture lost source order", err)
				}
			}
			spec, err := call.Render()
			if err != nil || len(spec.Args) != call.Count {
				t.Fatal("formal rendering lost", err)
			}
			for i, arg := range spec.Args {
				if arg.TransportType != call.Formals[i].Usage() {
					t.Fatal("formal rendering changed", i)
				}
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
			for corruption, corrupt := range []func(*functions.LtreePath){
				func(n *functions.LtreePath) { n.Member++ }, func(n *functions.LtreePath) { n.Operation ^= 1 },
				func(n *functions.LtreePath) { foreign := *n.Formals[0]; n.Formals[0] = &foreign },
				func(n *functions.LtreePath) { foreign := *n.Usage.Type; n.Usage.Type = &foreign },
				func(n *functions.LtreePath) { n.Usage.Type = nil },
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Display.Source = "ltree_out"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					copied := *foreign.Origin.Installation
					foreign.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: foreign.Origin.Declaration}
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Origin = nil
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Owner = (&authorization.Domain{}).Principal(foreign.Owner.ID())
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) { n.Ref.Epoch++ },
				func(n *functions.LtreePath) { n.Ref.Epoch = 0 },
				func(n *functions.LtreePath) { n.Ref = (&ids.Domain{}).Ref(n.Ref.Epoch, n.Ref.Address) },
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Implementation = extension.StockCitext
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.ObjectRevision++
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Signature = "25,25"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreePath) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Result = 16
					n.RoutineReference = &foreign
				},
			} {
				bad := call
				corrupt(&bad)
				if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
					t.Fatalf("forged captured provider admitted: role=%d corruption=%d", i, corruption)
				}
				if !reflect.DeepEqual(ltreePathOwnershipCall(t, node), call) {
					t.Fatal("failed admission mutated captured source")
				}
			}
			other := ltreePathOwnershipCall(t, project.Expressions[(i+1)%len(project.Expressions)])
			if call.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("different role grouped")
			}
		}
	}
}
func TestLtreePathStoredRemapOwnership(t *testing.T) {
	source, target := engine.New(), engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss, ts := ltreePathOwnershipSetup(t, source), ltreePathOwnershipSetup(t, target)
	enginetest.SessionSQL(t, ts, `CREATE EXTENSION "uuid-ossp"`)
	bound := ltreePathOwnershipBinding(t, source, ss, false)
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
		return v.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "lh_owner"})
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
		old, next := ltreePathOwnershipCall(t, bound.Expressions[i]), ltreePathOwnershipCall(t, node)
		if next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Formals == old.Formals || next.Count != old.Count || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Operation != old.Operation || next.Usage.Type != types(old.Usage.Type) {
			t.Fatal("remap lost exact ownership")
		}
		for i, formal := range old.Formals[:old.Count] {
			if next.Formals[i] != types(formal) {
				t.Fatal("remap lost active formal", i)
			}
		}
		captured, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || captured == old.RoutineReference || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()), captured) {
			t.Fatal("remapped captured declaration", err)
		}
		if old.Ref.Domain() != sv.Owner().Domain || ltreePathOwnershipCall(t, bound.Expressions[i]).Formals != old.Formals {
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
					input = ltreePathOwnershipBinding(t, source, ss, true)
				}
				survivor := mapper.Fresh()
				survivor.Catalog = current
				root, err := survivor.Nested(input)
				if err != nil {
					t.Fatal("surviving destination remap", absent, foreign, direct, err)
				}
				result := root.(*plan.ProjectNode)
				for i, node := range result.Expressions {
					old, next := ltreePathOwnershipCall(t, input.Expressions[i]), ltreePathOwnershipCall(t, node)
					if next.Ref != reference(old.Ref) || next.Member != old.Member || next.Operation != old.Operation || next.Count != old.Count || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence {
						t.Fatal("surviving remap changed capture")
					}
					for j, formal := range old.Formals[:old.Count] {
						if next.Formals[j] != types(formal) {
							t.Fatal("surviving remap lost formal", i, j)
						}
					}
					if _, _, err := stored.RetagTypes(current, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
						t.Fatal("surviving remap not current", err)
					}
				}
			}
		}
	}
	for _, input := range []*plan.ProjectNode{bound, ltreePathOwnershipBinding(t, source, ss, true)} {
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
		for _, slot := range []int{3} {
			scalar := sv.MustType(se.Installation.Members[slot]).SQLType
			candidates := []*sqltype.Descriptor{scalar}
			array, ok := sv.LookupArray(scalar)
			if !ok {
				t.Fatal("genuine LCA array companion missing")
			}
			candidates = append(candidates, array)
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
		broken := *tv.MustProcedure(te.Installation.Members[extension.LtreePathProcedureStart+int(sqltype.LtreeLCA2)])
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

// Keep the genuine sealed installation and nominal graph; only current
// reciprocal membership and the optional live origin header change.
func ltreePathQuerySurvivorView(t *testing.T, view catalog.Read, origin, receiver *catalog.Extension, absent, foreign bool) catalog.Read {
	t.Helper()
	state := catalog.CloneState(view.State())
	received, detached := *receiver, *origin
	received.Members = append([]ids.ObjectAddress(nil), receiver.Members...)
	detached.Members = nil
	for _, address := range origin.Members {
		d := state.Get(address)
		membership := ids.ObjectAddress{}
		if foreign {
			membership = receiver.Address()
			received.Members = append(received.Members, address)
		}
		next, err := catalog.WithExtensionMembership(d, membership, d.Revision())
		if err != nil {
			t.Fatal(err)
		}
		state.SetDescriptor(address, next)
	}
	state.SetDescriptor(received.Address(), &received)
	state.SetDescriptor(detached.Address(), &detached)
	if absent {
		state.UnbindName(catalog.DescriptorNameKey(origin))
		state.RemoveDescriptor(origin.Address())
	}
	return view.WithState(state)
}

func TestLtreePathStoredSurvivorAuthority(t *testing.T) {
	e := engine.New()
	s := ltreePathOwnershipSetup(t, e)
	enginetest.SessionSQL(t, s, `CREATE EXTENSION "uuid-ossp"`)
	view := e.CatalogRead()
	origin := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, direct := range []bool{false, true} {
		project := ltreePathOwnershipBinding(t, e, s, direct)
		for _, absent := range []bool{false, true} {
			for _, foreign := range []bool{false, true} {
				current := ltreePathQuerySurvivorView(t, view, origin, receiver, absent, foreign)
				for _, node := range project.Expressions {
					call := ltreePathOwnershipCall(t, node)
					rebound, changed, err := stored.RetagTypes(current, node, identity)
					var expected expr.Expr = call
					selected, operator := node.(plan.SelectedOperatorExpr)
					if operator {
						selected.Operator, _ = current.Operator(selected.Ref.Address)
						selected.Provider = call
						expected = selected
					}
					if err != nil || changed != operator || !reflect.DeepEqual(rebound, expected) {
						t.Fatal("surviving path carrier changed", call.Operation, absent, foreign, direct, changed, err)
					}
					if !reflect.DeepEqual(ltreePathOwnershipCall(t, node), call) {
						t.Fatal("survivor mutated source")
					}
					for _, cause := range []string{"principal", "provider", "declaration", "seal", "missing", "formal_generation", "result_generation"} {
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
						case "formal_generation", "result_generation":
							typ := call.Formals[0]
							if cause == "result_generation" {
								typ = call.Usage.Type
							}
							address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
							base := *current.MustType(address)
							generation := *base.SQLType
							base.SQLType = &generation
							state.SetDescriptor(address, &base)
						}
						if cause != "missing" && cause != "formal_generation" && cause != "result_generation" {
							state.SetDescriptor(bad.Address(), &bad)
						}
						if _, _, err := stored.RetagTypes(current.WithState(state), node, identity); err == nil {
							t.Fatal("invalid current path authority admitted", cause, call.Operation)
						}
					}
				}
			}
		}
	}
}
