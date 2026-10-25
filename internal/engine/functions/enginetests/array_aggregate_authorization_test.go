package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Public SQL denial and PostgreSQL grant witnesses live in reference. This
// component test binds read manifests and checks private ACLs without executing SQL.
func TestArrayAggregateColumnACL(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.aa_column_acl(id INT,v INT,a INT[])")
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), table.Address())
	reader := e.RoleStore.Domain.Principal(1000000)
	roles := cloneRoleFixture(e.RoleStore.Published)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "aa_unit_reader"})

	e.RoleStore.PublishLocked(roles)
	grant := func(object ids.ObjectRef, rights authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: table.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: rights}}}
		if err := enginetest.SetObjectACL(e, context.Background(), object, acl); err != nil {
			t.Fatal(err)
		}
	}
	column := func(index int) ids.ObjectRef {
		object := ref
		object.Address.SubID = table.Attributes[index]
		return object
	}
	grant(ref, 0)
	for _, tc := range []struct {
		query  string
		reads  []int
		denied bool
	}{
		{"SELECT array_agg(v ORDER BY v) FROM aa_column_acl", []int{1}, false},
		{"SELECT array_agg(v) FROM aa_column_acl WHERE FALSE", []int{1}, false},
		{"SELECT array_agg(v ORDER BY id) FROM aa_column_acl", []int{0, 1}, true},
		{"SELECT array_agg(a) FROM aa_column_acl WHERE FALSE", []int{2}, true},
		{"SELECT array_agg(a ORDER BY id) FILTER(WHERE FALSE) FROM aa_column_acl", []int{0, 2}, true},
		{"SELECT array_agg(v) FILTER(WHERE id=1) FROM aa_column_acl", []int{0, 1}, true},
		{"SELECT array_agg(v) FROM aa_column_acl GROUP BY id", []int{0, 1}, true},
		{"SELECT array_agg(v) FROM aa_column_acl HAVING count(id)>0", []int{0, 1}, true},
		{"SELECT array_agg(v) FROM aa_column_acl ORDER BY count(id)", []int{0, 1}, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for i := range table.Columns {
				rights := authorization.Privileges(0)
				if i == 1 {
					rights = authorization.SelectPrivilege
				}
				grant(column(i), rights)
			}
			auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
			b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			query, err := b.BindSelect(enginetest.Statement(t, tc.query).(*parser.Select), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(query.Root.Schema()) != 1 || !query.Root.Schema()[0].Usage.Type.IsArray() || len(b.Query.Sources) != 1 {
				t.Fatal("aggregate result/source shape", query.Root.Schema(), b.Query.Sources)
			}
			reads := b.Query.Sources[0].Uses
			if len(reads) != len(tc.reads) {
				t.Fatalf("source reads %v; want exactly %v", reads, tc.reads)
			}
			for _, want := range tc.reads {
				if !slices.Contains(reads, want) {
					t.Fatalf("source reads %v; missing %d", reads, want)
				}
			}
			check := func(denied bool) {
				t.Helper()
				current := enginetest.CatalogAuthorization(e, e.CatalogRead())
				current.State.CurrentUser = reader
				err := analyze.CheckQuerySources(current, b.Query.Sources)
				if !denied {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table aa_column_acl"}
				if !errors.As(err, &got) || *got != want {
					t.Fatal("source authority diagnostic", got, err)
				}
			}
			check(tc.denied)
			// Grant only actual source attributes: the array result adds no read.
			for _, i := range tc.reads {
				grant(column(i), authorization.SelectPrivilege)
			}
			check(false)
			// Retained manifests consult current ACLs, even on empty/FILTER-false paths.
			grant(column(tc.reads[0]), 0)
			check(true)
		})
	}
}

func domainUnitTable(t *testing.T, e *core.Engine, op *session.Operation, q string) *catalog.Relation {
	t.Helper()
	raw := enginetest.Statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = engine.SchemaTableLocked(e, op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.MustRelation(addresses[0])
}

func cloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}

func putFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
