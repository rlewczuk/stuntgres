package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestObjectACLAddressLookup(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	whole := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, owner, authorization.SelectPrivilege, 0)}}
	column := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: 1259, ObjectID: 48001}, Attributes: []int32{1, 3}, Security: catalog.RelationSecurity{ACL: whole, ColumnACL: map[int32]authorization.ACLValue{1: column, 2: whole}}}
	view = view.WithState(catalog.CloneState(view.State()))
	view.State().SetDescriptor(relation.Address(), relation)
	schema := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: 2615, ObjectID: 48002}, ACL: whole}
	view.State().SetDescriptor(schema.Address(), schema)
	check := func(address ids.ObjectAddress, want authorization.ACLValue) {
		t.Helper()
		got, err := access.RawObjectACL(view, address)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("address %#v got %#v want %#v: %v", address, got, want, err)
		}
	}
	for _, class := range []ids.OID{1259, 2615, 1247, 1255, 1262, 2612, 1213, 2328, 1417, 2995, 2613} {
		hasACL, known := access.ObjectACLClass(class)
		if !hasACL || !known {
			t.Fatal("released ACL-bearing class", class, hasACL, known)
		}
		check(ids.ObjectAddress{ClassID: class}, authorization.ACLValue{})
	}
	// Independently enumerated REL_18_0 ObjectProperty ACL-less classes.
	for _, class := range []ids.OID{2601, 2602, 2603, 2605, 3456, 2606, 2607, 826, 3079, 2616, 2617, 2753, 1260, 1261, 2618, 3576, 2620, 3256, 3466, 3602, 3600, 3601, 3764, 6104, 6100, 3381, 1418} {
		hasACL, known := access.ObjectACLClass(class)
		if hasACL || !known {
			t.Fatal("released ACL-less class", class, hasACL, known)
		}
		check(ids.ObjectAddress{ClassID: class, ObjectID: 999999, SubID: -1}, authorization.ACLValue{})
	}
	for _, class := range []ids.OID{0, 1249, 6243, 999999} {
		if hasACL, known := access.ObjectACLClass(class); hasACL || known {
			t.Fatal("unregistered class", class)
		}
		_, err := access.RawObjectACL(view, ids.ObjectAddress{ClassID: class, ObjectID: 1})
		if err == nil {
			t.Fatal("unknown class became NULL", class)
		}
	}
	for _, subid := range []int32{0, 1, -1, 65537} {
		check(ids.ObjectAddress{SubID: subid}, authorization.ACLValue{})
		check(ids.ObjectAddress{ClassID: 2615, ObjectID: 48002, SubID: subid}, whole)
	}
	check(relation.Address(), whole)
	for _, subid := range []int32{1, 65537, -65535} {
		check(ids.ObjectAddress{ClassID: 1259, ObjectID: 48001, SubID: subid}, column)
	}
	for _, subid := range []int32{2, 3, 4, -1, 65536, -65536} {
		check(ids.ObjectAddress{ClassID: 1259, ObjectID: 48001, SubID: subid}, authorization.ACLValue{})
	}
	roles := e.roles.published.Root.Read(authorization.PrincipalRef{})
	array, _ := view.LookupType(sqltype.ACLItemArray)
	for _, acl := range []authorization.ACLValue{{}, {Explicit: true}, column} {
		value, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, acl)
		if err != nil || value.Type() != array || value.IsNull() == acl.Explicit || value.ArrayLen() != len(acl.Entries) {
			t.Fatal("typed raw ACL conversion", value, err)
		}
	}
	var foreign authorization.Domain
	bad := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(foreign.Principal(10), owner, authorization.SelectPrivilege, 0)}}
	if _, err := pgcatalog.ClassACL(e.catalog.owner(), roles, array, bad); err == nil {
		t.Fatal("foreign principal escaped checked ACLITEM constructor")
	}
	t.Run("explicit_sequence_acl", func(t *testing.T) {
		// Use actual descriptor staging and the existing ACL publication seam;
		// no sequence GRANT SQL or new producer is introduced by this inquiry.
		engine := New()
		catalogView := engine.CatalogRead()
		principal := engine.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		stage := engine.BeginCatalogStage(catalogView)
		options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		engine.mu.Lock()
		sequence, err := engine.newTableDescriptorLocked(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "acl_unit_sequence", principal, engine.sequenceColumnsLocked(), nil)
		engine.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		sequence.RelationKind, sequence.Sequence = catalog.SequenceRelation, &options
		sequence.SequencePlanStamp = sequence.Revision()
		if err := stage.Create(sequence); err != nil {
			t.Fatal(err)
		}
		domainUnitPublish(t, engine, stage)
		ref := engine.catalog.Domain.Ref(engine.CatalogRead().Epoch(), sequence.Address())
		want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(principal, principal, authorization.SelectPrivilege|authorization.UpdatePrivilege|authorization.UsagePrivilege, 0),
			{Grantor: principal, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
		}}
		if err := engine.SetObjectACL(context.Background(), ref, want); err != nil {
			t.Fatal(err)
		}
		got, err := access.RawObjectACL(engine.CatalogRead(), sequence.Address())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("sequence raw ACL", got, want, err)
		}
	})
}

func TestObjectACLExpressionProperties(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"SELECT pg_get_acl($1,$2,$3)", "SELECT pg_get_acl(objsubid => $1,classid => $2,objid => $3)", "SELECT pg_get_acl(NULL,NULL,NULL)"} {
		q := domainUnitBindSelect(t, e, op, query)
		n, ok := q.Root.(*plan.ProjectNode).Expressions[0].(functions.ObjectACL)
		if !ok || n.ResultUsage().Type.ID() != sqltype.ACLItemArray || n.Properties().Volatility != expr.Stable || n.Validate() != nil {
			t.Fatal("typed STABLE recipe", n)
		}
		for i, arg := range n.Args {
			if arg.ResultUsage().Type.ID() != [3]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}[n.Slots[i]] {
				t.Fatal("wrong formal argument type", i, arg.ResultUsage())
			}
		}
		children := 0
		plan.ExpressionChildren(n, func(expr.Expr) { children++ })
		if children != 3 {
			t.Fatal("lexical children", children)
		}
		bad := n
		bad.Slots[1] = bad.Slots[0]
		if bad.Validate() == nil {
			t.Fatal("accepted duplicate formal slot")
		}
		bad = n
		bad.Property.Volatility = expr.Immutable
		if bad.Validate() == nil {
			t.Fatal("accepted immutable inquiry")
		}
		if query == "SELECT pg_get_acl(NULL,NULL,NULL)" {
			value, err := n.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
			if err != nil || !value.IsNull() || value.Type().ID() != sqltype.ACLItemArray {
				t.Fatal("strict typed NULL without authority", value, err)
			}
		}
	}
	// A stored recipe has no result/catalog cache; remint every child's carrier.
	view := e.CatalogRead()
	array, _ := view.LookupType(sqltype.ACLItemArray)
	oid, _ := view.LookupType(sqltype.Oid)
	integer, _ := view.LookupType(sqltype.Int4)
	n := functions.ObjectACL{Info: expr.Info{Usage: array.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Slots: [3]uint8{0, 1, 2}}
	for i, typ := range []*sqltype.Descriptor{oid, oid, integer} {
		n.Args[i] = plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}
	}
	destination := New().CatalogRead()
	mapped, changed, err := retagBoundTypes(destination, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := destination.LookupType(d.ID())
		return typ
	})
	if err != nil || !changed {
		t.Fatal("retag recipe", changed, err)
	}
	fresh := mapped.(functions.ObjectACL)
	if fresh.ResultUsage().Type == n.ResultUsage().Type || fresh.Validate() != nil {
		t.Fatal("foreign result carrier retained", fresh)
	}
	for i, arg := range fresh.Args {
		if arg.ResultUsage().Type == n.Args[i].ResultUsage().Type {
			t.Fatal("foreign argument carrier retained", i)
		}
	}
	remapped, err := remapBoundExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || remapped.(functions.ObjectACL).Validate() != nil {
		t.Fatal("stored remap", err)
	}
	rewritten, changed, err := mapBoundExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
		if literal, ok := child.(plan.LiteralExpr); ok && literal.Value.Type().ID() == sqltype.Oid {
			literal.Value, _ = sqltype.OIDValue(oid, 42)
			return literal, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed || rewritten.(functions.ObjectACL).Args[0].(plan.LiteralExpr).Value.IsNull() || !n.Args[0].(plan.LiteralExpr).Value.IsNull() {
		t.Fatal("leaf rewrite mutated original recipe or skipped children", changed, err)
	}
	// Named recipes must render their NULL children in written order.
	n.Slots, n.Labels = [3]uint8{2, 0, 1}, [3]string{"objsubid", "classid", "objid"}
	n.Args = [3]expr.Expr{n.Args[2], n.Args[0], n.Args[1]}
	spec, err := n.Render()
	if err != nil || spec.Name != "pg_get_acl" || len(spec.Args) != 3 {
		t.Fatal("named written-order render", spec, err)
	}
	for i, label := range []string{"objsubid", "classid", "objid"} {
		if spec.Args[i].Label != label || spec.Args[i].Expr != n.Args[i] || !spec.Args[i].Expr.(plan.LiteralExpr).Value.IsNull() {
			t.Fatal("named render changed written order or a NULL child", i, spec.Args[i])
		}
	}
}
