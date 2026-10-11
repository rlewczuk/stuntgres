package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgGroupColumns = []string{"groname", "grosysid", "grolist"}
var pgGroupTypes = []string{"NAME", "OID", "_OID"}

// Pinned PostgreSQL18.0 pg_views reconstruction, independently observed by root.
const pgGroupViewDefinition = " SELECT rolname AS groname,\n    oid AS grosysid,\n    ARRAY( SELECT pg_auth_members.member\n           FROM pg_auth_members\n          WHERE (pg_auth_members.roleid = pg_authid.oid)) AS grolist\n   FROM pg_authid\n  WHERE (NOT rolcanlogin);"

func pgGroupScenario(t *testing.T, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgGroupCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, name, options string) int64 {
	t.Helper()
	pgRolesExec(t, p, b, "CREATE ROLE "+name+" "+options)
	var id int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", strings.Trim(name, "\"")).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal("role identity", id, err)
	}
	return id
}

func pgGroupRow(name string, id int64, members ...int64) []any {
	parts := make([]string, len(members))
	for i, n := range members {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return []any{name, id, "{" + strings.Join(parts, ",") + "}"}
}

func pgGroupArray(t *testing.T, value any) string {
	t.Helper()
	s, ok := value.(string)
	if !ok || !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		t.Fatalf("nonnull one-dimensional OID array: %#v", value)
	}
	if s == "{}" {
		return s
	}
	parts := strings.Split(s[1:len(s)-1], ",")
	ids := make([]uint64, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			t.Fatal("non-NULL OID element", part, err)
		}
		ids[i] = n
	}
	slices.Sort(ids)
	for i, n := range ids {
		parts[i] = strconv.FormatUint(n, 10)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func pgGroupMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !slices.Equal(names, pgGroupColumns) {
		t.Fatal("exact three group columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 3 {
		t.Fatal(meta, err)
	}
	pgGroupSavedMetadata(t, meta, pg)
	return meta
}

func pgGroupSavedMetadata(t *testing.T, meta []*sql.ColumnType, pg bool) {
	t.Helper()
	for i, m := range meta {
		scan := reflect.TypeFor[string]()
		if i == 1 {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if m.Name() != pgGroupColumns[i] || m.DatabaseTypeName() != pgGroupTypes[i] || m.ScanType() != scan {
			t.Fatalf("metadata %d=%s/%s/%v", i, m.Name(), m.DatabaseTypeName(), m.ScanType())
		}
		if n, known := m.Nullable(); n != !pg || known != !pg {
			t.Fatal("nullability", i, n, known)
		}
		if n, known := m.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if p, s, known := m.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("decimal size", i, p, s, known)
		}
	}
}

func pgGroupCanonical(t *testing.T, rows [][]any, pg bool, raw bool) []string {
	t.Helper()
	result := make([]string, len(rows))
	for i, row := range rows {
		if len(row) != 3 {
			t.Fatal(row)
		}
		name, id, list := row[0], row[1], row[2]
		if raw {
			name = fixture.OidReferenceValue(t, pg, "NAME", name)
			id = fixture.OidReferenceValue(t, pg, "OID", id)
			list = fixture.OidReferenceValue(t, pg, "_OID", list)
		}
		result[i] = fmt.Sprintf("%s/%d/%s", name, id, pgGroupArray(t, list))
	}
	slices.Sort(result)
	return result
}

// Check both owned consumer and middleware buffers, then compare independent
// identities and element bags. Neither row order nor array order is promised.
func pgGroupConsume(t *testing.T, rows *sql.Rows, b fixture.Backend, q string, want [][]any, record *fixture.Operation) {
	t.Helper()
	defer rows.Close()
	meta := pgGroupMetadata(t, rows, b.PG)
	var got [][]any
	for rows.Next() {
		row := make([]any, 3)
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	actual := pgGroupCanonical(t, got, b.PG, true)
	expected := pgGroupCanonical(t, want, b.PG, false)
	if !slices.Equal(actual, expected) {
		t.Fatalf("group rows=%v want %v", actual, expected)
	}
	b.Recorder.Mu.Lock()
	op := record
	if op == nil {
		for _, candidate := range b.Recorder.Operations {
			if candidate.SQL == q && candidate.Err == nil && candidate.Columns != nil {
				op = candidate
			}
		}
	}
	if op == nil {
		b.Recorder.Mu.Unlock()
		t.Fatal("missing middleware result")
	}
	captured := make([][]any, len(op.Rows))
	for i, row := range op.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = value
		}
	}
	columns, closed, closeErr := slices.Clone(op.Columns), op.Closed, op.CloseErr
	b.Recorder.Mu.Unlock()
	if !slices.Equal(columns, pgGroupColumns) || !closed || closeErr != nil || !slices.Equal(pgGroupCanonical(t, captured, b.PG, true), expected) {
		t.Fatal("middleware group buffers or closure differ")
	}
	pgGroupSavedMetadata(t, meta, b.PG)
}

func pgGroupQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool, want [][]any, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	if prepared {
		stmt, err := e.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
	}
	rows, err := e.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	pgGroupConsume(t, rows, b, q, want, nil)
}

func pgGroupBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, want [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		pgGroupQuery(t, p, b, b.DB, q, prepared, want, args...)
	}
}

func TestReferencePGGroupShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgg_shape")
	pgGroupScenario(t, []string{"pgg_shape", "PggMixed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_group WHERE FALSE", "SELECT * FROM pg_catalog.pg_group WHERE FALSE"} {
			pgGroupBoth(t, p, b, q, nil)
		}
		id := pgGroupCreate(t, p, b, "pgg_shape", "NOLOGIN")
		mixed := pgGroupCreate(t, p, b, "\"PggMixed\"", "NOLOGIN")
		pgGroupBoth(t, p, b, "SELECT * FROM pg_catalog.pg_group WHERE grosysid=$1", [][]any{pgGroupRow("pgg_shape", id)}, id)
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname=$1", [][]any{pgGroupRow("PggMixed", mixed)}, "PggMixed")
		for _, q := range []string{"SELECT * FROM pg_group WHERE grosysid=$1", "SELECT * FROM pg_group WHERE groname=$1"} {
			pgGroupBoth(t, p, b, q, nil, nil)
		}
		pgGroupBoth(t, p, b, "SELECT * FROM pg_group WHERE groname='pggmixed'", nil)
		pgRolesBoth(t, p, b, fixture.Select("SELECT relname,relkind,relnatts FROM pg_class WHERE relname='pg_group'", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_group", "v", int64(3)}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT schemaname,viewname,viewowner,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_group'", []string{"schemaname", "viewname", "viewowner", "definition"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{"pg_catalog", "pg_group", pgTableViewsOwner(b), pgGroupViewDefinition}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT tablename FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_group'", []string{"tablename"}, []string{"NAME"}, nil))
		for _, q := range []string{"CREATE SCHEMA pgg_shape", "CREATE TABLE pgg_shape.pg_group(marker TEXT)", "SET search_path TO pgg_shape,pg_catalog"} {
			pgRolesExec(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgg_shape.pg_group VALUES('shadow')", 1))
		pgRolesBoth(t, p, b, fixture.Select("SELECT * FROM pg_group", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}}))
		pgGroupBoth(t, p, b, "SELECT * FROM pg_catalog.pg_group WHERE grosysid=$1", [][]any{pgGroupRow("pgg_shape", id)}, id)
		pgRolesExec(t, p, b, "RESET search_path")
	})
}

func TestReferencePGGroupOracleCatalog(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("pg_attribute/pg_type witness requires pinned PostgreSQL")
	}
	p := fixture.NewScenario(t)
	if p.Postgres == nil {
		t.Fatal("enabled reference backend unavailable")
	}
	b := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull,attcollation::bigint AS collation,attnum::bigint AS number FROM pg_attribute WHERE attrelid='pg_catalog.pg_group'::regclass AND attnum>0 ORDER BY attnum", []string{"name", "type_oid", "typmod", "attnotnull", "collation", "number"}, []string{"TEXT", "INT8", "INT8", "BOOL", "INT8", "INT8"}, [][]any{{"groname", int64(19), int64(-1), false, int64(950), int64(1)}, {"grosysid", int64(26), int64(-1), false, int64(0), int64(2)}, {"grolist", int64(1028), int64(-1), false, int64(0), int64(3)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS owner_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_group'::regclass", []string{"row_link", "owner_link", "element_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}
