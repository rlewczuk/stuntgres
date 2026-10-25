package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The ACL SQL function nodes evaluate through ACLRuntime and describe their
// own protocol: children, rebuild, planning hooks and presentation.

func aclTestLiteral(v sqltype.Value) expr.Expr { return temporalTestLiteral(v) }

func aclTestTrue(v sqltype.Value) bool {
	b, err := v.Boolean()
	return err == nil && b
}

func aclTestOID(t *testing.T, oid uint32) expr.Expr {
	t.Helper()
	v, err := sqltype.OIDValue(aclTestTypes[sqltype.Oid], oid)
	if err != nil {
		t.Fatal(err)
	}
	return aclTestLiteral(v)
}

func aclTestText(id sqltype.ID, text string) expr.Expr {
	return aclTestLiteral(sqltype.String(aclTestTypes[id], text))
}

func aclTestDefault(t *testing.T) ACLValue {
	t.Helper()
	n := ACLValue{Kind: ACLDefault, Info: expr.Info{Usage: aclTestTypes[sqltype.ACLItemArray].Usage()}}
	n.Args[0], n.Args[1] = aclTestText(sqltype.Char, "r"), aclTestOID(t, 10)
	return n
}

func TestACLValueEvaluatesThroughRuntime(t *testing.T) {
	runtime := newACLTestRuntime()
	value, err := aclTestDefault(t).Eval(runtime)
	if err != nil || value.ArrayLen() != 1 {
		t.Fatal("acldefault", err)
	}
	authority, grantee, grantor, privileges, _, err := value.ArrayElement(0).ACLItem()
	if err != nil || authority != runtime.authority || grantee != 10 || grantor != 10 || privileges != 16511 {
		t.Fatal("acldefault item", grantee, grantor, privileges, err)
	}
	contains := ACLValue{Kind: ACLContains, Info: expr.Info{Usage: aclTestTypes[sqltype.Bool].Usage()}}
	contains.Args[0] = aclTestLiteral(value)
	contains.Args[1] = aclTestLiteral(aclTestItem(t, runtime.authority, 10, 10, 2, 0))
	if got, err := contains.Eval(runtime); err != nil || !aclTestTrue(got) {
		t.Fatal("containment", got, err)
	}
	runtime.authority = nil
	if _, err := aclTestDefault(t).Eval(runtime); err == nil {
		t.Fatal("constructor without statement authority")
	}
	if _, err := aclTestDefault(t).Eval(&temporalTestContext{Context: runtime.Context}); err == nil {
		t.Fatal("constructor without ACL runtime")
	}
}

func TestObjectACLEvaluatesSlots(t *testing.T) {
	runtime := newACLTestRuntime()
	array := aclTestTypes[sqltype.ACLItemArray]
	subid, _ := sqltype.Integer(aclTestTypes[sqltype.Int4], 3)
	// Written order objsubid, classid, objid.
	n := ObjectACL{Info: expr.Info{Usage: array.Usage()}, Slots: [3]uint8{2, 0, 1}, Labels: [3]string{"objsubid", "classid", "objid"}}
	n.Args = [3]expr.Expr{aclTestLiteral(subid), aclTestOID(t, 1259), aclTestOID(t, 42)}
	value, err := n.Eval(runtime)
	if err != nil || !value.IsNull() || runtime.address != (ids.ObjectAddress{ClassID: 1259, ObjectID: 42, SubID: 3}) {
		t.Fatal("pg_get_acl slots", runtime.address, err)
	}
	n.Args[1] = aclTestLiteral(sqltype.Null(aclTestTypes[sqltype.Oid]))
	if value, err := n.Eval(runtime); err != nil || !value.IsNull() || runtime.calls != 1 {
		t.Fatal("NULL argument reached the runtime", runtime.calls, err)
	}
}

func TestAccessInquiryEvaluatesRoles(t *testing.T) {
	runtime := newACLTestRuntime()
	boolean := aclTestTypes[sqltype.Bool].Usage()
	two := AccessInquiry{Info: expr.Info{Usage: boolean}, Count: 2}
	two.Args[0], two.Args[1] = aclTestText(sqltype.Name, "postgres"), aclTestText(sqltype.Text, "USAGE")
	if got, err := two.Eval(runtime); err != nil || !aclTestTrue(got) || runtime.actor != nil || runtime.target != (AccessRole{Name: "postgres"}) || runtime.privilege != "USAGE" {
		t.Fatal("pg_has_role(name, text)", runtime.actor, runtime.target, err)
	}
	three := AccessInquiry{Info: expr.Info{Usage: boolean}, Count: 3}
	three.Args[0], three.Args[1], three.Args[2] = aclTestOID(t, 10), aclTestText(sqltype.Name, "x"), aclTestText(sqltype.Text, "MEMBER")
	if _, err := three.Eval(runtime); err != nil || runtime.actor == nil || *runtime.actor != (AccessRole{ByOID: true, OID: 10}) || runtime.target != (AccessRole{Name: "x"}) {
		t.Fatal("pg_has_role(oid, name, text)", runtime.actor, runtime.target, err)
	}
	rls := AccessInquiry{Info: expr.Info{Usage: boolean}, Count: 1, Kind: AccessRowSecurity}
	rls.Args[0] = aclTestOID(t, 16384)
	if _, err := rls.Eval(runtime); err != nil || runtime.relation != (AccessRelation{ByOID: true, OID: 16384}) {
		t.Fatal("row_security_active(oid)", runtime.relation, err)
	}
	rls.Args[0] = aclTestText(sqltype.Text, "public.t")
	if _, err := rls.Eval(runtime); err != nil || runtime.relation != (AccessRelation{Name: "public.t"}) {
		t.Fatal("row_security_active(text)", runtime.relation, err)
	}
}

func TestACLNodesProtocol(t *testing.T) {
	acl := aclTestDefault(t)
	object := ObjectACL{Info: expr.Info{Usage: aclTestTypes[sqltype.ACLItemArray].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Slots: [3]uint8{0, 1, 2}}
	object.Args = [3]expr.Expr{aclTestOID(t, 1259), aclTestOID(t, 1), aclTestLiteral(sqltype.Null(aclTestTypes[sqltype.Int4]))}
	inquiry := AccessInquiry{Info: expr.Info{Usage: aclTestTypes[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Count: 2}
	inquiry.Args[0], inquiry.Args[1] = aclTestText(sqltype.Name, "postgres"), aclTestText(sqltype.Text, "USAGE")
	for _, tc := range []struct {
		node       expr.Composite
		children   int
		volatility expr.Volatility
	}{{acl, 2, expr.Immutable}, {object, 3, expr.Stable}, {inquiry, 2, expr.Stable}} {
		if len(tc.node.Children()) != tc.children {
			t.Fatalf("%T children %d", tc.node, len(tc.node.Children()))
		}
		if _, err := tc.node.Rebuild(expr.InfoOf(tc.node), tc.node.Children()[:1]); err == nil {
			t.Fatalf("%T rebuilt with a short child list", tc.node)
		}
		replaced := append([]expr.Expr(nil), tc.node.Children()...)
		replaced[0] = aclTestOID(t, 7)
		rebuilt, err := tc.node.Rebuild(expr.InfoOf(tc.node), replaced)
		if err != nil || rebuilt.(expr.Composite).Children()[0] != replaced[0] || tc.node.Children()[0] == replaced[0] {
			t.Fatalf("%T rebuild must copy: %v", tc.node, err)
		}
		if got := tc.node.(expr.Intrinsic).IntrinsicVolatility(); got != tc.volatility {
			t.Fatalf("%T intrinsic volatility %v", tc.node, got)
		}
		if got := tc.node.(expr.Folding).PlanningFold(); got != expr.FoldNever {
			t.Fatalf("%T folds during planning", tc.node)
		}
		if !tc.node.(expr.FunctionSyntax).LooksLikeFunction() {
			t.Fatalf("%T index key is not function syntax", tc.node)
		}
		if got := tc.node.(expr.Preflighting).PreflightFold(); got != expr.PreflightChildren {
			t.Fatalf("%T preflight fold %v", tc.node, got)
		}
		identity := tc.node.(expr.GroupIdentity)
		equal := func(a, b expr.Expr) bool { return a == b }
		if !identity.SameGroup(tc.node, equal, false) || identity.SameGroup(rebuilt, equal, false) || identity.SameGroup(aclTestOID(t, 7), equal, false) {
			t.Fatalf("%T grouping identity", tc.node)
		}
	}
	explode := ACLValue{Kind: ACLExplode}
	if explode.IntrinsicVolatility() != expr.Stable {
		t.Fatal("aclexplode is not stable")
	}
	if (ACLValue{Kind: ACLContains}).LooksLikeFunction() {
		t.Fatal("aclitem containment is not function syntax")
	}
	named := explode
	named.Named = true
	if explode.SameGroup(named, func(a, b expr.Expr) bool { return true }, true) {
		t.Fatal("named notation is part of the ACL grouping identity")
	}
	reordered := object
	reordered.Slots, reordered.Labels = [3]uint8{1, 0, 2}, [3]string{"objid", "classid", "objsubid"}
	if object.SameGroup(reordered, func(a, b expr.Expr) bool { return true }, true) {
		t.Fatal("slot order is part of the object ACL grouping identity")
	}
	if _, ok := any(object).(expr.StoredProvider); ok {
		t.Fatal("pg_get_acl must not be a stored generation provider")
	}
	if _, ok := any(inquiry).(expr.StoredProvider); ok {
		t.Fatal("access inquiries must not be stored generation providers")
	}
	if err := acl.ValidateStoredProvider(); err != nil {
		t.Fatal("stored acldefault", err)
	}
}

func TestACLNodesRender(t *testing.T) {
	acl := aclTestDefault(t)
	spec := renderOf(t, acl)
	if spec.Tree != "ACLValue 0 named false namespace " || spec.Form != expr.FormCall || spec.Name != "pg_catalog.acldefault" || spec.Quote != expr.QuoteVerbatim || spec.Replay.Name != "acldefault" || len(spec.Args) != 2 || spec.Args[0].HideImplicit || spec.Args[0].Label != "" {
		t.Fatalf("acldefault rendering %+v", spec)
	}
	explode := ACLValue{Kind: ACLExplode, Named: true, Info: expr.Info{Usage: aclTestShape(t).Usage(), Property: expr.Properties{Volatility: expr.Stable}}}
	explode.Args[0] = aclTestLiteral(aclTestEmpty(t))
	spec = renderOf(t, explode)
	if spec.Name != "pg_catalog.aclexplode" || spec.Args[0].Label != "acl" || spec.LabelQuote != expr.QuoteVerbatim {
		t.Fatalf("named aclexplode rendering %+v", spec)
	}
	contains := ACLValue{Kind: ACLContains, Namespace: "pg_catalog", Info: expr.Info{Usage: aclTestTypes[sqltype.Bool].Usage()}}
	contains.Args[0] = aclTestLiteral(aclTestEmpty(t))
	contains.Args[1] = aclTestLiteral(aclTestItem(t, &sqltype.ACLAuthority{}, 0, 0, 0, 0))
	word, at := expr.TextPiece, expr.ArgPiece
	if got := renderOf(t, contains).Pieces; !reflect.DeepEqual(got, []expr.Piece{word("("), at(0), word(` OPERATOR("pg_catalog".@>) `), at(1), word(")")}) {
		t.Fatalf("qualified containment pieces %+v", got)
	}
	contains.Namespace = "public"
	if _, err := contains.Render(); err == nil {
		t.Fatal("foreign operator schema rendered")
	}

	object := ObjectACL{Info: expr.Info{Usage: aclTestTypes[sqltype.ACLItemArray].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Slots: [3]uint8{1, 0, 2}, Labels: [3]string{"objid", "classid", "objsubid"}}
	object.Args = [3]expr.Expr{aclTestOID(t, 1), aclTestOID(t, 1259), aclTestLiteral(sqltype.Null(aclTestTypes[sqltype.Int4]))}
	spec = renderOf(t, object)
	if spec.Tree != "ObjectACL slots [1 0 2] labels [objid classid objsubid]" || spec.Name != "pg_get_acl" || spec.Quote != expr.QuoteIdentifier || spec.Args[0].Label != "objid" || spec.Args[2].Label != "objsubid" {
		t.Fatalf("pg_get_acl rendering %+v", spec)
	}
	object.Labels[0] = ""
	if _, err := object.Render(); err == nil {
		t.Fatal("positional argument after reordered slots rendered")
	}

	inquiry := AccessInquiry{Info: expr.Info{Usage: aclTestTypes[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Count: 1, Kind: AccessRowSecurity}
	inquiry.Args[0] = aclTestText(sqltype.Text, "t")
	spec = renderOf(t, inquiry)
	if spec.Tree != "AccessInquiry row_security_active" || spec.Name != "row_security_active" || spec.Quote != expr.QuoteOnRequest || len(spec.Args) != 1 {
		t.Fatalf("row_security_active rendering %+v", spec)
	}
	inquiry.Count = 2
	if _, err := inquiry.Render(); err == nil {
		t.Fatal("two-argument row_security_active rendered")
	}
}

func aclTestEmpty(t *testing.T) sqltype.Value {
	t.Helper()
	v, err := sqltype.EmptyACLArrayValue(aclTestTypes[sqltype.ACLItemArray])
	if err != nil {
		t.Fatal(err)
	}
	return v
}
