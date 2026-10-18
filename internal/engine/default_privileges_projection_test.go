package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func defaultUnitProjection(t *testing.T, catalog CatalogRead, auth AuthorizationView) (*querySource, [][]string) {
	t.Helper()
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tablePrivilegesOID})
	ref := catalog.owner.domain.Ref(catalog.Epoch(), source.address)
	rows, err := captureTablePrivileges(context.Background(), catalog, auth, "unit_database", ref, source)
	if err != nil {
		t.Fatal(err)
	}
	var actual [][]string
	for _, row := range rows.anonymous {
		name, err := row[4].Text()
		if err != nil {
			t.Fatal("projection name carrier", err)
		}
		if !strings.HasPrefix(name, "proof_") {
			continue
		}
		values := make([]string, len(row))
		for i, value := range row {
			values[i], err = value.Text()
			if err != nil || value.Type() != source.columns[i].Usage.Type {
				t.Fatal("projection type/null", i, value, err)
			}
		}
		actual = append(actual, values)
	}
	sort.Slice(actual, func(i, j int) bool { return strings.Join(actual[i], "\x00") < strings.Join(actual[j], "\x00") })
	return rows, actual
}
func defaultUnitSortRows(rows [][]string) {
	sort.Slice(rows, func(i, j int) bool { return strings.Join(rows[i], "\x00") < strings.Join(rows[j], "\x00") })
}

func TestDefaultPrivilegesProjection(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := cloneCatalog(f.e.catalog.state)
	ownerSelect := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.SelectPrivilege}
	publicInsert := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.InsertPrivilege}
	memberUpdate := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.member}, Privileges: authorization.UpdatePrivilege}
	specs := []struct {
		name string
		kind relationKind
		acl  authorization.ACLValue
	}{
		{"proof_absent", tableRelation, authorization.ACLValue{}},
		{"proof_named", viewRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerSelect, defaultUnitReaderEntry(f, authorization.SelectPrivilege, authorization.SelectPrivilege), publicInsert}}},
		{"proof_empty", tableRelation, authorization.ACLValue{Explicit: true}},
		{"proof_partition", partitionedRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_member", tableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{memberUpdate}}},
		{"proof_update_only", tableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UpdatePrivilege, 0)}}},
		{"proof_maintain", tableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.MaintainPrivilege, authorization.MaintainPrivilege)}}},
		{"proof_column_only", tableRelation, authorization.ACLValue{Explicit: true}},
		{"proof_mv", materializedViewRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_sequence", sequenceRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_index", indexRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_composite", compositeRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
	}
	for i, spec := range specs {
		d := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20100 + i)}, namespace: f.schema, name: spec.name, owner: f.owner, revision: 2, relationKind: spec.kind, acl: spec.acl}
		if spec.name == "proof_column_only" {
			d.columnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}
		}
		state.setDescriptor(d.address, d)
	}
	catalog := f.e.catalog.read(state)
	f.e.mu.Lock()
	auth, err := f.e.authorizationLocked(f.op)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = catalog
	auth.recaptureCatalog(auth.catalog)
	held, got := defaultUnitProjection(t, catalog, auth)
	var want [][]string
	for _, privilege := range []string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
		hierarchy := "NO"
		if privilege == "SELECT" {
			hierarchy = "YES"
		}
		want = append(want, []string{"default_owner", "default_owner", "unit_database", "default_schema", "proof_absent", privilege, "YES", hierarchy})
	}
	want = append(want,
		[]string{"default_owner", "default_owner", "unit_database", "default_schema", "proof_named", "SELECT", "YES", "YES"},
		[]string{"default_owner", "default_reader", "unit_database", "default_schema", "proof_named", "SELECT", "YES", "YES"},
		[]string{"default_owner", "PUBLIC", "unit_database", "default_schema", "proof_named", "INSERT", "NO", "NO"},
		[]string{"default_owner", "default_reader", "unit_database", "default_schema", "proof_partition", "SELECT", "NO", "YES"},
		[]string{"default_owner", "default_member", "unit_database", "default_schema", "proof_member", "UPDATE", "YES", "NO"},
		[]string{"default_owner", "default_reader", "unit_database", "default_schema", "proof_update_only", "UPDATE", "NO", "NO"},
	)
	defaultUnitSortRows(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("projection origins/kinds/bits/options", got, want)
	}
	auth.state.CurrentUser = f.reader
	_, readerRows := defaultUnitProjection(t, catalog, auth)
	readerWant := [][]string{
		{"default_owner", "default_reader", "unit_database", "default_schema", "proof_named", "SELECT", "YES", "YES"},
		{"default_owner", "PUBLIC", "unit_database", "default_schema", "proof_named", "INSERT", "NO", "NO"},
		{"default_owner", "default_reader", "unit_database", "default_schema", "proof_partition", "SELECT", "NO", "YES"},
		{"default_owner", "default_reader", "unit_database", "default_schema", "proof_update_only", "UPDATE", "NO", "NO"},
	}
	defaultUnitSortRows(readerWant)
	if !reflect.DeepEqual(readerRows, readerWant) {
		t.Fatal("grantee visibility was filtered by namespace/table SELECT or invented inherited origins", readerRows)
	}
	auth.state.CurrentUser = f.member
	_, memberRows := defaultUnitProjection(t, catalog, auth)
	if !reflect.DeepEqual(memberRows, want) {
		t.Fatal("inherited grantor visibility", memberRows)
	}
	roles := cloneRoleFixture(auth.roleRecord)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.member {
			edges[i].Inherit = false
			edges[i].Set = true
		}
	}
	setFixtureEdges(roles, edges)
	auth.roleRecord = roles
	auth.recaptureCatalog(catalog)
	_, setRows := defaultUnitProjection(t, catalog, auth)
	setWant := [][]string{{"default_owner", "PUBLIC", "unit_database", "default_schema", "proof_named", "INSERT", "NO", "NO"}, {"default_owner", "default_member", "unit_database", "default_schema", "proof_member", "UPDATE", "NO", "NO"}}
	defaultUnitSortRows(setWant)
	if !reflect.DeepEqual(setRows, setWant) {
		t.Fatal("SET-only disclosed grantor rows or inferred ownership", setRows)
	}
	// Source rows are detached from live role/catalog changes; a later opening
	// captures those changes, never mutating already returned anonymous rows.
	before := make([][]sqltype.Value, len(held.anonymous))
	for i, row := range held.anonymous {
		before[i] = append([]sqltype.Value(nil), row...)
	}
	changed := cloneCatalog(state)
	named := *changed.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20101})
	named.acl = authorization.ACLValue{Explicit: true}
	changed.setDescriptor(named.address, &named)
	actor := cloneRoleFixture(roles)
	renamed := fixtureRole(actor, f.reader)
	renamed.Name = "renamed_reader"
	putFixtureRole(actor, renamed)

	auth.roleRecord = actor
	auth.recaptureCatalog(f.e.catalog.read(changed))
	auth.state.CurrentUser = f.reader
	_, fresh := defaultUnitProjection(t, auth.catalog, auth)
	freshWant := [][]string{{"default_owner", "renamed_reader", "unit_database", "default_schema", "proof_partition", "SELECT", "NO", "YES"}, {"default_owner", "renamed_reader", "unit_database", "default_schema", "proof_update_only", "UPDATE", "NO", "NO"}}
	defaultUnitSortRows(freshWant)
	if !reflect.DeepEqual(fresh, freshWant) || !reflect.DeepEqual(held.anonymous, before) {
		t.Fatal("fresh capture/returned row lifetime", fresh)
	}
	source := state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tablePrivilegesOID})
	names := []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}
	for i, column := range source.columns {
		typ := sqltype.Name
		length, known := int64(0), false
		if i >= 5 {
			typ = sqltype.VarChar
			length, known = math.MaxInt64, true
		}
		if i >= 6 {
			length = 3
		}
		actualLength, actualKnown := column.Usage.Length()
		if column.Name != names[i] || column.Usage.Type.ID() != typ || column.Usage.Type.IsDomain() || actualLength != length || actualKnown != known || !column.Nullable || !column.NullabilityKnown || i >= 5 && column.Usage.Collation.ID != 950 {
			t.Fatal("source carriers", i, column, actualLength, actualKnown)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ref := f.e.catalog.domain.Ref(catalog.Epoch(), source.address)
	rows, err := captureTablePrivileges(canceled, catalog, AuthorizationView{}, "unit_database", ref, source)
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source exposed partial rows", rows, err)
	}
}
