package enginetests_test

import (
	"context"
	"reflect"
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

func hstoreRowsOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session) plan.Node {
	t.Helper()
	ast := enginetest.Statement(t, "SELECT skeys(v),svals(v),(each(v)).key FROM hs_rows_owner")
	op, err := s.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); t.Fatal(err) }
	binding, err := analyze.Describe(op.Context(), e.AnalysisHost(op), ast, core.AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, nil, false)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	return binding.Query.Root
}

func hstoreRowsOwnershipCalls(t *testing.T, root plan.Node) map[uint8]functions.HstoreRows {
	t.Helper()
	calls := make(map[uint8]functions.HstoreRows)
	plan.VisitExecutableQuery(root, func(plan.Node) {}, func(node expr.Expr) {
		plan.WalkExpression(node, func(node expr.Expr) {
			if call, ok := node.(functions.HstoreRows); ok { calls[call.Member] = call }
		})
	})
	if len(calls) != 3 { t.Fatalf("missing actual selected row sources: %d", len(calls)) }
	return calls
}

func TestHstoreRowsStoredReconstruction(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
	enginetest.SessionSQL(t, s, "CREATE TABLE hs_rows_owner(v hstore)")
	root := hstoreRowsOwnershipBinding(t, e, s)
	view := e.CatalogRead()
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, call := range hstoreRowsOwnershipCalls(t, root) {
		if call.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain || call.Ref.Epoch != view.Epoch() { t.Fatal("lost declaration provenance") }
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
			next, err := rebuild(call.Info, call.Children())
			if err != nil || !reflect.DeepEqual(next, call) || !call.SameSourceOccurrence(next) { t.Fatal("reconstruction changed source ownership", err) }
		}
		capture, ok, err := call.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || !reflect.DeepEqual(capture.Args[0], call.Arg) { t.Fatal("capture changed operand", err) }
		retagged, changed, err := stored.RetagTypes(view, call, identity)
		if err != nil || changed || !reflect.DeepEqual(retagged, call) { t.Fatal("identity retag changed source", changed, err) }
		mapped, err := stored.RemapExpression(call, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !reflect.DeepEqual(mapped, call) { t.Fatal("identity remap changed source", err) }
		captured, err := exec.ExpressionRoutineDescriptor(call)
		if err != nil || captured != call.RoutineReference { t.Fatal("captured declaration changed", err) }
		for _, mutate := range []func(*functions.HstoreRows){
			func(n *functions.HstoreRows) { n.Member++ },
			func(n *functions.HstoreRows) { n.Ref.Epoch++ },
			func(n *functions.HstoreRows) { other := *n.Formal; n.Formal = &other },
			func(n *functions.HstoreRows) { other := *captured; other.Display.Source = "hstore_out"; n.RoutineReference = &other },
			func(n *functions.HstoreRows) { other := *captured; other.Installed.ReturnsSet = false; n.RoutineReference = &other },
		} {
			bad := call
			mutate(&bad)
			if _, _, err := stored.RetagTypes(view, bad, identity); err == nil { t.Fatal("forged/stale source authority accepted") }
		}
	}
}

func TestHstoreRowsStoredRemapOwnership(t *testing.T) {
	source, target := engine.New(), engine.New()
	ss, ts := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, target, nil)
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	for _, s := range []*session.Session{ss, ts} {
		enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
		enginetest.SessionSQL(t, s, "CREATE TABLE hs_rows_owner(v hstore)")
	}
	root := hstoreRowsOwnershipBinding(t, source, ss)
	original := hstoreRowsOwnershipCalls(t, root)
	sv, tv := source.CatalogRead(), target.CatalogRead()
	installed := func(v catalog.Read) *catalog.Extension { return v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"})) }
	se, te := installed(sv), installed(tv)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, address := range se.Members { mapping[address] = te.Members[i] }
	relation := func(v catalog.Read) ids.ObjectAddress { return v.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "hs_rows_owner"}) }
	mapping[relation(sv)] = relation(tv)
	address := func(old ids.ObjectAddress) ids.ObjectAddress { if next, ok := mapping[old]; ok { return next }; return old }
	reference := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) { return old }
		return tv.Owner().Domain.Ref(tv.Epoch(), address(old.Address))
	}
	sourceBase, targetBase := sv.MustType(se.Members[0]).SQLType, tv.MustType(te.Members[0]).SQLType
	// Match the production remap entry points: OID 2249 is not authority to
	// replace each's occurrence-owned OUT record with the generic record type.
	types := analyze.RecordTypeMapper(func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil { return nil }
		if old == sourceBase { return targetBase }
		if next, ok := tv.LookupType(old.ID()); ok { return next }
		return old
	})
	principal := func(old authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		return tv.Owner().Principals.Principal(old.ID()), nil
	}
	principals := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(old authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(old)
	}})
	procedure := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		if old == nil { return nil, nil }
		if err := extension.StockHstore.ValidateProcedure(sv, old); err != nil { return nil, err }
		mapped := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		return principals(mapped)
	}
	mapper := stored.ViewQueryRemapper{Catalog: tv, Ref: reference, Type: types, Procedure: procedure}
	mapped, err := mapper.Nested(root)
	if err != nil { t.Fatal("authorized displaced remap", err) }
	for member, next := range hstoreRowsOwnershipCalls(t, mapped) {
		old := original[member]
		if next.Formal != targetBase || next.Arg.ResultUsage().Type != targetBase || next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Plan.Kind() != old.Plan.Kind() || next.Info.Occurrence != old.Info.Occurrence || next.Properties() != old.Properties() { t.Fatal("lost destination generation or source provenance") }
		captured, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || captured == old.RoutineReference || captured.Address() != te.Members[member] || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()), captured) { t.Fatal("lost remapped captured declaration", err) }
		if _, _, err := stored.RetagTypes(tv, old, types); err == nil { t.Fatal("source-domain call admitted in destination") }
		if !reflect.DeepEqual(hstoreRowsOwnershipCalls(t, root)[member], old) { t.Fatal("remap mutated source") }
	}
}
