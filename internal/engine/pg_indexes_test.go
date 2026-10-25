package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgIndexesUnitSource(t *testing.T, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12002}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing pg_indexes")
	}
	return d, c.Owner().Domain.Ref(c.Epoch(), a)
}
func pgIndexesUnitCapture(t *testing.T, c catalog.Read, auth core.AuthorizationView) *exec.Source {
	t.Helper()
	d, ref := pgIndexesUnitSource(t, c)
	s, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: c, Ref: ref, Source: d, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth)})}, capturePGIndexes)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func pgIndexesUnitRows(t *testing.T, s *exec.Source) [][]any {
	t.Helper()
	var rows [][]any
	for _, row := range s.Anonymous {
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
	if d.Name() != "pg_indexes" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || len(d.Columns) != 5 || d.ColumnMeta.HighWater != 5 {
		t.Fatal(d)
	}
	for i, col := range d.Columns {
		id := sqltype.Name
		coll := uint32(950)
		if i == 4 {
			id = sqltype.Text
			coll = 100
		}
		typ, ok := c.LookupType(id)
		if !ok || col.Name != names[i] || col.Usage.Type != typ || col.Usage.Type.ID() != id || col.Usage.Typmod != -1 || uint32(col.Usage.Collation.ID) != coll || !col.Nullable || !col.NullabilityKnown || col.OriginTable != 12002 || col.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[names[i]] != i {
			t.Fatal(i, col)
		}
	}
	if err := (core.RelationProvider).Validate(c, d); err != nil {
		t.Fatal(err)
	}
	row := c.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	if row.OwningRelation != d.Address() {
		t.Fatal("row type linkage", row)
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal(d.Security.ACL)
	}
	s := pgIndexesUnitCapture(t, c, catalogAuthorization(e, c))
	if s.Ref != ref || s.Anonymous == nil || len(s.Anonymous) != 0 {
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
				if err := setObjectACL(e, context.Background(), ref, acl); err != nil {
					t.Fatal(err)
				}
			}
			schema := parent
			schema.Address = e.CatalogRead().MustRelation(parent.Address).Namespace()
			schemaACL := authorization.ACLValue{Explicit: true}
			if !tc.schemaDenied {
				schemaACL.Entries = []authorization.ACLEntry{
					{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege},
					{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege},
				}
			}
			if err := setObjectACL(e, context.Background(), schema, schemaACL); err != nil {
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
			auth.State.CurrentUser = reader.Ref
			if tc.owner {
				auth.State.CurrentUser = owner.Ref
			}
			if tc.super {
				auth.State.CurrentUser = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			}
			source := pgIndexesUnitCapture(t, c, auth)
			if source.Anonymous == nil {
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
			acl.Entries = []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}
		}
		if err := setObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	capture := func() (catalog.Read, core.AuthorizationView) {
		c := e.CatalogRead()
		auth := catalogAuthorization(e, c)
		auth.State.CurrentUser = reader.Ref
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
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pgIndexesUnitRows(t, pgIndexesUnitCapture(t, old, auth)), want) {
		t.Fatal("Reset mutated capture")
	}
	d, ref := pgIndexesUnitSource(t, old)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: canceled, Catalog: old, Ref: ref, Source: d, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth)})}, capturePGIndexes); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign := New()
	bad := ref
	bad = foreign.Catalog.Domain.Ref(bad.Epoch, bad.Address)
	for _, invalid := range []ids.ObjectRef{bad, ref.Domain().Ref(ref.Epoch+1, ref.Address), ref.Domain().Ref(ref.Epoch, parent.Address)} {
		if _, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: old, Ref: invalid, Source: d, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth)})}, capturePGIndexes); err == nil {
			t.Fatal("invalid capture accepted", invalid)
		}
	}
	mixed := auth
	mixed.Catalog = e.CatalogRead()
	if _, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: old, Ref: ref, Source: d, Roles: (mixed).Roles, Owner: (mixed).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: mixed, Temporary: core.CatalogSourceTemporaryOwner(mixed)})}, capturePGIndexes); err == nil {
		t.Fatal("mixed authorization capture accepted")
	}
	broken := auth
	broken.Roles = foreign.Roles()
	broken.RoleRecord = foreign.RoleStore.Published
	if _, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: old, Ref: ref, Source: d, Roles: (broken).Roles, Owner: (broken).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: broken, Temporary: core.CatalogSourceTemporaryOwner(broken)})}, capturePGIndexes); err == nil {
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
	capture := func() *exec.Source {
		t.Helper()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		_, ref := pgIndexesUnitSource(t, e.PrivateCatalogLocked(scope.Owner.Catalog))
		// A surrounding definition principal must not replace the operation actor.
		s, err := core.EngineOpenRelationSourceLocked(e, scope, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
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
	if err := setObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	old := capture()
	if len(pgIndexesUnitRows(t, old)) != 1 {
		t.Fatal("new column grant not captured")
	}
	if err := setObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	if len(pgIndexesUnitRows(t, capture())) != 0 || len(pgIndexesUnitRows(t, old)) != 1 {
		t.Fatal("fresh authorization or prior result ownership lost")
	}
}
