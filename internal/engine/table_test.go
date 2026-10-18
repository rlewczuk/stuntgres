package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTableCoreLikeColumnOrigins(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE one(a INT NOT NULL)")
	run(t, e, "CREATE TABLE two(b TEXT)")
	run(t, e, "CREATE TABLE clone(head BOOL,LIKE one,middle BIGINT,LIKE two,tail TEXT)")
	ref := roleTableRef(t, e, "clone")
	d, ok := e.CatalogRead().Descriptor(ref.Address)
	if !ok {
		t.Fatal("missing clone descriptor")
	}
	for i, c := range d.(*relationDescriptor).Columns {
		if c.OriginTable != uint32(ref.Address.ObjectID) || c.OriginAttribute != int16(i+1) {
			t.Fatal("incorrect clone column origin", c)
		}
	}
}

func TestTableCoreLikeSkipDescriptorIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(id INT)")
	ref := roleTableRef(t, e, "t")
	before, _ := e.CatalogRead().Descriptor(ref.Address)
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(other TEXT)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(id no_such_type)", "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(LIKE missing INCLUDING ALL)"} {
		roleExecute(t, e, op, q)
		after, _ := e.CatalogRead().Descriptor(ref.Address)
		if after != before || roleTableRef(t, e, "t") != ref {
			t.Fatal("semantic skip replaced incumbent descriptor")
		}
	}
}
func TestTableCoreLikeOwnershipDescriptors(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() {
		roleExecute(t, e, op, "CREATE TABLE source(a INT NOT NULL)")
	})
	source := roleTableRef(t, e, "source")
	reader, _ := e.Roles().Lookup("reader")
	owner := op.RoleState().CurrentUser
	entry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	column := source
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	if err := e.SetObjectACL(context.Background(), source, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		roleExecute(t, e, op, "CREATE TABLE clone(LIKE source)")
	})
	clone := e.CatalogRead().MustRelation(roleTableRef(t, e, "clone").Address)
	if clone.Owner != reader.Ref || clone.ACL.Explicit || len(clone.ColumnACL) != 0 {
		t.Fatal("LIKE copied ACL/owner")
	}
}

func TestTableCoreSchemaOwnerAndACLBlockerCleanup(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER schema_owner")
	roleExecute(t, e, op, "CREATE USER acl_reader")
	roleExecute(t, e, op, "CREATE TABLE secured(id INT)")
	reader, _ := e.Roles().Lookup("acl_reader")
	ref := roleTableRef(t, e, "secured")
	entry := authorization.ACLEntry{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	column := ref
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
		t.Fatal(err)
	}
	_, err := e.Execute(op, statement(t, "DROP ROLE acl_reader"), 0, nil)
	state(t, err, "2BP01")
	owner, _ := e.Roles().Lookup("schema_owner")
	replacement := catalog.CloneState(e.CatalogRead().State())
	nsaddr := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	ns := *replacement.MustNamespace(nsaddr)
	ns.Owner = owner.Ref
	replacement.SetDescriptor(nsaddr, &ns)
	if _, err = e.ReplaceCatalog(context.Background(), e, e.catalog.read(replacement)); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION schema_owner")
	// Neither relation ownership nor ordinary ACL grants DROP; schema ownership does.
	roleExecute(t, e, op, "DROP TABLE secured RESTRICT")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "DROP ROLE acl_reader")
	if _, ok := e.Roles().Lookup("schema_owner"); !ok {
		t.Fatal("DROP removed schema owner")
	}
	if _, ok := e.CatalogRead().Descriptor(nsaddr); !ok {
		t.Fatal("DROP removed namespace")
	}
}

func TestTableCoreQuotedDuplicateRelationPublicationConflict(t *testing.T) {
	for _, tc := range []struct{ identifier, message string }{
		{`"a""b"`, `relation "a"b" already exists`},
		{`"a\b"`, `relation "a\b" already exists`},
	} {
		t.Run(tc.identifier, func(t *testing.T) {
			e := New()
			var winner CatalogRead
			var winnerRef ids.ObjectRef
			var loser ids.ObjectAddress
			e.catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
				CatalogProvider: e.catalog.Providers[ids.RelationClassID],
				publishWinner: func(d Descriptor) {
					loser = d.Address()
					run(t, e, "CREATE TABLE "+tc.identifier+"(id INT NOT NULL)")
					run(t, e, "INSERT INTO "+tc.identifier+" VALUES(11)")
					winner = e.CatalogRead()
					winnerRef = roleTableRef(t, e, tc.identifier)
				},
			}
			result, err := testExecute(e, context.Background(), statement(t, "CREATE TABLE "+tc.identifier+"(lost TEXT)"), nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: tc.message}
			if !errors.As(err, &got) || *got != want {
				t.Errorf("conflict diagnostic %#v (%v), want %#v", got, err, want)
			}
			if result.Count != 0 || result.Rows != nil {
				t.Fatal("loser exposed result", result)
			}
			after := e.CatalogRead()
			if winner.State() == nil || after.State() != winner.State() || roleTableRef(t, e, tc.identifier) != winnerRef {
				t.Fatal("winner identity changed")
			}
			if _, present := after.Descriptor(loser); present || e.storage[loser] != nil {
				t.Fatal("loser published descriptor/storage")
			}
			beforeDescriptor, _ := winner.Descriptor(winnerRef.Address)
			afterDescriptor, _ := after.Descriptor(winnerRef.Address)
			if beforeDescriptor != afterDescriptor {
				t.Fatal("winner descriptor changed")
			}
			rows := run(t, e, "SELECT * FROM "+tc.identifier)
			if len(rows.Rows.Schema()) != 1 || rows.Rows.Schema()[0].Name != "id" || rows.Rows.Schema()[0].Usage.Type.DatabaseName() != "INT4" || !rows.Rows.Schema()[0].NullabilityKnown || rows.Rows.Schema()[0].Nullable {
				t.Fatal("winner shape changed", rows.Rows.Schema())
			}
			if got := tuples(rows); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
				t.Fatal("winner data changed", got)
			}
		})
	}
}
