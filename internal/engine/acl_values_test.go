package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func aclValuesUnitContext(t *testing.T) (*Engine, *exec.Context) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(&StatementScope{op: op})}}
}
func TestACLValuesZeroIdentity(t *testing.T) {
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.ACLItem)
	value, err := sqltype.ACLItemValue(d, &e.roles.aclAuthority, 0, 0, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	a, g, r, p, o, err := value.ACLItem()
	if err != nil || a != &e.roles.aclAuthority || g != 0 || r != 0 || p != 2 || o != 2 {
		t.Fatal("zero identity datum", err)
	}
	output := regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles()}
	text, err := d.OutputWithEnvironment(c.Context, value, sqltype.OutputEnvironment{ACLItem: output})
	if err != nil || text != "=r*/0" {
		t.Fatal(text, err)
	}
	rows := &exec.FinalRows{}
	if err := regobj.CaptureACLItemOutputValue(rows, output, value); err != nil {
		t.Fatal(err)
	}
	text, err = d.OutputWithEnvironment(c.Context, value, rows.OutputEnvironment())
	if err != nil || text != "=r*/0" {
		t.Fatal("retained all-zero output", text, err)
	}
	if err := regobj.PreflightACLItemValue(c.Context, regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles(), Lossless: true}, value); err == nil {
		t.Fatal("zero grantor dump accepted")
	}
	target := New()
	mapped, err := target.aclItemImporter(e, e.Roles(), target.Roles(), nil)(value)
	if err != nil {
		t.Fatal(err)
	}
	aa, gg, rr, _, _, err := mapped.ACLItem()
	if err != nil || aa != &target.roles.aclAuthority || gg != 0 || rr != 0 {
		t.Fatal("zero remap", err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	array, _ := view.LookupType(sqltype.ACLItemArray)
	contains := functions.ACLValue{Kind: functions.ACLContains, Info: expr.Info{Usage: boolean.Usage()}}
	contains.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: mustACLValuesArray(t, view, value)}
	contains.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: mapped}
	if _, err := contains.Eval(c); err == nil {
		t.Fatal("foreign authority accepted")
	}
}
func mustACLValuesArray(t *testing.T, view catalog.Read, items ...sqltype.Value) sqltype.Value {
	t.Helper()
	d, _ := view.LookupType(sqltype.ACLItemArray)
	v, err := sqltype.ArrayValue(d, []sqltype.ArrayDimension{{Lower: -3, Length: int32(len(items))}}, items)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestACLValuesStoredRecipe(t *testing.T) {
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	array, _ := view.LookupType(sqltype.ACLItemArray)
	char, _ := view.LookupType(sqltype.Char)
	oid, _ := view.LookupType(sqltype.Oid)
	zero, _ := sqltype.OIDValue(oid, 0)
	n := functions.ACLValue{Kind: functions.ACLDefault, Info: expr.Info{Usage: array.Usage()}}
	n.Args[0] = plan.LiteralExpr{Info: expr.Info{Usage: char.Usage()}, Value: sqltype.String(char, "c")}
	n.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: zero}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	original, err := n.Eval(c)
	if err != nil || original.ArrayRank() != 1 || original.ArrayLen() != 0 {
		t.Fatal("original empty recipe", err)
	}
	target := New().CatalogRead()
	mapped, _, err := stored.MapExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		fresh, ok := target.LookupType(d.ID())
		if !ok {
			return d
		}
		return fresh
	}, nil, nil, nil)
	if err != nil || mapped.(functions.ACLValue).Validate() != nil {
		t.Fatal("typed recipe retag", err)
	}
	value, err := mapped.Eval(c)
	if err != nil || value.ArrayRank() != 1 || value.ArrayDimension(0).Length != 0 {
		t.Fatal("remapped recipe lost behavior", err)
	}
	empty, _ := sqltype.EmptyACLArrayValue(array)
	text, err := regobj.Literal(context.Background(), view, plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: empty})
	if err != nil || text != `pg_catalog.acldefault('c'::"char",0::oid)` {
		t.Fatal("lossless empty literal", text, err)
	}
	bad := n
	bad.Usage = oid.Usage()
	if bad.Validate() == nil {
		t.Fatal("bad result admitted")
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: array.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	carrier, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{empty})
	if err != nil {
		t.Fatal(err)
	}
	nestedArray, err := sqltype.Array(90001, "acl_values_carriers", shape)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := sqltype.ArrayValue(nestedArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{carrier})
	if err != nil {
		t.Fatal(err)
	}
	output := regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles(), Lossless: true}
	if err := regobj.PreflightACLItemValue(context.Background(), output, carrier); err != nil {
		t.Fatal("representable composite rejected", err)
	}
	if err := regobj.PreflightACLItemValue(context.Background(), output, nested); err == nil {
		t.Fatal("lossy nested array passed preflight")
	}
}
