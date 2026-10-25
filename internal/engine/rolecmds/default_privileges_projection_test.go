package rolecmds_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func defaultUnitProjection(t *testing.T, catalog catalog.Read, auth core.AuthorizationView) (*exec.Source, [][]string) {
	t.Helper()
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TablePrivilegesOID})
	ref := catalog.Owner().Domain.Ref(catalog.Epoch(), source.Address())
	rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Ref: ref, Source: source, Roles: (auth).Roles, Owner: (auth).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Temporary: core.CatalogSourceTemporaryOwner(auth), Database: "unit_database"})}, captureTablePrivileges)
	if err != nil {
		t.Fatal(err)
	}
	var actual [][]string
	for _, row := range rows.Anonymous {
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
			if err != nil || value.Type() != source.Columns[i].Usage.Type {
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
	state := catalog.CloneState(f.e.Catalog.State)
	ownerSelect := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.owner}, Privileges: authorization.SelectPrivilege}
	publicInsert := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.InsertPrivilege}
	memberUpdate := authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.member}, Privileges: authorization.UpdatePrivilege}
	specs := []struct {
		name string
		kind catalog.RelationKind
		acl  authorization.ACLValue
	}{
		{"proof_absent", catalog.TableRelation, authorization.ACLValue{}},
		{"proof_named", catalog.ViewRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerSelect, defaultUnitReaderEntry(f, authorization.SelectPrivilege, authorization.SelectPrivilege), publicInsert}}},
		{"proof_empty", catalog.TableRelation, authorization.ACLValue{Explicit: true}},
		{"proof_partition", catalog.PartitionedRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_member", catalog.TableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{memberUpdate}}},
		{"proof_update_only", catalog.TableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UpdatePrivilege, 0)}}},
		{"proof_maintain", catalog.TableRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.MaintainPrivilege, authorization.MaintainPrivilege)}}},
		{"proof_column_only", catalog.TableRelation, authorization.ACLValue{Explicit: true}},
		{"proof_mv", catalog.MaterializedViewRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_sequence", catalog.SequenceRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_index", catalog.IndexRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
		{"proof_composite", catalog.CompositeRelation, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}},
	}
	for i, spec := range specs {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(20100 + i)}, ObjectNamespace: f.schema, ObjectName: spec.name, Owner: f.owner, ObjectRevision: 2, RelationKind: spec.kind, Security: catalog.RelationSecurity{ACL: spec.acl}}
		if spec.name == "proof_column_only" {
			d.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}
		}
		state.SetDescriptor(d.Address(), d)
	}
	view := f.e.Catalog.Read(state)
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(f.op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	held, got := defaultUnitProjection(t, view, auth)
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
	auth.State.CurrentUser = f.reader
	_, readerRows := defaultUnitProjection(t, view, auth)
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
	auth.State.CurrentUser = f.member
	_, memberRows := defaultUnitProjection(t, view, auth)
	if !reflect.DeepEqual(memberRows, want) {
		t.Fatal("inherited grantor visibility", memberRows)
	}
	roles := cloneRoleFixture(auth.RoleRecord)
	edges := fixtureEdges(roles)
	for i := range edges {
		if edges[i].Member == f.member {
			edges[i].Inherit = false
			edges[i].Set = true
		}
	}
	setFixtureEdges(roles, edges)
	auth.RoleRecord = roles
	auth.Recapture(view)
	_, setRows := defaultUnitProjection(t, view, auth)
	setWant := [][]string{{"default_owner", "PUBLIC", "unit_database", "default_schema", "proof_named", "INSERT", "NO", "NO"}, {"default_owner", "default_member", "unit_database", "default_schema", "proof_member", "UPDATE", "NO", "NO"}}
	defaultUnitSortRows(setWant)
	if !reflect.DeepEqual(setRows, setWant) {
		t.Fatal("SET-only disclosed grantor rows or inferred ownership", setRows)
	}
	// Source rows are detached from live role/catalog changes; a later opening
	// captures those changes, never mutating already returned anonymous rows.
	before := make([][]sqltype.Value, len(held.Anonymous))
	for i, row := range held.Anonymous {
		before[i] = append([]sqltype.Value(nil), row...)
	}
	changed := catalog.CloneState(state)
	named := *changed.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20101})
	named.Security.ACL = authorization.ACLValue{Explicit: true}
	changed.SetDescriptor(named.Address(), &named)
	actor := cloneRoleFixture(roles)
	renamed := fixtureRole(actor, f.reader)
	renamed.Name = "renamed_reader"
	putFixtureRole(actor, renamed)

	auth.RoleRecord = actor
	auth.Recapture(f.e.Catalog.Read(changed))
	auth.State.CurrentUser = f.reader
	_, fresh := defaultUnitProjection(t, auth.Catalog, auth)
	freshWant := [][]string{{"default_owner", "renamed_reader", "unit_database", "default_schema", "proof_partition", "SELECT", "NO", "YES"}, {"default_owner", "renamed_reader", "unit_database", "default_schema", "proof_update_only", "UPDATE", "NO", "NO"}}
	defaultUnitSortRows(freshWant)
	if !reflect.DeepEqual(fresh, freshWant) || !reflect.DeepEqual(held.Anonymous, before) {
		t.Fatal("fresh capture/returned row lifetime", fresh)
	}
	source := state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TablePrivilegesOID})
	names := []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}
	for i, column := range source.Columns {
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
	ref := f.e.Catalog.Domain.Ref(view.Epoch(), source.Address())
	rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: canceled, Catalog: view, Ref: ref, Source: source, Roles: (core.AuthorizationView{}).Roles, Owner: (core.AuthorizationView{}).Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: core.AuthorizationView{}, Temporary: core.CatalogSourceTemporaryOwner(core.AuthorizationView{}), Database: "unit_database"})}, captureTablePrivileges)
	if rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled source exposed partial rows", rows, err)
	}
}

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = registeredCatalogProducer(12081)
	captureDataTypePrivileges  = registeredCatalogProducer(16201)
	captureExtensionCatalog    = registeredCatalogProducer(1255)
	capturePGAM                = registeredCatalogProducer(2601)
	capturePGAggregate         = registeredCatalogProducer(2600)
	capturePGAttribute         = registeredCatalogProducer(1249)
	capturePGCast              = registeredCatalogProducer(2605)
	capturePGCollation         = registeredCatalogProducer(3456)
	capturePGConstraint        = registeredCatalogProducer(2606)
	capturePGCursors           = registeredCatalogProducer(12077)
	capturePGDatabase          = registeredCatalogProducer(1262)
	capturePGDefaultACL        = registeredCatalogProducer(826)
	capturePGDepend            = registeredCatalogProducer(2608)
	capturePGDescription       = registeredCatalogProducer(2609)
	capturePGEnum              = registeredCatalogProducer(3501)
	capturePGGroup             = registeredCatalogProducer(12008)
	capturePGIndexes           = registeredCatalogProducer(12002)
	capturePGNamespace         = registeredCatalogProducer(2615)
	capturePGOpclass           = registeredCatalogProducer(2616)
	capturePGOperator          = registeredCatalogProducer(2617)
	capturePGParameterACL      = registeredCatalogProducer(6243)
	capturePGRange             = registeredCatalogProducer(3541)
	capturePGRoles             = registeredCatalogProducer(12007)
	capturePGSequence          = registeredCatalogProducer(2224)
	capturePGSettings          = registeredCatalogProducer(16202)
	capturePGShDescription     = registeredCatalogProducer(2396)
	capturePGTablespace        = registeredCatalogProducer(1213)
	capturePGType              = registeredCatalogProducer(1247)
	capturePGUser              = registeredCatalogProducer(12014)
	capturePreparedSource      = registeredCatalogProducer(12001)
	captureTablePrivileges     = registeredCatalogProducer(16200)
	captureTableViewSummary    = registeredCatalogProducer(12000)
	captureTimezoneSource      = registeredCatalogProducer(12004)
)

func defaultUnitReaderEntry(f defaultUnitFixture, privileges, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: privileges, GrantOptions: options}
}

func registeredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
}
