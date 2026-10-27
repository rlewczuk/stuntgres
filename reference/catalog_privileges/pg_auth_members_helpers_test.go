package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"stuntgres/reference/fixture"
	"testing"
)

var authMembersColumns = []string{"oid", "roleid", "member", "grantor", "admin_option", "inherit_option", "set_option"}
var authMembersTypes = []string{"OID", "OID", "OID", "OID", "BOOL", "BOOL", "BOOL"}

type authMemberRow struct {
	oid, role, member, grantor int64
	admin, inherit, set bool
}

func authMembersMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !slices.Equal(names, authMembersColumns) { t.Fatal(names, err) }
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 7 { t.Fatal(meta, err) }
	authMembersSavedMetadata(t, meta, pg)
	return meta
}

func authMembersSavedMetadata(t *testing.T, meta []*sql.ColumnType, pg bool) {
	t.Helper()
	for i, m := range meta {
		scan := reflect.TypeFor[bool]()
		if i < 4 {
			scan = reflect.TypeFor[int64]()
			if pg { scan = reflect.TypeFor[any]() }
		}
		if m.Name() != authMembersColumns[i] || m.DatabaseTypeName() != authMembersTypes[i] || m.ScanType() != scan { t.Fatal("column metadata", i, m) }
		if nullable, known := m.Nullable(); nullable || known != !pg { t.Fatal("nullability", i, nullable, known) }
		if n, known := m.Length(); n != 0 || known { t.Fatal("length", i, n, known) }
		if p, s, known := m.DecimalSize(); p != 0 || s != 0 || known { t.Fatal("precision", i, p, s, known) }
	}
}

func authMembersConsume(t *testing.T, rows *sql.Rows, pg bool) []authMemberRow {
	t.Helper()
	defer rows.Close()
	meta := authMembersMetadata(t, rows, pg)
	var result []authMemberRow
	seen := map[int64]bool{}
	for rows.Next() {
		var raw [7]any
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6]); err != nil { t.Fatal(err) }
		var ids [4]int64
		for i := range ids {
			v := fixture.OidReferenceValue(t, pg, "OID", raw[i])
			var ok bool
			ids[i], ok = v.(int64)
			if !ok || ids[i] <= 0 || ids[i] > 4294967295 { t.Fatal("OID carrier", v) }
		}
		var flags [3]bool
		for i := range flags {
			var ok bool
			flags[i], ok = raw[i+4].(bool)
			if !ok { t.Fatal("boolean carrier", raw[i+4]) }
		}
		if seen[ids[0]] { t.Fatal("duplicate row identity", ids[0]) }
		seen[ids[0]] = true
		result = append(result, authMemberRow{ids[0], ids[1], ids[2], ids[3], flags[0], flags[1], flags[2]})
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	authMembersSavedMetadata(t, meta, pg)
	return result
}

func authMembersRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, args ...any) []authMemberRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, q, args...)
	if err != nil { t.Fatal(err) }
	return authMembersConsume(t, rows, b.PG)
}

func authMembersBag(rows []authMemberRow, identities bool) []string {
	bag := make([]string, len(rows))
	for i, r := range rows {
		id := int64(0)
		if identities { id = r.oid }
		bag[i] = fmt.Sprintf("%d/%d/%d/%d/%v/%v/%v", id, r.role, r.member, r.grantor, r.admin, r.inherit, r.set)
	}
	slices.Sort(bag)
	return bag
}

func authMembersWant(t *testing.T, got, want []authMemberRow, identities bool) {
	t.Helper()
	if !slices.Equal(authMembersBag(got, identities), authMembersBag(want, identities)) { t.Fatalf("memberships=%v want %v", got, want) }
}

func authMembersBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want []authMemberRow, args ...any) {
	t.Helper()
	authMembersWant(t, authMembersRead(t, p, b, e, q, args...), want, true)
	stmt, err := e.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
	authMembersWant(t, authMembersRead(t, p, b, held, q, args...), want, true)
}

func authMembersOne(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, role int64) authMemberRow {
	t.Helper()
	rows := authMembersRead(t, p, b, e, "SELECT * FROM pg_catalog.pg_auth_members WHERE roleid=$1", role)
	if len(rows) != 1 { t.Fatal("one direct membership", rows) }
	return rows[0]
}
