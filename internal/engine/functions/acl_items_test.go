package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// aclTestRuntime is an ACLRuntime over one ACL authority. It records the
// inquiries it receives and answers them with fixed values.
type aclTestRuntime struct {
	temporalTestContext
	authority *sqltype.ACLAuthority
	address   ids.ObjectAddress
	actor     *AccessRole
	target    AccessRole
	privilege string
	relation  AccessRelation
	calls     int
}

func newACLTestRuntime() *aclTestRuntime {
	return &aclTestRuntime{temporalTestContext: temporalTestContext{Context: context.Background()}, authority: &sqltype.ACLAuthority{}}
}

func (r *aclTestRuntime) ACLAuthority() *sqltype.ACLAuthority { return r.authority }
func (r *aclTestRuntime) ObjectACL(address ids.ObjectAddress, result *sqltype.Descriptor) (sqltype.Value, error) {
	r.calls++
	r.address = address
	return sqltype.Null(result), nil
}
func (r *aclTestRuntime) RoleMembership(actor *AccessRole, target AccessRole, privilege string) (bool, error) {
	r.calls++
	r.actor, r.target, r.privilege = actor, target, privilege
	return true, nil
}
func (r *aclTestRuntime) RowSecurityActive(relation AccessRelation) (bool, error) {
	r.calls++
	r.relation = relation
	return true, nil
}
func (r *aclTestRuntime) ObjectPrivilege(kind AccessKind, actor *AccessRole, target AccessRole, privilege string) (bool, bool, error) {
	r.calls++
	r.actor, r.target, r.privilege = actor, target, privilege
	return true, true, nil
}

func (r *aclTestRuntime) RelationPrivilege(kind AccessKind, actor *AccessRole, relation AccessRelation, column AccessColumn, privilege string) (bool, bool, error) {
	switch kind {
	case AccessTable, AccessSequence, AccessColumnPrivilege, AccessAnyColumn:
		r.calls++
		r.actor, r.relation, r.privilege = actor, relation, privilege
		return true, true, nil
	default:
		return false, false, errors.New("invalid relation inquiry kind")
	}
}

// aclTestTypes is one builtin generation, so items, arrays and records agree.
var aclTestTypes = temporalTestTypes()

func aclTestItem(t *testing.T, authority *sqltype.ACLAuthority, g, r, p, o uint32) sqltype.Value {
	t.Helper()
	v, err := sqltype.ACLItemValue(aclTestTypes[sqltype.ACLItem], authority, g, r, p, o)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func aclTestArray(t *testing.T, items ...sqltype.Value) sqltype.Value {
	t.Helper()
	v, err := sqltype.ArrayValue(aclTestTypes[sqltype.ACLItemArray], []sqltype.ArrayDimension{{Lower: -3, Length: int32(len(items))}}, items)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func aclTestShape(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	d := aclTestTypes
	names := []string{"grantor", "grantee", "privilege_type", "is_grantable"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Text, sqltype.Bool}
	members := make([]sqltype.CompositeMember, 4)
	for i, name := range names {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: d[types[i]].Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

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
	for _, text := range []string{"", ",SELECT", "SELECT,", "ALL", "ALTER\tSYSTEM", "ALTER  SYSTEM", " SELECT", "ſELECT"} {
		if _, err := aclPrivilegeNames(text); err == nil {
			t.Fatal("invalid privilege", text)
		}
	}
}

func TestACLValuesContainment(t *testing.T) {
	runtime := newACLTestRuntime()
	ctx := context.Background()
	item := func(g, r, p, o uint32) sqltype.Value { return aclTestItem(t, runtime.authority, g, r, p, o) }
	split := aclTestArray(t, item(10, 10, 2, 0), item(10, 10, 4, 0))
	both := aclTestArray(t, item(10, 10, 6, 2))
	for _, tc := range []struct {
		array, item sqltype.Value
		want        bool
	}{{split, item(10, 10, 6, 0), false}, {both, item(10, 10, 2, 2), true}, {both, item(10, 10, 6, 4), false}, {both, item(11, 10, 2, 0), false}, {both, item(10, 11, 2, 0), false}, {both, item(10, 10, 0, 0), true}} {
		got, err := aclContains(ctx, runtime, tc.array, tc.item)
		if err != nil || got != tc.want {
			t.Fatal("containment", got, tc.want, err)
		}
	}
	d := aclTestTypes
	array, element := d[sqltype.ACLItemArray], d[sqltype.ACLItem]
	rank0, _ := sqltype.ArrayValue(array, nil, nil)
	rank2, _ := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}, {Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(element)})
	rank1 := aclTestArray(t, sqltype.Null(element))
	for _, tc := range []struct {
		array sqltype.Value
		code  string
	}{{rank0, "22023"}, {rank2, "22023"}, {rank1, "22004"}} {
		err := checkACLValue(ctx, runtime, tc.array)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal("rank/null precedence", err)
		}
	}
	empty, _ := sqltype.EmptyACLArrayValue(array)
	got, err := aclContains(ctx, runtime, empty, item(0, 0, 0, 0))
	if err != nil || got {
		t.Fatal("empty contains", got, err)
	}
	// Items of another role store are foreign, as either operand.
	foreign := &sqltype.ACLAuthority{}
	if _, err := aclContains(ctx, runtime, both, aclTestItem(t, foreign, 10, 10, 2, 0)); err == nil {
		t.Fatal("foreign item accepted")
	}
	if _, err := aclContains(ctx, runtime, aclTestArray(t, aclTestItem(t, foreign, 10, 10, 2, 0)), item(10, 10, 2, 0)); err == nil {
		t.Fatal("foreign array accepted")
	}
	runtime.authority = nil
	if err := checkACLValue(ctx, runtime, both); err == nil {
		t.Fatal("missing statement authority accepted")
	}
}

func TestACLValuesInvocation(t *testing.T) {
	authority := &sqltype.ACLAuthority{}
	item := aclTestItem(t, authority, 0, 0, 16386, 16384)
	noRights := aclTestItem(t, authority, 0, 0, 0, 0)
	array := aclTestArray(t, noRights, item, item)
	shape := aclTestShape(t)
	first, second := ACLExplodeCursor{array: array}, ACLExplodeCursor{array: array}
	for i, name := range []string{"SELECT", "MAINTAIN", "SELECT", "MAINTAIN"} {
		value, ok, err := first.Next(context.Background(), shape)
		if err != nil || !ok {
			t.Fatal(i, ok, err)
		}
		text, _ := value.CompositeField(2).Text()
		grantable, _ := value.CompositeField(3).Boolean()
		if text != name || grantable != (i%2 == 1) {
			t.Fatal("bit and option", text, grantable)
		}
	}
	if _, ok, err := first.Next(context.Background(), shape); err != nil || ok {
		t.Fatal("EOF", ok, err)
	}
	value, ok, err := second.Next(context.Background(), shape)
	text, _ := value.CompositeField(2).Text()
	if err != nil || !ok || text != "SELECT" {
		t.Fatal("independent invocation", text, ok, err)
	}
	second.Close()
	if _, ok, err := second.Next(context.Background(), shape); err != nil || ok {
		t.Fatal("closed cursor", ok, err)
	}
	canceled := ACLExplodeCursor{array: array}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := canceled.Next(ctx, shape); !errors.Is(err, context.Canceled) || !canceled.closed {
		t.Fatal("cancel cleanup", err)
	}
}
