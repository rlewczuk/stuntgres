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

func ltreeHierarchyOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session, direct bool) *plan.ProjectNode {
	t.Helper()
	query := "SELECT a @> b,a <@ b FROM lh_owner"
	if direct {
		query = "SELECT public.ltree_isparent(a,b),public.ltree_risparent(a,b) FROM lh_owner"
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
	if !ok || len(project.Expressions) != int(sqltype.LtreeHierarchyOperationCount) {
		t.Fatal("selected projection absent")
	}
	return project
}
func ltreeHierarchyOwnershipCall(t *testing.T, node expr.Expr) functions.LtreeHierarchy {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator == nil || selected.Ref.Address != selected.Operator.Address() {
			t.Fatal("duplicate operator authority")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.LtreeHierarchy)
	if !ok {
		t.Fatalf("selected provider absent: %T", node)
	}
	return call
}
func ltreeHierarchyOwnershipSetup(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree")
	enginetest.SessionSQL(t, s, "CREATE TABLE lh_owner(a ltree,b ltree)")
	return s
}
func TestLtreeHierarchyStoredOwnership(t *testing.T) {
	e := engine.New()
	s := ltreeHierarchyOwnershipSetup(t, e)
	view := e.CatalogRead()
	for _, direct := range []bool{false, true} {
		project := ltreeHierarchyOwnershipBinding(t, e, s, direct)
		identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
		for i, node := range project.Expressions {
			call := ltreeHierarchyOwnershipCall(t, node)
			if call.Operation != sqltype.LtreeHierarchyOperation(i) || call.Member != uint8(extension.LtreeHierarchyProcedureStart+i) || call.Info.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain {
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
			for corruption, corrupt := range []func(*functions.LtreeHierarchy){
				func(n *functions.LtreeHierarchy) { n.Member++ }, func(n *functions.LtreeHierarchy) { n.Operation ^= 1 },
				func(n *functions.LtreeHierarchy) { foreign := *n.Formals[0]; n.Formals[0] = &foreign },
				func(n *functions.LtreeHierarchy) { foreign := *n.Usage.Type; n.Usage.Type = &foreign },
				func(n *functions.LtreeHierarchy) { n.Usage.Type = n.Formals[0] },
				func(n *functions.LtreeHierarchy) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Display.Source = "ltree_out"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeHierarchy) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					copied := *foreign.Origin.Installation
					foreign.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: foreign.Origin.Declaration}
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeHierarchy) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Owner = (&authorization.Domain{}).Principal(foreign.Owner.ID())
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeHierarchy) { n.Ref.Epoch++ },
				func(n *functions.LtreeHierarchy) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.ObjectRevision++
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeHierarchy) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Signature = "25,25"
					n.RoutineReference = &foreign
				},
				func(n *functions.LtreeHierarchy) {
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
				if !reflect.DeepEqual(ltreeHierarchyOwnershipCall(t, node), call) {
					t.Fatal("failed admission mutated captured source")
				}
			}
			other := ltreeHierarchyOwnershipCall(t, project.Expressions[(i+1)%len(project.Expressions)])
			if call.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("different role grouped")
			}
		}
	}
}
func TestLtreeHierarchyStoredRemapOwnership(t *testing.T) {
	source, target := engine.New(), engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss, ts := ltreeHierarchyOwnershipSetup(t, source), ltreeHierarchyOwnershipSetup(t, target)
	bound := ltreeHierarchyOwnershipBinding(t, source, ss, false)
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
		old, next := ltreeHierarchyOwnershipCall(t, bound.Expressions[i]), ltreeHierarchyOwnershipCall(t, node)
		if next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Formals[0] != types(old.Formals[0]) || next.Formals[1] != types(old.Formals[1]) || next.Formals == old.Formals || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Operation != old.Operation {
			t.Fatal("remap lost exact ownership")
		}
		captured, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || captured == old.RoutineReference || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()), captured) {
			t.Fatal("remapped captured declaration", err)
		}
		if old.Ref.Domain() != sv.Owner().Domain || ltreeHierarchyOwnershipCall(t, bound.Expressions[i]).Formals != old.Formals {
			t.Fatal("source changed")
		}
	}
	for _, input := range []*plan.ProjectNode{bound, ltreeHierarchyOwnershipBinding(t, source, ss, true)} {
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
		broken := *tv.MustProcedure(te.Installation.Members[extension.LtreeHierarchyProcedureStart])
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

func TestLtreeHierarchyStoredSurvivorAuthority(t *testing.T) {
	e := engine.New()
	s := ltreeHierarchyOwnershipSetup(t, e)
	enginetest.SessionSQL(t, s, `CREATE EXTENSION "uuid-ossp"`)
	view := e.CatalogRead()
	origin := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
	for _, direct := range []bool{false, true} {
		project := ltreeHierarchyOwnershipBinding(t, e, s, direct)
		for _, foreign := range []bool{false, true} {
			state := catalog.CloneState(view.State())
			received := *receiver
			received.Members = append([]ids.ObjectAddress(nil), receiver.Members...)
			for _, address := range origin.Members {
				descriptor := state.Get(address)
				membership := ids.ObjectAddress{}
				if foreign {
					membership = receiver.Address()
					received.Members = append(received.Members, address)
				}
				next, err := catalog.WithExtensionMembership(descriptor, membership, descriptor.Revision())
				if err != nil {
					t.Fatal(err)
				}
				state.SetDescriptor(address, next)
			}
			if foreign {
				state.SetDescriptor(received.Address(), &received)
			}
			state.UnbindName(catalog.DescriptorNameKey(origin))
			state.RemoveDescriptor(origin.Address())
			current := view.WithState(state)
			for _, node := range project.Expressions {
				identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
				call := ltreeHierarchyOwnershipCall(t, node)
				wantDependency := call.Ref.Address
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					wantDependency = selected.Ref.Address
				}
				references, err := stored.ExpressionDependRefs(context.Background(), current, node)
				if err != nil {
					t.Fatal("surviving hierarchy expression dependencies", err)
				}
				want := map[ids.ObjectAddress]bool{wantDependency: true}
				for _, arg := range call.Args {
					want[arg.(plan.ColumnExpr).Origin.Address] = true
				}
				if len(references) != len(want) {
					t.Fatal("hierarchy dependency set differs", direct, foreign, want, references)
				}
				for _, address := range references {
					if !want[address] {
						t.Fatal("unexpected hierarchy dependency", direct, foreign, address)
					}
					delete(want, address)
				}
				if len(want) != 0 {
					t.Fatal("missing hierarchy dependencies", direct, foreign, want)
				}
				// RetagTypes preserves the authenticated original procedure carrier.
				// Its selected-operator wrapper separately refreshes the current
				// operator descriptor after membership COW, without changing types.
				rebound, changed, err := stored.RetagTypes(current, node, identity)
				if err != nil || changed != !direct {
					t.Fatal("genuine surviving stored hierarchy carrier policy changed", foreign, direct, changed, err)
				}
				var expected expr.Expr = call
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					selected.Provider = call
					selected.Operator, _ = current.Operator(selected.Ref.Address)
					expected = selected
				}
				if !reflect.DeepEqual(rebound, expected) {
					t.Fatal("survivor retag changed declaration, nominal generation, occurrence or current authority", foreign, direct)
				}
				if !reflect.DeepEqual(ltreeHierarchyOwnershipCall(t, node), call) {
					t.Fatal("survivor retag mutated historical source backing")
				}
				for _, cause := range []string{"principal", "generation"} {
					broken := catalog.CloneState(state)
					if cause == "principal" {
						procedure := *current.MustProcedure(call.Ref.Address)
						procedure.Owner = (&authorization.Domain{}).Principal(procedure.Owner.ID())
						broken.SetDescriptor(procedure.Address(), &procedure)
					} else {
						base := *current.MustType(origin.Installation.Members[3])
						generation := *base.SQLType
						base.SQLType = &generation
						broken.SetDescriptor(base.Address(), &base)
					}
					if _, _, err := stored.RetagTypes(view.WithState(broken), node, identity); err == nil {
						t.Fatal("foreign current authority admitted", cause, foreign, direct)
					}
				}
			}
		}
	}
}
