package engine

import (
	"context"
	"errors"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Public SQL denial and PostgreSQL grant witnesses live in reference. This
// component test binds read manifests and checks private ACLs without executing SQL.
func TestArrayAggregateColumnACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
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
		if err := e.SetObjectACL(context.Background(), object, acl); err != nil {
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
			auth := catalogAuthorization(e, e.CatalogRead())
			b := NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth = &auth
			query, err := b.BindSelect(statement(t, tc.query).(*parser.Select), false)
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
				current := catalogAuthorization(e, e.CatalogRead())
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
