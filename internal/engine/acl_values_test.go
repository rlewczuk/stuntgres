package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestACLValuesDefaults(t *testing.T) {
	for _, tc := range []struct {
		code          string
		owner, public uint32
	}{{"c", 0, 0}, {"r", 16511, 0}, {"s", 262, 0}, {"d", 3584, 3072}, {"f", 128, 128}, {"l", 256, 256}, {"L", 6, 0}, {"n", 768, 0}, {"p", 12288, 0}, {"t", 512, 0}, {"F", 256, 0}, {"S", 256, 0}, {"T", 256, 256}} {
		owner, public, err := aclDefaultMasks(tc.code)
		if err != nil || owner != tc.owner || public != tc.public {
			t.Fatal(tc, owner, public, err)
		}
	}
	for _, code := range []string{"", "z", "R", "\xc3"} {
		_, _, err := aclDefaultMasks(code)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "XX000" || d.Message != "unrecognized object type abbreviation: "+code {
			t.Fatal(code, err)
		}
	}
}
func TestACLValuesPrivileges(t *testing.T) {
	names := []string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "EXECUTE", "USAGE", "CREATE", "TEMPORARY", "CONNECT", "SET", "ALTER SYSTEM", "MAINTAIN"}
	for i, name := range names {
		got, err := aclPrivilegeNames(name)
		if err != nil || got != uint32(1)<<i {
			t.Fatal(name, got, err)
		}
	}
	for _, tc := range []struct {
		text string
		mask uint32
	}{{" select, INSERT, update ,DELETE ", 15}, {"TEMP,TEMPORARY,temp", 1024}, {"\tSELECT\r,select\v", 2}} {
		got, err := aclPrivilegeNames(tc.text)
		if err != nil || got != tc.mask {
			t.Fatal(tc, got, err)
		}
	}
	for _, text := range []string{"", ",SELECT", "SELECT,", "ALL", "ALTER\tSYSTEM", "ALTER  SYSTEM", "\u00a0SELECT", "ſELECT"} {
		if _, err := aclPrivilegeNames(text); err == nil {
			t.Fatal("invalid privilege", text)
		}
	}
}
func aclValuesUnitContext(t *testing.T) (*Engine, *EvalContext) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return e, &EvalContext{Context: context.Background(), statementScope: &StatementScope{op: op}}
}
func TestACLValuesZeroIdentity(t *testing.T) {
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.ACLItem)
	value, err := sqltype.ACLItemValue(d, &e.aclAuthority, 0, 0, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	a, g, r, p, o, err := value.ACLItem()
	if err != nil || a != &e.aclAuthority || g != 0 || r != 0 || p != 2 || o != 2 {
		t.Fatal("zero identity datum", err)
	}
	output := capturedACLItemOutput{owner: e, roles: e.Roles()}
	text, err := d.OutputWithEnvironment(c.Context, value, sqltype.OutputEnvironment{ACLItem: output})
	if err != nil || text != "=r*/0" {
		t.Fatal(text, err)
	}
	rows := &finalRows{}
	if err := captureACLItemOutputValue(rows, output, value); err != nil {
		t.Fatal(err)
	}
	text, err = d.OutputWithEnvironment(c.Context, value, rows.OutputEnvironment())
	if err != nil || text != "=r*/0" {
		t.Fatal("retained all-zero output", text, err)
	}
	if err := preflightACLItemValue(c.Context, capturedACLItemOutput{owner: e, roles: e.Roles(), lossless: true}, value); err == nil {
		t.Fatal("zero grantor dump accepted")
	}
	target := New()
	mapped, err := target.aclItemImporter(e, e.Roles(), target.Roles(), nil)(value)
	if err != nil {
		t.Fatal(err)
	}
	aa, gg, rr, _, _, err := mapped.ACLItem()
	if err != nil || aa != &target.aclAuthority || gg != 0 || rr != 0 {
		t.Fatal("zero remap", err)
	}
	if _, err := aclValueContains(c, mustACLValuesArray(t, view, value), mapped); err == nil {
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
func TestACLValuesContainment(t *testing.T) {
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.ACLItem)
	item := func(g, r, p, o uint32) sqltype.Value {
		v, err := sqltype.ACLItemValue(d, &e.aclAuthority, g, r, p, o)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	split := mustACLValuesArray(t, view, item(10, 10, 2, 0), item(10, 10, 4, 0))
	both := mustACLValuesArray(t, view, item(10, 10, 6, 2))
	for _, tc := range []struct {
		array, item sqltype.Value
		want        bool
	}{{split, item(10, 10, 6, 0), false}, {both, item(10, 10, 2, 2), true}, {both, item(10, 10, 6, 4), false}, {both, item(11, 10, 2, 0), false}, {both, item(10, 11, 2, 0), false}, {both, item(10, 10, 0, 0), true}} {
		got, err := aclValueContains(c, tc.array, tc.item)
		if err != nil || got != tc.want {
			t.Fatal("containment", got, tc.want, err)
		}
	}
	array, _ := view.LookupType(sqltype.ACLItemArray)
	rank0, _ := sqltype.ArrayValue(array, nil, nil)
	rank2, _ := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}, {Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(d)})
	rank1 := mustACLValuesArray(t, view, sqltype.Null(d))
	for _, tc := range []struct {
		array sqltype.Value
		code  string
	}{{rank0, "22023"}, {rank2, "22023"}, {rank1, "22004"}} {
		err := checkACLValue(c, tc.array)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal("rank/null precedence", err)
		}
	}
	empty, _ := sqltype.EmptyACLArrayValue(array)
	got, err := aclValueContains(c, empty, item(0, 0, 0, 0))
	if err != nil || got {
		t.Fatal("empty contains", got, err)
	}
}
func aclValuesUnitShape(t *testing.T, view catalog.Read) *sqltype.Descriptor {
	t.Helper()
	names := []string{"grantor", "grantee", "privilege_type", "is_grantable"}
	ids := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Text, sqltype.Bool}
	members := make([]sqltype.CompositeMember, 4)
	for i, name := range names {
		d, _ := view.LookupType(ids[i])
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: d.Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}
func TestACLValuesInvocation(t *testing.T) {
	e, _ := aclValuesUnitContext(t)
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.ACLItem)
	item, _ := sqltype.ACLItemValue(d, &e.aclAuthority, 0, 0, 16386, 16384)
	noRights, _ := sqltype.ACLItemValue(d, &e.aclAuthority, 0, 0, 0, 0)
	array := mustACLValuesArray(t, view, noRights, item, item)
	shape := aclValuesUnitShape(t, view)
	first, second := aclExplodeCursor{array: array}, aclExplodeCursor{array: array}
	for i, name := range []string{"SELECT", "MAINTAIN", "SELECT", "MAINTAIN"} {
		value, ok, err := first.next(context.Background(), shape)
		if err != nil || !ok {
			t.Fatal(i, ok, err)
		}
		text, _ := value.CompositeField(2).Text()
		grantable, _ := value.CompositeField(3).Boolean()
		if text != name || grantable != (i%2 == 1) {
			t.Fatal("bit and option", text, grantable)
		}
	}
	if _, ok, err := first.next(context.Background(), shape); err != nil || ok {
		t.Fatal("EOF", ok, err)
	}
	value, ok, err := second.next(context.Background(), shape)
	text, _ := value.CompositeField(2).Text()
	if err != nil || !ok || text != "SELECT" {
		t.Fatal("independent invocation", text, ok, err)
	}
	second.close()
	if _, ok, err := second.next(context.Background(), shape); err != nil || ok {
		t.Fatal("closed cursor", ok, err)
	}
	canceled := aclExplodeCursor{array: array}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := canceled.next(ctx, shape); !errors.Is(err, context.Canceled) || !canceled.closed {
		t.Fatal("cancel cleanup", err)
	}
}
func TestACLValuesStoredRecipe(t *testing.T) {
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	array, _ := view.LookupType(sqltype.ACLItemArray)
	char, _ := view.LookupType(sqltype.Char)
	oid, _ := view.LookupType(sqltype.Oid)
	zero, _ := sqltype.OIDValue(oid, 0)
	n := aclValueExpression{kind: aclDefault, Info: expr.Info{Usage: array.Usage()}}
	n.args[0] = literalExpression{Info: expr.Info{Usage: char.Usage()}, value: sqltype.String(char, "c")}
	n.args[1] = literalExpression{Info: expr.Info{Usage: oid.Usage()}, value: zero}
	if err := n.validateStored(); err != nil {
		t.Fatal(err)
	}
	original, err := n.Eval(c)
	if err != nil || original.ArrayRank() != 1 || original.ArrayLen() != 0 {
		t.Fatal("original empty recipe", err)
	}
	target := New().CatalogRead()
	mapped, _, err := mapBoundExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		fresh, ok := target.LookupType(d.ID())
		if !ok {
			return d
		}
		return fresh
	}, nil, nil, nil)
	if err != nil || mapped.(aclValueExpression).validateStored() != nil {
		t.Fatal("typed recipe retag", err)
	}
	value, err := mapped.Eval(c)
	if err != nil || value.ArrayRank() != 1 || value.ArrayDimension(0).Length != 0 {
		t.Fatal("remapped recipe lost behavior", err)
	}
	empty, _ := sqltype.EmptyACLArrayValue(array)
	text, err := catalogLiteralSQL(context.Background(), view, literalExpression{Info: expr.Info{Usage: array.Usage()}, value: empty})
	if err != nil || text != `pg_catalog.acldefault('c'::"char",0::oid)` {
		t.Fatal("lossless empty literal", text, err)
	}
	bad := n
	bad.Usage = oid.Usage()
	if bad.validateStored() == nil {
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
	output := capturedACLItemOutput{owner: e, roles: e.Roles(), lossless: true}
	if err := preflightACLItemValue(context.Background(), output, carrier); err != nil {
		t.Fatal("representable composite rejected", err)
	}
	if err := preflightACLItemValue(context.Background(), output, nested); err == nil {
		t.Fatal("lossy nested array passed preflight")
	}
}
