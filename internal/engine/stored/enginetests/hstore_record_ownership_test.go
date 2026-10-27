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

func hstoreRecordOwnerBinding(t *testing.T, e *core.Engine, s *session.Session) *plan.ProjectNode {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := enginetest.Statement(t, "SELECT hstore(r),populate_record(r,h),r #= h FROM hs_record_owner")
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
	if !ok || len(project.Expressions) != 3 {
		t.Fatal("missing actual record binding")
	}
	return project
}
func hstoreRecordOwnerCall(t *testing.T, node expr.Expr) functions.HstoreRecord {
	t.Helper()
	if selected, ok := node.(plan.SelectedOperatorExpr); ok {
		if selected.RoutineReference != nil || selected.Operator.Function == 0 {
			t.Fatal("duplicate/missing operator admission")
		}
		node = selected.Provider
	}
	call, ok := node.(functions.HstoreRecord)
	if !ok {
		t.Fatalf("wrong record provider %T", node)
	}
	return call
}
func hstoreRecordOwnerSetup(t *testing.T, e *core.Engine) *session.Session {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION hstore")
	enginetest.SessionSQL(t, s, "CREATE TYPE hs_owner_pair AS (i integer,n text)")
	enginetest.SessionSQL(t, s, "CREATE TABLE hs_record_owner(r hs_owner_pair,h hstore)")
	return s
}
func TestHstoreRecordStoredAuthority(t *testing.T) {
	e := engine.New()
	s := hstoreRecordOwnerSetup(t, e)
	bound := hstoreRecordOwnerBinding(t, e, s)
	view := e.CatalogRead()
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for i, node := range bound.Expressions {
		call := hstoreRecordOwnerCall(t, node)
		procedure, err := exec.ExpressionRoutineDescriptor(call)
		if err != nil {
			t.Fatal(err)
		}
		signature := catalog.ProcedureSignature("2249")
		result := ids.OID(call.Hstore.ID())
		if i > 0 {
			signature = catalog.ProcedureSignature(fmt.Sprintf("2283,%d", call.Hstore.ID()))
			result = 2283
		}
		if procedure.Signature != signature || procedure.Result != result || call.Formals[0].ID() == 2249 || call.Info.Occurrence == 0 {
			t.Fatal("declared and effective authorities conflated")
		}
		next, changed, err := stored.RetagTypes(view, node, identity)
		if err != nil || changed || !reflect.DeepEqual(next, node) {
			t.Fatal("identity retag", err)
		}
		next, err = stored.RemapExpression(node, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
		if err != nil || !reflect.DeepEqual(next, node) {
			t.Fatal("reference remap changed original occurrence", err)
		}
		for _, mutate := range []func(*functions.HstoreRecord){
			func(c *functions.HstoreRecord) { c.Member = 31 },
			func(c *functions.HstoreRecord) { foreign := *c.Hstore; c.Hstore = &foreign },
			func(c *functions.HstoreRecord) { foreign := *c.Formals[0]; c.Formals[0] = &foreign },
			func(c *functions.HstoreRecord) { c.Ref.Epoch++ },
			func(c *functions.HstoreRecord) {
				p := *procedure
				p.Signature = catalog.ProcedureSignature(fmt.Sprintf("%d", c.Formals[0].ID()))
				c.RoutineReference = &p
			},
			func(c *functions.HstoreRecord) {
				p := *procedure
				p.Display.Source = "hstore_from_text"
				c.RoutineReference = &p
			},
		} {
			bad := call
			mutate(&bad)
			if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
				t.Fatal("forged original/effective record authority accepted")
			}
		}
		if !reflect.DeepEqual(hstoreRecordOwnerCall(t, bound.Expressions[i]), call) {
			t.Fatal("failed validation changed original graph")
		}
	}
}
func TestHstoreRecordStoredRemap(t *testing.T) {
	source, target := engine.New(), engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss := hstoreRecordOwnerSetup(t, source)
	_ = hstoreRecordOwnerSetup(t, target)
	bound := hstoreRecordOwnerBinding(t, source, ss)
	sv, tv := source.CatalogRead(), target.CatalogRead()
	se := sv.MustExtension(sv.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	te := tv.MustExtension(tv.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, a := range se.Members {
		mapping[a] = te.Members[i]
	}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for _, key := range []ids.NameKey{{ClassID: ids.TypeClassID, Namespace: public, Name: "hs_owner_pair"}, {ClassID: ids.TypeClassID, Namespace: public, Name: "_hs_owner_pair"}, {ClassID: ids.RelationClassID, Namespace: public, Name: "hs_record_owner"}} {
		mapping[sv.NameAddress(key)] = tv.NameAddress(key)
	}
	sourceBase, targetBase := sv.MustType(se.Members[0]), tv.MustType(te.Members[0])
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceBase.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(targetBase.Array)}
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
		a := address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())})
		if d, ok := tv.LookupType(sqltype.ID(a.ObjectID)); ok {
			return d
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
		return principals(extension.RemapDescriptor(old, mapping).(*catalog.Procedure))
	}
	mapper := stored.ViewQueryRemapper{Catalog: tv, Ref: reference, Type: types, Procedure: procedure}
	mapped, err := mapper.Nested(bound)
	if err != nil {
		t.Fatal("authorized record remap", err)
	}
	project := mapped.(*plan.ProjectNode)
	for i, node := range project.Expressions {
		old, next := hstoreRecordOwnerCall(t, bound.Expressions[i]), hstoreRecordOwnerCall(t, node)
		p, err := exec.ExpressionRoutineDescriptor(next)
		if err != nil || next.Ref != reference(old.Ref) || next.Ref == old.Ref || next.Hstore != targetBase.SQLType || next.Formals[0] != types(old.Formals[0]) || next.Usage.Type != types(old.Usage.Type) || next.Info.Source != old.Info.Source || next.Info.Occurrence != old.Info.Occurrence || p == old.RoutineReference || p.Revision() != old.RoutineReference.(*catalog.Procedure).Revision() {
			t.Fatal("remap lost original record provenance", err)
		}
		if !exec.SameRoutineDeclaration(tv.MustProcedure(p.Address()), p) {
			t.Fatal("remapped declared pseudo signature changed")
		}
	}
	for _, cause := range []string{"current_signature", "current_member", "source_domain", "effective_generation"} {
		// Each authority attempt owns a fresh query memo. Copying the successful
		// mapper would return its cached root without visiting forged declarations.
		bad := stored.ViewQueryRemapper{Ref: reference, Type: types, Procedure: procedure}
		state := catalog.CloneState(tv.State())
		switch cause {
		case "current_signature":
			p := *tv.MustProcedure(te.Members[52])
			p.Signature = "25"
			state.SetDescriptor(p.Address(), &p)
		case "current_member":
			ext := *te
			ext.Members = append([]ids.ObjectAddress(nil), te.Members...)
			ext.Members[51], ext.Members[52] = ext.Members[52], ext.Members[51]
			state.SetDescriptor(ext.Address(), &ext)
		case "source_domain":
			bad.Ref = func(old ids.ObjectRef) ids.ObjectRef {
				if old == (ids.ObjectRef{}) {
					return old
				}
				next := reference(old)
				return sv.Owner().Domain.Ref(next.Epoch, next.Address)
			}
		case "effective_generation":
			original := hstoreRecordOwnerCall(t, bound.Expressions[0]).Formals[0]
			foreign := *types(original)
			bad.Type = func(d *sqltype.Descriptor) *sqltype.Descriptor {
				if d == original {
					return &foreign
				}
				return types(d)
			}
		}
		bad.Catalog = target.Catalog.Read(state)
		if _, err := bad.Nested(bound); err == nil {
			t.Fatal("forged record remap accepted", cause)
		}
	}
}
