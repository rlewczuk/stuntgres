package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var pgAuthIDColumns = []string{"oid", "rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolbypassrls", "rolconnlimit", "rolpassword", "rolvaliduntil"}
var pgAuthIDTypes = []string{"OID", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "TEXT", "TIMESTAMPTZ"}

func pgAuthIDRow(name string, id int64, login bool) []any {
	return []any{id, name, false, true, false, false, login, false, false, int64(-1), nil, nil}
}

func pgAuthIDSelect(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgAuthIDColumns, pgAuthIDTypes, rows, args...)
	s.Bag = true
	return s
}

// All non-secret expectations exercise direct, freshly prepared and retained
// public consumers. Secret assertions use unrecorded callbacks instead.
func pgAuthIDRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	pgUserBoth(t, p, b, s)
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmt, err := conn.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.PgDescriptionExecutorRows(t, p, b, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: s.Query}, s)
}

func pgAuthIDMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, pgAuthIDColumns) {
		t.Fatal("authid column order")
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 12 {
		t.Fatal("authid metadata width")
	}
	for i, m := range meta {
		scan := reflect.TypeFor[bool]()
		switch i {
		case 0, 9:
			scan = reflect.TypeFor[int64]()
		case 1, 10:
			scan = reflect.TypeFor[string]()
		case 11:
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && (i == 0 || i == 1) {
			scan = reflect.TypeFor[any]()
		}
		if pg && i == 9 {
			scan = reflect.TypeFor[int32]()
		}
		if m.Name() != pgAuthIDColumns[i] || m.DatabaseTypeName() != pgAuthIDTypes[i] || m.ScanType() != scan {
			t.Fatal("authid metadata type", i)
		}
		nullable, known := m.Nullable()
		if known != !pg || nullable != (!pg && i >= 10) {
			t.Fatal("authid nullability", i)
		}
		length, lengthKnown := m.Length()
		wantLength := int64(0)
		if i == 10 {
			wantLength = math.MaxInt64
		}
		if length != wantLength || lengthKnown != (i == 10) {
			t.Fatal("authid length", i)
		}
		if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("authid decimal metadata", i)
		}
	}
}

func TestReferencePGAuthIDShape(t *testing.T) {
	pgUserScenario(t, []string{"pga_shape"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pga_shape")
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_authid WHERE oid=$1", "SELECT * FROM pg_authid WHERE FALSE AND oid=$1", "SELECT * FROM pg_authid WHERE oid=$1 LIMIT 0"} {
			rows := [][]any{}
			if q == "SELECT * FROM pg_catalog.pg_authid WHERE oid=$1" {
				rows = [][]any{pgAuthIDRow("pga_shape", id, true)}
			}
			s := pgAuthIDSelect(q, rows, id)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgAuthIDMetadata(t, rows, b.PG) }
			pgAuthIDRoutes(t, p, b, s)
		}
		pgAuthIDRoutes(t, p, b, fixture.Select("SELECT oid::bigint AS id,relnamespace::bigint AS ns,relkind,reltype::bigint AS row_type,relisshared,reltablespace::bigint AS space,relhasindex,relnatts FROM pg_class WHERE oid='pg_catalog.pg_authid'::regclass", []string{"id", "ns", "relkind", "row_type", "relisshared", "space", "relhasindex", "relnatts"}, []string{"INT8", "INT8", "CHAR", "INT8", "BOOL", "INT8", "BOOL", "INT2"}, [][]any{{int64(1260), int64(11), "r", int64(2842), true, int64(1664), true, int64(12)}}))
		pgAuthIDRoutes(t, p, b, fixture.Select("SELECT t.oid::bigint AS row_type,t.typrelid::bigint AS relation,t.typarray::bigint AS array_type,a.typelem::bigint AS element FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=2842", []string{"row_type", "relation", "array_type", "element"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(2842), int64(1260), int64(10057), int64(2842)}}))
		var attrs [][]any
		oids := []int64{26, 19, 16, 16, 16, 16, 16, 16, 16, 23, 25, 1184}
		for i, name := range pgAuthIDColumns {
			collation := int64(0)
			if i == 1 || i == 10 {
				collation = 950
			}
			attrs = append(attrs, []any{name, int64(i + 1), oids[i], int64(-1), i < 10, collation, int64(0)})
		}
		fixture.PGAttributeNameCastWitness(t, p, b, fixture.Select("SELECT attname::text AS name,attnum::bigint AS num,atttypid::bigint AS type_oid,atttypmod::bigint AS typmod,attnotnull,attcollation::bigint AS collation,attndims::bigint AS dimensions FROM pg_attribute WHERE attrelid=1260 AND attnum>0 ORDER BY attnum", []string{"name", "num", "type_oid", "typmod", "attnotnull", "collation", "dimensions"}, []string{"TEXT", "INT8", "INT8", "INT8", "BOOL", "INT8", "INT8"}, attrs))
		pgAuthIDRoutes(t, p, b, fixture.Select("SELECT tablename,tablespace,hasindexes FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_authid'", []string{"tablename", "tablespace", "hasindexes"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"pg_authid", "pg_global", true}}))
		pgAuthIDRoutes(t, p, b, fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_authid'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
		pgUserExec(t, p, b, "DROP ROLE pga_shape")
	})
}
