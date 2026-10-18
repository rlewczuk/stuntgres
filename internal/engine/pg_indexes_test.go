package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgIndexesUnitSource(t *testing.T, c CatalogRead) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12002}
	d, ok := c.state.descriptors[a].(*relationDescriptor)
	if !ok {
		t.Fatal("missing pg_indexes")
	}
	return d, c.owner.catalog.domain.Ref(c.Epoch(), a)
}
func pgIndexesUnitCapture(t *testing.T, c CatalogRead, auth AuthorizationView) *querySource {
	t.Helper()
	d, ref := pgIndexesUnitSource(t, c)
	s, err := capturePGIndexes(context.Background(), c, auth, ref, d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func pgIndexesUnitRows(t *testing.T, s *querySource) [][]any {
	t.Helper()
	var rows [][]any
	for _, row := range s.anonymous {
		if len(row) != 5 {
			t.Fatal(row)
		}
		values := make([]any, 5)
		for i, v := range row {
			if v.IsNull() {
				continue
			}
			text, err := v.Text()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = text
		}
		rows = append(rows, values)
	}
	return rows
}
func TestPGIndexesDescriptors(t *testing.T) {
	e := New()
	c := e.CatalogRead()
	d, ref := pgIndexesUnitSource(t, c)
	names := []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}
	if d.name != "pg_indexes" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || len(d.columns) != 5 || d.attributeHighWater != 5 {
		t.Fatal(d)
	}
	for i, col := range d.columns {
		id := sqltype.Name
		coll := uint32(950)
		if i == 4 {
			id = sqltype.Text
			coll = 100
		}
		typ, ok := c.LookupType(id)
		if !ok || col.Name != names[i] || col.Usage.Type != typ || col.Usage.Type.ID() != id || col.Usage.Typmod != -1 || uint32(col.Usage.Collation.ID) != coll || !col.Nullable || !col.NullabilityKnown || col.OriginTable != 12002 || col.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[names[i]] != i {
			t.Fatal(i, col)
		}
	}
	if err := (relationProvider{}).Validate(c, d); err != nil {
		t.Fatal(err)
	}
	row := c.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.rowType)}].(*typeDescriptor)
	if row.owningRelation != d.address {
		t.Fatal("row type linkage", row)
	}
	if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal(d.acl)
	}
	s := pgIndexesUnitCapture(t, c, catalogAuthorization(e, c))
	if s.ref != ref || s.anonymous == nil || len(s.anonymous) != 0 {
		t.Fatal(s)
	}
}
func TestPGIndexesVisibilityPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		table                                          authorization.Privileges
		column                                         int32
		public, owner, emptyOwner, super, schemaDenied bool
		want                                           int
	}{
		{name: "no-rights"},
		{name: "table-select", table: authorization.SelectPrivilege, want: 1},
		{name: "nonindexed-column-select", column: 3, want: 1},
		{name: "public-select", table: authorization.SelectPrivilege, public: true, want: 1},
		{name: "insert-only", table: authorization.InsertPrivilege},
		{name: "delete-only", table: authorization.DeletePrivilege},
		{name: "maintain-only", table: authorization.MaintainPrivilege},
		{name: "absent-owner", owner: true, want: 1},
		{name: "explicit-empty-owner", owner: true, emptyOwner: true},
		{name: "superuser", super: true, emptyOwner: true, want: 1},
		{name: "schema-denied-table-select", table: authorization.SelectPrivilege, schemaDenied: true},
		{name: "schema-denied-column-select", column: 3, schemaDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE ROLE pgi_unit_owner")
			run(t, e, "CREATE ROLE pgi_unit_reader")
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE SCHEMA pgi_unit_schema AUTHORIZATION pgi_unit_owner")
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION pgi_unit_owner")
			roleExecute(t, e, op, "CREATE TABLE pgi_unit_schema.pgi_unit_table(n INT,unused INT,p TEXT)")
			roleExecute(t, e, op, "CREATE INDEX pgi_unit_index ON pgi_unit_schema.pgi_unit_table(n)")
			parent := roleTableRef(t, e, "pgi_unit_schema.pgi_unit_table")
			reader, _ := e.Roles().Lookup("pgi_unit_reader")
			owner, _ := e.Roles().Lookup("pgi_unit_owner")
			grant := func(ref ids.ObjectRef, rights authorization.Privileges, public bool) {
				t.Helper()
				acl := authorization.ACLValue{Explicit: true}
				if rights != 0 {
					acl.Entries = []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref, Public: public}, Privileges: rights}}
				}
				if public {
					acl.Entries[0].Grantee.Role = authorization.PrincipalRef{}
				}
				if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
					t.Fatal(err)
				}
			}
			schema := parent
			schema.Address = e.CatalogRead().state.descriptors[parent.Address].(*relationDescriptor).namespace
			schemaACL := authorization.ACLValue{Explicit: true}
			if !tc.schemaDenied {
				schemaACL.Entries = []authorization.ACLEntry{
					{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege},
					{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege},
				}
			}
			if err := e.SetObjectACL(context.Background(), schema, schemaACL); err != nil {
				t.Fatal(err)
			}
			if !tc.owner || tc.emptyOwner {
				grant(parent, tc.table, tc.public)
			}
			if tc.column != 0 {
				column := parent
				column.Address.SubID = tc.column
				grant(column, authorization.SelectPrivilege, false)
			}
			c := e.CatalogRead()
			auth := catalogAuthorization(e, c)
			auth.state.CurrentUser = reader.Ref
			if tc.owner {
				auth.state.CurrentUser = owner.Ref
			}
			if tc.super {
				auth.state.CurrentUser = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			}
			source := pgIndexesUnitCapture(t, c, auth)
			if source.anonymous == nil {
				t.Fatal("empty or denied capture must retain an anonymous source")
			}
			got := pgIndexesUnitRows(t, source)
			if len(got) != tc.want {
				t.Fatal(got, tc.want)
			}
			if tc.want == 1 && !reflect.DeepEqual(got, [][]any{{"pgi_unit_schema", "pgi_unit_table", "pgi_unit_index", nil, "CREATE INDEX pgi_unit_index ON pgi_unit_schema.pgi_unit_table USING btree (n)"}}) {
				t.Fatal(got)
			}
		})
	}
}
func TestPGIndexesCaptureRetentionAndStableColumnACL(t *testing.T) {
	e := New()
	run(t, e, "CREATE ROLE pgi_unit_reader")
	run(t, e, "CREATE TABLE pgi_unit_table(n INT,dead TEXT,live TEXT)")
	run(t, e, "CREATE INDEX pgi_unit_index ON pgi_unit_table(n)")
	parent := roleTableRef(t, e, "pgi_unit_table")
	reader, _ := e.Roles().Lookup("pgi_unit_reader")
	grant := func(sub int32, rights authorization.Privileges) {
		t.Helper()
		ref := parent
		ref.Address.SubID = sub
		acl := authorization.ACLValue{Explicit: true}
		if rights != 0 {
			acl.Entries = []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}
		}
		if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() (CatalogRead, AuthorizationView) {
		c := e.CatalogRead()
		auth := catalogAuthorization(e, c)
		auth.state.CurrentUser = reader.Ref
		return c, auth
	}
	grant(2, authorization.SelectPrivilege)
	old, auth := capture()
	retained := pgIndexesUnitCapture(t, old, auth)
	want := [][]any{{"public", "pgi_unit_table", "pgi_unit_index", nil, "CREATE INDEX pgi_unit_index ON public.pgi_unit_table USING btree (n)"}}
	run(t, e, "ALTER TABLE pgi_unit_table DROP COLUMN dead")
	c, fresh := capture()
	if len(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, c, fresh))) != 0 {
		t.Fatal("dropped column grant disclosed index")
	}
	grant(3, authorization.SelectPrivilege)
	c, fresh = capture()
	if !reflect.DeepEqual(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, c, fresh)), want) {
		t.Fatal("stable live nonindexed column grant lost")
	}
	grant(3, 0)
	c, fresh = capture()
	if len(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, c, fresh))) != 0 {
		t.Fatal("revoked ACL cached")
	}
	run(t, e, "ALTER INDEX pgi_unit_index RENAME TO pgi_unit_renamed")
	if !reflect.DeepEqual(pgIndexesUnitRows(t, retained), want) || !reflect.DeepEqual(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, old, auth)), want) {
		t.Fatal("capture read current names/ACL")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, old, auth)), want) {
		t.Fatal("Reset mutated capture")
	}
	d, ref := pgIndexesUnitSource(t, old)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := capturePGIndexes(canceled, old, auth, ref, d); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign := New()
	bad := ref
	bad = foreign.catalog.domain.Ref(bad.Epoch, bad.Address)
	for _, invalid := range []ids.ObjectRef{bad, ref.Domain().Ref(ref.Epoch+1, ref.Address), ref.Domain().Ref(ref.Epoch, parent.Address)} {
		if _, err := capturePGIndexes(context.Background(), old, auth, invalid, d); err == nil {
			t.Fatal("invalid capture accepted", invalid)
		}
	}
	mixed := auth
	mixed.catalog = e.CatalogRead()
	if _, err := capturePGIndexes(context.Background(), old, mixed, ref, d); err == nil {
		t.Fatal("mixed authorization capture accepted")
	}
	broken := auth
	broken.roles = foreign.Roles()
	broken.roleRecord = foreign.roles
	if _, err := capturePGIndexes(context.Background(), old, broken, ref, d); err == nil {
		t.Fatal("unexpected authorization failure swallowed")
	}
}

func TestPGIndexesSourceInvokerAndFreshAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE pgi_unit_reader")
	roleExecute(t, e, op, "CREATE TABLE pgi_unit_table(n INT,p TEXT)")
	roleExecute(t, e, op, "CREATE INDEX pgi_unit_index ON pgi_unit_table(n)")
	parent := roleTableRef(t, e, "pgi_unit_table")
	reader, _ := e.Roles().Lookup("pgi_unit_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION pgi_unit_reader")
	capture := func() *querySource {
		t.Helper()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		_, ref := pgIndexesUnitSource(t, e.privateCatalogLocked(scope.owner.catalog))
		// A surrounding definition principal must not replace the operation actor.
		s, err := e.openRelationSourceLocked(scope, ref, e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), queryRowMark{}, nil, nil)
		err = scope.Finish(err)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if len(pgIndexesUnitRows(t, capture())) != 0 {
		t.Fatal("definition owner disclosed denied row")
	}
	column := parent
	column.Address.SubID = 2
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old := capture()
	if len(pgIndexesUnitRows(t, old)) != 1 {
		t.Fatal("new column grant not captured")
	}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	if len(pgIndexesUnitRows(t, capture())) != 0 || len(pgIndexesUnitRows(t, old)) != 1 {
		t.Fatal("fresh authorization or prior result ownership lost")
	}
}
