package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var pgRolesColumns = []string{"rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit", "rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig", "oid"}
var pgRolesTypes = []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "TIMESTAMPTZ", "BOOL", "_TEXT", "OID"}

// Pinned PostgreSQL18.0 pg_views reconstruction, independently observed by root.
const pgRolesViewDefinition = " SELECT pg_authid.rolname,\n    pg_authid.rolsuper,\n    pg_authid.rolinherit,\n    pg_authid.rolcreaterole,\n    pg_authid.rolcreatedb,\n    pg_authid.rolcanlogin,\n    pg_authid.rolreplication,\n    pg_authid.rolconnlimit,\n    '********'::text AS rolpassword,\n    pg_authid.rolvaliduntil,\n    pg_authid.rolbypassrls,\n    s.setconfig AS rolconfig,\n    pg_authid.oid\n   FROM (pg_authid\n     LEFT JOIN pg_db_role_setting s ON (((pg_authid.oid = s.setrole) AND (s.setdatabase = (0)::oid))));"

func pgRolesScenario(t *testing.T, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgRolesExec(t *testing.T, p *fixture.Pair, b fixture.Backend, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
}

func pgRolesCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	return pgShDescriptionCreateRole(t, p, b, name, "public")
}

func pgRolesRow(name string, id int64, expiry any) []any {
	return []any{name, false, true, false, false, false, false, int64(-1), "********", expiry, false, nil, id}
}

func pgRolesSelect(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgRolesColumns, pgRolesTypes, rows, args...)
	s.Bag = true
	return s
}

func pgRolesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}

func pgRolesMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, pgRolesColumns) {
		t.Fatal("pg_roles columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 13 {
		t.Fatal("pg_roles metadata", meta, err)
	}
	pgRolesSavedMetadata(t, meta, pg)
	return meta
}

func pgRolesSavedMetadata(t *testing.T, meta []*sql.ColumnType, pg bool) {
	t.Helper()
	for i, m := range meta {
		scan := reflect.TypeFor[string]()
		switch i {
		case 7, 12:
			scan = reflect.TypeFor[int64]()
		case 1, 2, 3, 4, 5, 6, 10:
			scan = reflect.TypeFor[bool]()
		case 9:
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && (i == 0 || i == 11 || i == 12) {
			scan = reflect.TypeFor[any]()
		}
		if pg && i == 7 {
			scan = reflect.TypeFor[int32]()
		}
		if m.Name() != pgRolesColumns[i] || m.DatabaseTypeName() != pgRolesTypes[i] || m.ScanType() != scan {
			t.Errorf("column %d name/type/scan=%s/%s/%v want %s/%s/%v", i, m.Name(), m.DatabaseTypeName(), m.ScanType(), pgRolesColumns[i], pgRolesTypes[i], scan)
		}
		nullable, known := m.Nullable()
		if nullable != !pg || known != !pg {
			t.Errorf("column %d nullable=%t/%t", i, nullable, known)
		}
		length, lengthKnown := m.Length()
		wantLength, wantKnown := int64(0), false
		if i == 8 {
			wantLength, wantKnown = math.MaxInt64, true
		}
		if length != wantLength || lengthKnown != wantKnown {
			t.Errorf("column %d length=%d/%t want %d/%t", i, length, lengthKnown, wantLength, wantKnown)
		}
		if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Errorf("column %d decimal=%d/%d/%t", i, precision, scale, known)
		}
	}
}

func TestReferencePGRolesShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgr_shape")
	pgRolesScenario(t, []string{"pgr_shape_login", "PgrMixed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_roles WHERE FALSE", "SELECT * FROM pg_catalog.pg_roles WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				s := pgRolesSelect(q, [][]any{})
				s.Prepared = prepared
				var saved []*sql.ColumnType
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { saved = pgRolesMetadata(t, rows, b.PG) }
				fixture.PgDescriptionRows(t, p, b, s)
				pgRolesSavedMetadata(t, saved, b.PG)
			}
		}
		id := pgRolesCreate(t, p, b, "pgr_shape_login")
		s := pgRolesSelect("SELECT * FROM pg_catalog.pg_roles WHERE oid=$1", [][]any{pgRolesRow("pgr_shape_login", id, nil)}, id)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgRolesMetadata(t, rows, b.PG) }
		pgRolesBoth(t, p, b, s)
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_roles WHERE oid=$1 AND rolvaliduntil IS NULL AND rolconfig IS NULL", [][]any{pgRolesRow("pgr_shape_login", id, nil)}, id))
		pgRolesBoth(t, p, b, pgRolesSelect("SELECT * FROM pg_catalog.pg_roles WHERE oid=$1", [][]any{}, nil))
		for _, arg := range []any{"pgr_shape_login", nil, "pgr_absent"} {
			var expected [][]any
			if arg == "pgr_shape_login" {
				expected = [][]any{pgRolesRow("pgr_shape_login", id, nil)}
			}
			step := pgRolesSelect("SELECT * FROM pg_roles WHERE rolname=$1", expected, arg)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgRolesMetadata(t, rows, b.PG) }
			pgRolesBoth(t, p, b, step)
		}
		pgRolesExec(t, p, b, "CREATE ROLE \"PgrMixed\"")
		pgRolesBoth(t, p, b, fixture.Select("SELECT rolname,rolpassword,rolconfig FROM pg_roles WHERE rolname=$1", []string{"rolname", "rolpassword", "rolconfig"}, []string{"NAME", "TEXT", "_TEXT"}, [][]any{{"PgrMixed", "********", nil}}, "PgrMixed"))
		pgRolesBoth(t, p, b, fixture.Select("SELECT rolname FROM pg_roles WHERE rolname='pgrmixed'", []string{"rolname"}, []string{"NAME"}, [][]any{}))
		pgRolesExec(t, p, b, "DROP ROLE \"PgrMixed\"")
		pgRolesBoth(t, p, b, fixture.Select("SELECT rolconfig,rolname,oid,rolname,rolvaliduntil,rolpassword FROM pg_roles WHERE oid=$1", []string{"rolconfig", "rolname", "oid", "rolname", "rolvaliduntil", "rolpassword"}, []string{"_TEXT", "NAME", "OID", "NAME", "TIMESTAMPTZ", "TEXT"}, [][]any{{nil, "pgr_shape_login", id, "pgr_shape_login", nil, "********"}}, id))
		pgRolesBoth(t, p, b, fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='pg_roles'", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_roles", "v", int64(13)}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT schemaname,viewname,viewowner,definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_roles'", []string{"schemaname", "viewname", "viewowner", "definition"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{"pg_catalog", "pg_roles", pgTableViewsOwner(b), pgRolesViewDefinition}}))
		pgRolesBoth(t, p, b, fixture.Select("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_roles'", []string{"tablename"}, []string{"NAME"}, [][]any{}))
		for _, q := range []string{"CREATE SCHEMA pgr_shape", "CREATE TABLE pgr_shape.pg_roles(marker TEXT)", "SET search_path TO pgr_shape,pg_catalog"} {
			pgRolesExec(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgr_shape.pg_roles VALUES('shadow')", 1))
		pgRolesBoth(t, p, b, fixture.Select("SELECT * FROM pg_roles", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}}))
		pgRolesBoth(t, p, b, s)
		pgRolesExec(t, p, b, "RESET search_path")
		pgRolesExec(t, p, b, "DROP ROLE pgr_shape_login")
	})
}

func TestReferencePGRolesOracleCatalog(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("pg_type metadata witness requires real pinned PostgreSQL")
	}
	p := fixture.NewScenario(t)
	if p.Postgres == nil {
		t.Fatal("enabled reference backend is unavailable")
	}
	t.Run("postgres", func(t *testing.T) {
		b := fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS owner_link,a.typelem=t.oid AS element_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_roles'::regclass", []string{"row_link", "owner_link", "element_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	})
}

func TestReferencePGRolesAttributeCatalog(t *testing.T) {
	pgRolesScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var expected [][]any
		oids := []int64{19, 16, 16, 16, 16, 16, 16, 23, 25, 1184, 16, 1009, 26}
		collations := []int64{950, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0, 950, 0}
		for i, name := range pgRolesColumns {
			expected = append(expected, []any{name, oids[i], int64(-1), false, collations[i]})
		}
		s := fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_roles'::regclass AND attnum>0 ORDER BY attnum", []string{"name", "type_oid", "typmod", "attnotnull", "collation"}, []string{"TEXT", "INT8", "INT8", "BOOL", "INT8"}, expected)
		pgAttributeNameCastWitness(t, p, b, s)
	})
}
