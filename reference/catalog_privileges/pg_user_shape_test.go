package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var pgUserTypes = []string{"NAME", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TIMESTAMPTZ", "_TEXT"}

// Exact formatted pg_views text observed on the pinned PostgreSQL18.0 oracle.
const pgUserViewDefinition = " SELECT usename,\n    usesysid,\n    usecreatedb,\n    usesuper,\n    userepl,\n    usebypassrls,\n    '********'::text AS passwd,\n    valuntil,\n    useconfig\n   FROM pg_shadow;"

func pgUserScenario(t *testing.T, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgUserExec(t *testing.T, p *fixture.Pair, b fixture.Backend, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
}

func pgUserCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) int64 {
	t.Helper()
	id := fixture.PGShDescriptionCreateRole(t, p, b, name, "public")
	pgUserExec(t, p, b, "ALTER ROLE "+name+" LOGIN")
	return id
}

func pgUserRow(name string, id int64, expiry any) []any {
	return []any{name, id, false, false, false, false, "********", expiry, nil}
}

func pgUserSelect(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGUserColumns, pgUserTypes, rows, args...)
	s.Bag = true
	return s
}

func pgUserBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}

func pgUserMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGUserColumns) {
		t.Fatal("pg_user columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 9 {
		t.Fatal("pg_user metadata", meta, err)
	}
	pgUserSavedMetadata(t, meta, pg)
	return meta
}

func pgUserSavedMetadata(t *testing.T, meta []*sql.ColumnType, pg bool) {
	t.Helper()
	for i, m := range meta {
		scan := reflect.TypeFor[string]()
		switch i {
		case 1:
			scan = reflect.TypeFor[int64]()
		case 2, 3, 4, 5:
			scan = reflect.TypeFor[bool]()
		case 7:
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && (i == 0 || i == 1 || i == 8) {
			scan = reflect.TypeFor[any]()
		}
		if m.Name() != fixture.PGUserColumns[i] || m.DatabaseTypeName() != pgUserTypes[i] || m.ScanType() != scan {
			t.Errorf("column %d name/type/scan=%s/%s/%v want %s/%s/%v", i, m.Name(), m.DatabaseTypeName(), m.ScanType(), fixture.PGUserColumns[i], pgUserTypes[i], scan)
		}
		nullable, known := m.Nullable()
		if nullable != !pg || known != !pg {
			t.Errorf("column %d nullable=%t/%t", i, nullable, known)
		}
		length, lengthKnown := m.Length()
		wantLength, wantKnown := int64(0), false
		if i == 6 {
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

func TestReferencePGUserShape(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgu_shape")
	pgUserScenario(t, []string{"pgu_shape_login"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{"SELECT * FROM pg_user WHERE FALSE", "SELECT * FROM pg_catalog.pg_user WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				s := pgUserSelect(q, [][]any{})
				s.Prepared = prepared
				var saved []*sql.ColumnType
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { saved = pgUserMetadata(t, rows, b.PG) }
				fixture.PgDescriptionRows(t, p, b, s)
				pgUserSavedMetadata(t, saved, b.PG)
			}
		}
		id := pgUserCreate(t, p, b, "pgu_shape_login")
		s := pgUserSelect("SELECT * FROM pg_catalog.pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgu_shape_login", id, nil)}, id)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMetadata(t, rows, b.PG) }
		pgUserBoth(t, p, b, s)
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_catalog.pg_user WHERE usesysid=$1", [][]any{}, nil))
		pgUserBoth(t, p, b, fixture.Select("SELECT useconfig,usename,usesysid,usename,valuntil,passwd FROM pg_user WHERE usesysid=$1", []string{"useconfig", "usename", "usesysid", "usename", "valuntil", "passwd"}, []string{"_TEXT", "NAME", "OID", "NAME", "TIMESTAMPTZ", "TEXT"}, [][]any{{nil, "pgu_shape_login", id, "pgu_shape_login", nil, "********"}}, id))
		pgUserBoth(t, p, b, fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='pg_user'", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_user", "v", int64(9)}}))
		pgUserBoth(t, p, b, fixture.Select("SELECT schemaname,viewname,viewowner,definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_user'", []string{"schemaname", "viewname", "viewowner", "definition"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{"pg_catalog", "pg_user", fixture.PGTableViewsOwner(b), pgUserViewDefinition}}))
		pgUserBoth(t, p, b, fixture.Select("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_user'", []string{"tablename"}, []string{"NAME"}, [][]any{}))
		for _, q := range []string{"CREATE SCHEMA pgu_shape", "CREATE TABLE pgu_shape.pg_user(marker TEXT)", "SET search_path TO pgu_shape,pg_catalog"} {
			pgUserExec(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgu_shape.pg_user VALUES('shadow')", 1))
		pgUserBoth(t, p, b, fixture.Select("SELECT * FROM pg_user", []string{"marker"}, []string{"TEXT"}, [][]any{{"shadow"}}))
		pgUserBoth(t, p, b, s)
		pgUserExec(t, p, b, "RESET search_path")
		pgUserExec(t, p, b, "DROP ROLE pgu_shape_login")
	})
}

func TestReferencePGUserOracleCatalog(t *testing.T) {
	pgUserScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if !b.PG {
			return
		} // Release allocator/view-definition observations remain PostgreSQL-only.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT oid::bigint AS relation_oid,reltype::bigint AS rowtype_oid FROM pg_catalog.pg_class WHERE oid='pg_catalog.pg_user'::regclass", []string{"relation_oid", "rowtype_oid"}, []string{"INT8", "INT8"}, [][]any{{int64(12014), int64(12016)}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='pg_catalog' AND viewname='pg_user'", []string{"definition"}, []string{"TEXT"}, [][]any{{pgUserViewDefinition}}))
	})
}

func TestReferencePGUserAttributeCatalog(t *testing.T) {
	pgUserScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		rows := [][]any{}
		oids := []int64{19, 26, 16, 16, 16, 16, 25, 1184, 1009}
		collations := []int64{950, 0, 0, 0, 0, 0, 100, 0, 950}
		for i, name := range fixture.PGUserColumns {
			rows = append(rows, []any{name, oids[i], int64(-1), false, collations[i]})
		}
		s := fixture.Select("SELECT attname::text AS name,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_user'::regclass AND attnum>0 ORDER BY attnum", []string{"name", "type_oid", "typmod", "attnotnull", "collation"}, []string{"TEXT", "INT8", "INT8", "BOOL", "INT8"}, rows)
		fixture.PGAttributeNameCastWitness(t, p, b, s)
	})
}
