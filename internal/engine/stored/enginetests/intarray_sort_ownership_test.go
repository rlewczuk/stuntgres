package enginetests_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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

func intarraySortOwnershipBinding(t *testing.T, e *core.Engine, s *session.Session, direct bool) *plan.ProjectNode {
	t.Helper()
	query := "SELECT public.sort(a),public.sort(a,d),public.sort_asc(a),public.sort_desc(a),public.uniq(a),public.sort(a),public.sort(a,d),public.sort_asc(a),public.sort_desc(a),public.uniq(a) FROM ia_owner"
	if direct {
		query = "SELECT public.sort(a::integer[]),public.sort(a::integer[],d::text),public.sort_asc(a::integer[]),public.sort_desc(a::integer[]),public.uniq(a::integer[]),public.sort(a::integer[]),public.sort(a::integer[],d::text),public.sort_asc(a::integer[]),public.sort_desc(a::integer[]),public.uniq(a::integer[]) FROM ia_owner"
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
	if !ok || len(project.Expressions) != 10 {
		t.Fatal("selected projection absent")
	}
	return project
}
func intarraySortOwnershipCall(t *testing.T, node expr.Expr) functions.IntarraySort {
	t.Helper()

	call, ok := node.(functions.IntarraySort)
	if !ok {
		t.Fatalf("selected provider absent: %T", node)
	}
	return call
}
func intarraySortOwnershipSetup(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION intarray")
	enginetest.SessionSQL(t, s, "CREATE TABLE ia_owner(a integer[],b integer[],n integer,d text)")
	return s
}
func TestIntarraySortOwnership(t *testing.T) {
	t.Run("exact_dependencies", intarraySortDependencyOwnership)
	t.Run("stored_remap", intarraySortStoredRemap)
	e := engine.New()
	s := intarraySortOwnershipSetup(t, e)
	view := e.CatalogRead()
	for _, direct := range []bool{false, true} {
		project := intarraySortOwnershipBinding(t, e, s, direct)
		identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
		for i, node := range project.Expressions {
			call := intarraySortOwnershipCall(t, node)
			if call.Operation != sqltype.IntarraySortOperation(i%5) || call.Member != uint8(39+i%5) || call.Info.Occurrence == 0 || call.Ref.Domain() != view.Owner().Domain {
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
				if err != nil || !ok || capture.Count != call.Operation.Arity() || !reflect.DeepEqual(capture.Args, call.Children()) {
					t.Fatal("capture lost source order", err)
				}
			}
			spec, err := call.Render()
			if err != nil || len(spec.Args) != call.Operation.Arity() || spec.Args[0].TransportType != call.Formals[0].Usage() {
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
			for _, corrupt := range []func(*functions.IntarraySort){
				func(n *functions.IntarraySort) { n.Member++ }, func(n *functions.IntarraySort) { n.Operation ^= 1 },
				func(n *functions.IntarraySort) { foreign := *n.Formals[0]; n.Formals[0] = &foreign },
				func(n *functions.IntarraySort) { foreign := *n.Usage.Type; n.Usage.Type = &foreign },
				func(n *functions.IntarraySort) {
					n.Usage.Type = nil
				},
				func(n *functions.IntarraySort) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Display.Source = "bqarr_out"
					n.RoutineReference = &foreign
				},
				func(n *functions.IntarraySort) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					copied := *foreign.Origin.Installation
					foreign.Origin = &catalog.InstalledOrigin{Installation: &copied, Declaration: foreign.Origin.Declaration}
					n.RoutineReference = &foreign
				},
				func(n *functions.IntarraySort) {
					foreign := *n.RoutineReference.(*catalog.Procedure)
					foreign.Owner = (&authorization.Domain{}).Principal(foreign.Owner.ID())
					n.RoutineReference = &foreign
				},
				func(n *functions.IntarraySort) { n.Ref.Epoch++ },
			} {
				bad := call
				corrupt(&bad)
				if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
					t.Fatal("forged captured provider admitted")
				}
				if !reflect.DeepEqual(intarraySortOwnershipCall(t, node), call) {
					t.Fatal("failed admission mutated captured source")
				}
			}
			other := intarraySortOwnershipCall(t, project.Expressions[(i+1)%len(project.Expressions)])
			if call.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("different role grouped")
			}
		}
	}
}
func intarraySortStoredRemap(t *testing.T) {
	t.Run("detached_originals", intarraySortDetachedStoredRemap)
	source, target := engine.New(), engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss, ts := intarraySortOwnershipSetup(t, source), intarraySortOwnershipSetup(t, target)
	bound := intarraySortOwnershipBinding(t, source, ss, false)
	_ = ts
	sv, tv := source.CatalogRead(), target.CatalogRead()
	installed := func(v catalog.Read) *catalog.Extension {
		return v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
	}
	se, te := installed(sv), installed(tv)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, a := range se.Installation.Members {
		mapping[a] = te.Installation.Members[i]
	}
	for _, slot := range []int{0, 3} {
		a, b := sv.MustType(se.Installation.Members[slot]), tv.MustType(te.Installation.Members[slot])
		mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(a.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(b.Array)}
	}
	relation := func(v catalog.Read) ids.ObjectAddress {
		return v.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ia_owner"})
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
		if err := extension.StockIntarray.ValidateProcedure(sv, old); err != nil {
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
		old, next := intarraySortOwnershipCall(t, bound.Expressions[i]), intarraySortOwnershipCall(t, node)
		if next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Usage.Type != types(old.Usage.Type) || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || next.Operation != old.Operation {
			t.Fatal("remap lost exact ownership")
		}
		for j := range old.Operation.Arity() {
			if next.Formals[j] != types(old.Formals[j]) || next.Args[j].ResultUsage().Type != types(old.Args[j].ResultUsage().Type) || expr.InfoOf(next.Args[j]).Source != expr.InfoOf(old.Args[j]).Source || expr.InfoOf(next.Args[j]).Occurrence != expr.InfoOf(old.Args[j]).Occurrence {
				t.Fatal("active operand/formal ownership lost", i, j)
			}
		}
		captured, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || captured == old.RoutineReference || !exec.SameRoutineDeclaration(tv.MustProcedure(captured.Address()), captured) {
			t.Fatal("remapped captured declaration", err)
		}
		if old.Ref.Domain() != sv.Owner().Domain || intarraySortOwnershipCall(t, bound.Expressions[i]).Formals != old.Formals {
			t.Fatal("source changed")
		}
	}

	for _, input := range []*plan.ProjectNode{bound, intarraySortOwnershipBinding(t, source, ss, true)} {
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
		for _, id := range []sqltype.ID{1007, sqltype.Int4, sqltype.Text} {
			actual, ok := tv.LookupType(id)
			if !ok {
				t.Fatalf("missing destination type %d", id)
			}
			foreign := *actual
			replacement, mappedID := &foreign, id
			if id == sqltype.Int4 {
				// The policy maps descriptor leaves; it does not recursively
				// rebuild builtin arrays from a separately mapped int4. Put
				// the foreign element in the active mapped array itself.
				array, ok := tv.LookupType(1007)
				if !ok {
					t.Fatal("missing destination integer array")
				}
				var err error
				replacement, err = sqltype.Array(array.ID(), array.Name(), &foreign)
				if err != nil {
					t.Fatal(err)
				}
				if replacement.Element() != &foreign || replacement.Element() == array.Element() {
					t.Fatal("element-generation corruption was not installed")
				}
				mappedID = array.ID()
			}
			bad := mapper.Fresh()
			reached := false
			bad.Type = func(old *sqltype.Descriptor) *sqltype.Descriptor {
				next := types(old)
				if next.ID() == mappedID {
					reached = true
					return replacement
				}
				return next
			}
			_, err := bad.Nested(input)
			if !reached {
				t.Fatalf("corrupted active descriptor %d was not reached", id)
			}
			if err == nil {
				t.Fatalf("foreign same-OID target generation %d admitted", id)
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
		broken := *tv.MustProcedure(te.Installation.Members[extension.IntarraySortProcedureStart])
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

func intarraySortDetachedStoredRemap(t *testing.T) {
	e := engine.New()
	s := intarraySortOwnershipSetup(t, e)
	project := intarraySortOwnershipBinding(t, e, s, true)
	enginetest.SessionSQL(t, s, "CREATE SCHEMA detached_sort")
	for _, signature := range []string{"sort(integer[])", "sort(integer[],text)", "sort_asc(integer[])", "sort_desc(integer[])", "uniq(integer[])"} {
		name := strings.SplitN(signature, "(", 2)[0]
		args := signature[len(name):]
		enginetest.SessionSQL(t, s, "ALTER EXTENSION intarray DROP FUNCTION public."+signature)
		enginetest.SessionSQL(t, s, "ALTER FUNCTION public."+signature+" RENAME TO original_"+name)
		enginetest.SessionSQL(t, s, "ALTER FUNCTION public.original_"+name+args+" SET SCHEMA detached_sort")
	}
	enginetest.SessionSQL(t, s, "DROP EXTENSION intarray CASCADE")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION intarray")
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for _, node := range project.Expressions {
		before := intarraySortOwnershipCall(t, node)
		next, _, err := stored.RetagTypes(view, node, identity)
		if err != nil {
			t.Fatal("detached original rejected after new installation", err)
		}
		after := intarraySortOwnershipCall(t, next)
		if after.Ref != before.Ref || after.Ref.Address == installation.Members[after.Member] {
			t.Fatal("same-name installation replaced original")
		}
		children := make([]expr.Expr, after.Operation.Arity())
		for i := range children {
			children[i] = plan.LiteralExpr{Info: expr.Info{Usage: after.Formals[i].Usage()}, Value: sqltype.Null(after.Formals[i])}
		}
		info := after.Info
		info.Property = expr.Properties{Volatility: expr.Immutable}
		literal, err := after.Rebuild(info, children)
		if err != nil {
			t.Fatal(err)
		}
		sql, err := (stored.ScalarRenderer{}).SQL(t.Context(), view, literal)
		if err != nil || !strings.Contains(sql, "detached_sort") || !strings.Contains(sql, "original_"+extension.IntarraySortProcedureSymbol(after.Operation)) {
			t.Fatal("rendering lost current original spelling", sql, err)
		}
	}
}
