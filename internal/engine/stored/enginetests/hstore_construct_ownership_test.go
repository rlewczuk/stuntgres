package enginetests_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

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
)

func hstoreConstructOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session, family string) *plan.ProjectNode {
	t.Helper()
	query := "SELECT hstore(k,w),fetchval(v,k),v->k,v->ARRAY[k] FROM hs_construct_owner"
	count := 4
	if family == "keys" {
		query = "SELECT v ? k,v ?& ARRAY[k],v ?| ARRAY[k],v @> v,v <@ v,akeys(v),avals(v) FROM hs_construct_owner"
		count = 7
	}
	if family == "update" {
		query = "SELECT hs_concat(v,v),delete(v,k),delete(v,ARRAY[k]),delete(v,v),slice(v,ARRAY[k]),v || v,v - k,v - ARRAY[k],v - v FROM hs_construct_owner"
		count = 9
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
	if binding.Query == nil {
		t.Fatal("missing actual bound conversion query")
	}
	project, ok := binding.Query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != count {
		t.Fatal("missing actual bound conversion projection")
	}
	return project
}

func hstoreConstructOwnershipCall(t *testing.T, node expr.Expr) functions.HstoreConstruct {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator == nil || selected.Ref.Address != selected.Operator.Address() {
			t.Fatal("operator wrapper acquired duplicate routine authority")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.HstoreConstruct)
	if !ok {
		t.Fatalf("missing installed conversion provider: %T", node)
	}
	return call
}

func TestHstoreConstructStoredReconstruction(t *testing.T) {
	testHstoreSelectedStoredReconstruction(t, "construct")
}

func TestHstoreKeysStoredOwnership(t *testing.T) {
	t.Run("reconstruction", func(t *testing.T) { testHstoreSelectedStoredReconstruction(t, "keys") })
	t.Run("remap", func(t *testing.T) { testHstoreSelectedStoredRemap(t, "keys") })
}

func testHstoreSelectedStoredReconstruction(t *testing.T, family string) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
	enginetest.SessionSQL(t, s, "CREATE TABLE hs_construct_owner(k text,w text,v hstore)")
	binding := hstoreConstructOwnershipBinding(t, e, s, family)
	view := e.CatalogRead()
	for i, node := range binding.Expressions {
		names := []string{"constructor", "fetch", "scalar_arrow", "array_arrow"}
		members := []uint8{31, 34, 34, 35}
		if family == "keys" {
			names = []string{"exists", "all", "any", "contains", "contained", "keys", "values"}
			members = []uint8{36, 40, 41, 42, 43, 44, 45}
		}
		if family == "update" {
			names = []string{"concat", "scalar", "array", "pair", "slice", "op_concat", "op_scalar", "op_array", "op_pair"}
			members = []uint8{57, 58, 59, 60, 61, 57, 58, 59, 60}
		}
		t.Run(names[i], func(t *testing.T) {
			call := hstoreConstructOwnershipCall(t, node)
			if call.Member != members[i] || call.Info.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain || call.Ref.Epoch != view.Epoch() {
				t.Fatal("binding lost genuine shape/provenance")
			}
			if err := call.ValidateStoredProvider(); err != nil {
				t.Fatal(err)
			}
			children := call.Children()
			for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
				rebuilt, err := reconstruct(call.Info, children)
				if err != nil {
					t.Fatal(err)
				}
				next := rebuilt.(functions.HstoreConstruct)
				if next.Info != call.Info || next.Formals != call.Formals || next.Hstore != call.Hstore || next.Ref != call.Ref || next.Member != call.Member || next.RoutineReference != call.RoutineReference || !reflect.DeepEqual(next.Args, call.Args) || !reflect.DeepEqual(next.Properties(), call.Properties()) || !call.SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
					t.Fatal("reconstruction changed exact source occurrence, selected declaration, operand or properties")
				}
			}
			for _, kind := range []expr.CaptureKind{expr.CaptureFunction, expr.CaptureOperator} {
				captured, ok, err := call.Capture(kind)
				if err != nil || !ok || captured.Count != call.Arity() {
					t.Fatal("capture changed ordered operand ownership", err)
				}
				for i := range call.Arity() {
					operand, err := captured.At(call, i)
					if err != nil || !reflect.DeepEqual(operand, call.Args[i]) {
						t.Fatal("capture lost ordered operand", err)
					}
				}
			}
			identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
			retagged, changed, err := stored.RetagTypes(view, node, identity)
			if err != nil || changed || !reflect.DeepEqual(retagged, node) {
				t.Fatal("identity retag changed provenance", changed, err)
			}
			references, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil || !reflect.DeepEqual(references, node) {
				t.Fatal("identity reference remap changed source", err)
			}
			spec, err := call.Render()
			if err != nil || spec.Name != call.Name || spec.Namespace != call.Namespace || len(spec.Args) != call.Arity() || spec.Args[0].Transport != expr.TransportCast || spec.Args[0].TransportType != call.Formals[0].Usage() {
				t.Fatal("qualified reconstruction lost formal replay authority", err)
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured != call.RoutineReference {
				t.Fatal("selected declaration lost", err)
			}
			for _, mutate := range []func(*functions.HstoreConstruct){
				func(n *functions.HstoreConstruct) { n.Member = 37 },
				func(n *functions.HstoreConstruct) { foreign := *n.Hstore; n.Hstore = &foreign },
				func(n *functions.HstoreConstruct) { foreign := *n.Usage.Type; n.Usage.Type = &foreign },
				func(n *functions.HstoreConstruct) {
					foreign := *captured
					foreign.Display.Source = "hstore_out"
					n.RoutineReference = &foreign
				},
				func(n *functions.HstoreConstruct) { n.Ref.Epoch++ },
			} {
				bad := call
				mutate(&bad)
				if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
					t.Fatal("invalid captured member/generation authority accepted")
				}
				if call.Info != hstoreConstructOwnershipCall(t, node).Info || call.Formals != hstoreConstructOwnershipCall(t, node).Formals {
					t.Fatal("failed validation mutated source")
				}
			}
		})
	}
}

func TestHstoreConstructStoredRemapOwnership(t *testing.T) {
	testHstoreSelectedStoredRemap(t, "construct")
}

func testHstoreSelectedStoredRemap(t *testing.T, family string) {
	t.Helper()
	source, target := engine.New(), engine.New()
	ss, ts := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, target, nil)
	// Advance the real destination allocator so numeric identity cannot accidentally
	// stand in for source-authorized member and reference reprovenance.
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*session.Session{ss, ts} {
		enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
		enginetest.SessionSQL(t, s, "CREATE TABLE hs_construct_owner(k text,w text,v hstore)")
	}
	bound := hstoreConstructOwnershipBinding(t, source, ss, family)
	original := append([]expr.Expr(nil), bound.Expressions...)
	sv, tv := source.CatalogRead(), target.CatalogRead()
	installed := func(view catalog.Read) *catalog.Extension {
		return view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	}
	se, te := installed(sv), installed(tv)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, a := range se.Members {
		mapping[a] = te.Members[i]
	}
	sourceBase, targetBase := sv.MustType(se.Members[0]), tv.MustType(te.Members[0])
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceBase.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(targetBase.Array)}
	relation := func(view catalog.Read) ids.ObjectAddress {
		return view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "hs_construct_owner"})
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
		if old == sourceBase.SQLType {
			return targetBase.SQLType
		}
		if next, ok := tv.LookupType(old.ID()); ok {
			return next
		}
		return old
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
		if err := extension.StockHstore.ValidateProcedure(sv, old); err != nil {
			return nil, err
		}
		mapped := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		return principals(mapped)
	}
	remap := func(view catalog.Read, typ func(*sqltype.Descriptor) *sqltype.Descriptor) (plan.Node, error) {
		mapper := stored.ViewQueryRemapper{Catalog: view, Ref: reference, Type: typ, Procedure: procedure}
		return mapper.Nested(bound)
	}
	mapped, err := remap(tv, types)
	if err != nil {
		t.Fatal("authorized installed remap", err)
	}
	project, ok := mapped.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != len(original) {
		t.Fatal("lost remapped conversion projection")
	}
	for i, node := range project.Expressions {
		next := hstoreConstructOwnershipCall(t, node)
		old := hstoreConstructOwnershipCall(t, original[i])
		expected := reference(old.Ref)
		if next.Hstore != targetBase.SQLType || next.Formals[0] != types(old.Formals[0]) || next.Args[0].ResultUsage().Type != types(old.Args[0].ResultUsage().Type) || next.ResultUsage().Type == old.ResultUsage().Type || next.ResultUsage().Type != types(old.ResultUsage().Type) || next.Ref != expected || next.Ref == old.Ref || next.Member != old.Member || next.Formals[1] != types(old.Formals[1]) || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Properties() != old.Properties() {
			t.Fatal("authorized remap lost target generations or exact source provenance")
		}
		current, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || current == old.RoutineReference || current.Address() != te.Members[next.Member] || !exec.SameRoutineDeclaration(tv.MustProcedure(current.Address()), current) || current.Revision() != old.RoutineReference.(*catalog.Procedure).Revision() {
			t.Fatal("remap substituted current declaration instead of reprovenancing original", err)
		}
		if selected, ok := node.(plan.SelectedOperatorExpr); ok {
			prior := original[i].(plan.SelectedOperatorExpr)
			if selected.Ref != reference(prior.Ref) || selected.Operator != tv.Get(reference(prior.Ref).Address) || selected.RoutineReference != nil {
				t.Fatal("operator remap lost wrapper identity or duplicated admission")
			}
		}
		if sourceBase.SQLType != old.Hstore || old.Ref.Domain() != sv.Owner().Domain || old.ResultUsage().Type == next.ResultUsage().Type || !reflect.DeepEqual(bound.Expressions[i], original[i]) {
			t.Fatal("remap mutated immutable source graph")
		}
	}

	for _, cause := range []string{"current_member", "current_membership_link", "current_source", "current_formal", "result_generation", "source_domain"} {
		t.Run(cause, func(t *testing.T) {
			candidate := catalog.CloneState(tv.State())
			targetExt := *te
			targetExt.Members = slices.Clone(te.Members)
			switch cause {
			case "current_member":
				targetExt.Members[extension.HstoreTextConstructorSlot], targetExt.Members[extension.HstoreFetchSlot] = targetExt.Members[extension.HstoreFetchSlot], targetExt.Members[extension.HstoreTextConstructorSlot]
				candidate.SetDescriptor(targetExt.Address(), &targetExt)
			case "current_membership_link":
				p := *tv.MustProcedure(te.Installation.Members[hstoreConstructOwnershipCall(t, original[0]).Member])
				p.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999999}
				candidate.SetDescriptor(p.Address(), &p)
			case "current_source":
				p := *tv.MustProcedure(te.Installation.Members[hstoreConstructOwnershipCall(t, original[0]).Member])
				p.Display.Source = "hstore_out"
				candidate.SetDescriptor(p.Address(), &p)
			case "current_formal":
				p := *tv.MustProcedure(te.Installation.Members[hstoreConstructOwnershipCall(t, original[len(original)-1]).Member])
				p.Signature = "25"
				candidate.SetDescriptor(p.Address(), &p)
			}
			typeMap := types
			if cause == "result_generation" {
				foreign := *types(hstoreConstructOwnershipCall(t, original[0]).Usage.Type)
				typeMap = func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d == sourceBase.SQLType {
						return &foreign
					}
					return types(d)
				}
			}
			mapper := stored.ViewQueryRemapper{Catalog: target.Catalog.Read(candidate), Ref: reference, Type: typeMap, Procedure: procedure}
			if cause == "source_domain" {
				mapper.Ref = func(ref ids.ObjectRef) ids.ObjectRef {
					if ref == (ids.ObjectRef{}) {
						return ref
					}
					next := reference(ref)
					return sv.Owner().Domain.Ref(next.Epoch, next.Address)
				}
			}
			if _, err := mapper.Nested(bound); cause == "current_member" {
				if err != nil {
					t.Fatal("lawful membership permutation changed released authority", err)
				}
			} else if err == nil {
				t.Fatal("unauthorized source/current remap accepted")
			}
			if tv.MustExtension(te.Address()) != te || sv.MustExtension(se.Address()) != se {
				t.Fatal("rejected remap mutated published catalogs")
			}
			for i, node := range bound.Expressions {
				if !reflect.DeepEqual(node, original[i]) {
					t.Fatal("rejected remap mutated original expressions")
				}
			}
		})
	}
}
