package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestNamespaceIdentityStagedAuthorityAndFinishVeto(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-stage", true: "existing-stage"}[prior], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, &schemaDefaults{path: "pg_temp,public"})
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			failure := error(context.Canceled)
			if prior {
				failure = sqlError("22012", "division by zero")
			}
			defer scope.Finish(context.Canceled)
			base := e.catalog.state
			e.mu.Lock()
			var carried *namespaceDescriptor
			if prior {
				scope.catalog = newCatalogChanges()
				carried = &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}, ObjectName: "private_carried", Owner: e.authorizationDomain.Principal(10), ObjectRevision: 1}
				e.catalogChangeLocked(scope.catalog, carried.Address(), carried)
			}
			auth, path, err := scope.schemaInquiryPathLocked(e)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			id := temporaryNamespace(auth)
			candidate := scope.catalog
			if id == (ids.ObjectAddress{}) || len(path) != 2 || path[0] != id || candidate == nil || e.catalog.state != base || s.temporaryNamespace != (ids.ObjectAddress{}) {
				e.mu.Unlock()
				t.Fatal("private first-use candidate", id, path)
			}
			if prior && candidate.descriptors[carried.Address()] != carried {
				e.mu.Unlock()
				t.Fatal("lost prior candidate")
			}
			before := len(candidate.descriptors)
			auth2, path2, err := scope.schemaInquiryPathLocked(e)
			if err != nil || temporaryNamespace(auth2) != id || len(path2) != len(path) || len(candidate.descriptors) != before || candidate != scope.catalog {
				e.mu.Unlock()
				t.Fatal("repeated inquiry allocated or lost overlay", err)
			}
			oidType, _ := auth.catalog.LookupType(sqltype.Oid)
			boolType, _ := auth.catalog.LookupType(sqltype.Bool)
			var toast ids.ObjectAddress
			for a, d := range candidate.descriptors {
				if n, ok := d.(*namespaceDescriptor); ok && n.TemporaryToast && n.Temporary == s.token {
					toast = a
				}
			}
			e.mu.Unlock()
			getter := namespaceInquiryExpression{Info: expr.Info{Usage: oidType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, kind: myTempNamespace}
			value, err := getter.Eval(&EvalContext{Context: context.Background(), statementScope: scope})
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.OID()
			if err != nil || ids.OID(got) != id.ObjectID {
				t.Fatal("getter missed statement overlay", got, err)
			}
			input, err := sqltype.OIDValue(oidType, uint32(toast.ObjectID))
			if err != nil || toast == (ids.ObjectAddress{}) {
				t.Fatal("TOAST pair", err)
			}
			classifier := namespaceInquiryExpression{Info: expr.Info{Usage: boolType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, kind: otherTempNamespace, arg: literalExpression{Info: expr.Info{Usage: oidType.Usage()}, value: input}}
			value, err = classifier.Eval(&EvalContext{Context: context.Background(), statementScope: scope})
			if err != nil {
				t.Fatal(err)
			}
			other, err := value.Boolean()
			if err != nil || other {
				t.Fatal("own staged TOAST classified as peer", other, err)
			}
			if err := scope.Finish(failure); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if e.catalog.state != base || s.temporaryNamespace != (ids.ObjectAddress{}) || s.transactions.catalog != nil {
				t.Fatal("Finish veto published namespace")
			}
		})
	}
}

func TestNamespaceIdentityNoPendingFastPath(t *testing.T) {
	e := New()
	s := sessionStart(t, e, &schemaDefaults{path: "public,pg_temp"})
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.mu.Lock()
	defer e.mu.Unlock()
	base, next := e.catalog.state, e.catalog.nextOID
	_, path, err := scope.schemaInquiryPathLocked(e)
	if err != nil || len(path) != 1 || scope.catalog != nil || e.catalog.state != base || e.catalog.nextOID != next {
		t.Fatal("no-pending path introduced catalog work", path, err)
	}
}

func TestNamespaceIdentityStoredValidation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	oidType, _ := view.LookupType(sqltype.Oid)
	boolType, _ := view.LookupType(sqltype.Bool)
	n := namespaceInquiryExpression{Info: expr.Info{Usage: oidType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, kind: myTempNamespace}
	if err := n.validateStored(); err != nil {
		t.Fatal(err)
	}
	bad := n
	bad.Property.Volatility = expr.Immutable
	if bad.validateStored() == nil {
		t.Fatal("lost stable volatility accepted")
	}
	bad = n
	bad.Usage = boolType.Usage()
	if bad.validateStored() == nil {
		t.Fatal("wrong result accepted")
	}
	n.kind = otherTempNamespace
	n.Usage = boolType.Usage()
	value, _ := sqltype.OIDValue(oidType, 0)
	n.arg = literalExpression{Info: expr.Info{Usage: oidType.Usage()}, value: value}
	if err := n.validateStored(); err != nil {
		t.Fatal(err)
	}
	n.arg = nil
	if n.validateStored() == nil {
		t.Fatal("missing operand accepted")
	}
}

func TestNamespaceIdentityTypedBinding(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	query := domainUnitBindSelect(t, e, op, "SELECT pg_my_temp_schema(),pg_is_other_temp_schema($1)")
	expressions := query.Root.(*projectNode).expressions
	getter, ok := expressions[0].(namespaceInquiryExpression)
	if !ok || getter.kind != myTempNamespace || getter.arg != nil || getter.ResultUsage().Type.ID() != sqltype.Oid || getter.Properties().Volatility != expr.Stable {
		t.Fatal("getter typed node", expressions[0])
	}
	classifier, ok := expressions[1].(namespaceInquiryExpression)
	if !ok || classifier.kind != otherTempNamespace || classifier.arg == nil || classifier.arg.ResultUsage().Type.ID() != sqltype.Oid || classifier.ResultUsage().Type.ID() != sqltype.Bool || classifier.Properties().Volatility != expr.Stable || !classifier.Properties().ParameterDependent {
		t.Fatal("classifier typed OID inference", expressions[1])
	}
	if err := getter.validateStored(); err != nil {
		t.Fatal(err)
	}
	if err := classifier.validateStored(); err != nil {
		t.Fatal(err)
	}
}
