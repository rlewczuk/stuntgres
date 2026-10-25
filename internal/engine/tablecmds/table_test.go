package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestTableCoreLikeColumnOrigins(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE one(a INT NOT NULL)")
	enginetest.Run(t, e, "CREATE TABLE two(b TEXT)")
	enginetest.Run(t, e, "CREATE TABLE clone(head BOOL,LIKE one,middle BIGINT,LIKE two,tail TEXT)")
	ref := enginetest.RoleTableRef(t, e, "clone")
	d, ok := e.CatalogRead().Descriptor(ref.Address)
	if !ok {
		t.Fatal("missing clone descriptor")
	}
	for i, c := range d.(*catalog.Relation).Columns {
		if c.OriginTable != uint32(ref.Address.ObjectID) || c.OriginAttribute != int16(i+1) {
			t.Fatal("incorrect clone column origin", c)
		}
	}
}

func TestTableCoreLikeSkipDescriptorIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INT)")
	ref := enginetest.RoleTableRef(t, e, "t")
	before, _ := e.CatalogRead().Descriptor(ref.Address)
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(other TEXT)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(id no_such_type)", "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(LIKE missing INCLUDING ALL)"} {
		enginetest.RoleExecute(t, e, op, q)
		after, _ := e.CatalogRead().Descriptor(ref.Address)
		if after != before || enginetest.RoleTableRef(t, e, "t") != ref {
			t.Fatal("semantic skip replaced incumbent descriptor")
		}
	}
}
func TestTableCoreLikeOwnershipDescriptors(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE source(a INT NOT NULL)")
	})
	source := enginetest.RoleTableRef(t, e, "source")
	reader, _ := e.Roles().Lookup("reader")
	owner := op.RoleState().CurrentUser
	entry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	column := source
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	if err := enginetest.SetObjectACL(e, context.Background(), source, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE clone(LIKE source)")
	})
	clone := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "clone").Address)
	if clone.Owner != reader.Ref || clone.Security.ACL.Explicit || len(clone.Security.ColumnACL) != 0 {
		t.Fatal("LIKE copied ACL/owner")
	}
}

func TestTableCoreSchemaOwnerAndACLBlockerCleanup(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER schema_owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER acl_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE secured(id INT)")
	reader, _ := e.Roles().Lookup("acl_reader")
	ref := enginetest.RoleTableRef(t, e, "secured")
	entry := authorization.ACLEntry{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	column := ref
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, acl); err != nil {
		t.Fatal(err)
	}
	_, err := e.Execute(op, enginetest.Statement(t, "DROP ROLE acl_reader"), 0, nil)
	enginetest.State(t, err, "2BP01")
	owner, _ := e.Roles().Lookup("schema_owner")
	replacement := catalog.CloneState(e.CatalogRead().State())
	nsaddr := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	ns := *replacement.MustNamespace(nsaddr)
	ns.Owner = owner.Ref
	replacement.SetDescriptor(nsaddr, &ns)
	if _, err = e.ReplaceCatalog(context.Background(), e, e.Catalog.Read(replacement)); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION schema_owner")
	// Neither relation ownership nor ordinary ACL grants DROP; schema ownership does.
	enginetest.RoleExecute(t, e, op, "DROP TABLE secured RESTRICT")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "DROP ROLE acl_reader")
	if _, ok := e.Roles().Lookup("schema_owner"); !ok {
		t.Fatal("DROP removed schema owner")
	}
	if _, ok := e.CatalogRead().Descriptor(nsaddr); !ok {
		t.Fatal("DROP removed namespace")
	}
}

func TestTableCoreQuotedDuplicateRelationPublicationConflict(t *testing.T) {
	for _, tc := range []struct{ identifier, name string }{
		{`"a""b"`, `a"b`},
		{`"a\b"`, `a\b`},
	} {
		t.Run(tc.identifier, func(t *testing.T) {
			e := engine.New()
			namespace := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
			var winner catalog.Read
			var winnerRef ids.ObjectRef
			var loser ids.ObjectAddress
			e.Catalog.Providers[ids.RelationClassID] = &publicationConflictProvider{
				Provider: e.Catalog.Providers[ids.RelationClassID],
				publishWinner: func(d catalog.Descriptor) {
					loser = d.Address()
					enginetest.Run(t, e, "CREATE TABLE "+tc.identifier+"(id INT NOT NULL)")
					enginetest.Run(t, e, "INSERT INTO "+tc.identifier+" VALUES(11)")
					winner = e.CatalogRead()
					winnerRef = enginetest.RoleTableRef(t, e, tc.identifier)
				},
			}
			result, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE TABLE "+tc.identifier+"(lost TEXT)"), nil)
			var got *sqlstate.Error
			want := sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
				Message: `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`,
				Detail:  fmt.Sprintf("Key (typname, typnamespace)=(%s, %d) already exists.", tc.name, namespace.ObjectID),
				Schema:  "pg_catalog", Table: "pg_type", Constraint: "pg_type_typname_nsp_index",
			}
			if !errors.As(err, &got) || *got != want {
				t.Errorf("conflict diagnostic %#v (%v), want %#v", got, err, want)
			}
			if result.Count != 0 || result.Rows != nil {
				t.Fatal("loser exposed result", result)
			}
			after := e.CatalogRead()
			if winner.State() == nil || after.State() != winner.State() || enginetest.RoleTableRef(t, e, tc.identifier) != winnerRef {
				t.Fatal("winner identity changed")
			}
			if _, present := after.Descriptor(loser); present || e.Storage.Relations[loser] != nil {
				t.Fatal("loser published descriptor/storage")
			}
			beforeDescriptor, _ := winner.Descriptor(winnerRef.Address)
			afterDescriptor, _ := after.Descriptor(winnerRef.Address)
			if beforeDescriptor != afterDescriptor {
				t.Fatal("winner descriptor changed")
			}
			rows := enginetest.Run(t, e, "SELECT * FROM "+tc.identifier)
			if len(rows.Rows.Schema()) != 1 || rows.Rows.Schema()[0].Name != "id" || rows.Rows.Schema()[0].Usage.Type.DatabaseName() != "INT4" || !rows.Rows.Schema()[0].NullabilityKnown || rows.Rows.Schema()[0].Nullable {
				t.Fatal("winner shape changed", rows.Rows.Schema())
			}
			if got := enginetest.Tuples(rows); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
				t.Fatal("winner data changed", got)
			}
		})
	}
}

type publicationConflictProvider struct {
	catalog.Provider
	publishWinner func(catalog.Descriptor)
}

func (p *publicationConflictProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(view, d); err != nil {
		return err
	}
	if publish := p.publishWinner; publish != nil {
		p.publishWinner = nil
		publish(d)
	}
	return nil
}
