package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgAggregateTypes = []string{"REGPROC", "CHAR", "INT2", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "BOOL", "BOOL", "CHAR", "CHAR", "OID", "OID", "INT4", "OID", "INT4", "TEXT", "TEXT"}
var pgAggregateTypeIDs = []int64{24, 18, 21, 24, 24, 24, 24, 24, 24, 24, 24, 16, 16, 18, 18, 26, 26, 23, 26, 23, 25, 25}

const pgAggregateProbe = "SELECT * FROM pg_catalog.pg_aggregate WHERE aggfnoid=$1"

func pgAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}
func pgAggregateSavedMetadata(t *testing.T, meta []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(meta) != 22 {
		t.Fatal("aggregate width", len(meta))
	}
	for i, c := range meta {
		scan := reflect.TypeFor[int64]()
		switch pgAggregateTypes[i] {
		case "REGPROC", "CHAR", "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			switch pgAggregateTypes[i] {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
				scan = reflect.TypeFor[string]()
			}
		}
		if c.Name() != fixture.PGAggregateColumns[i] || c.DatabaseTypeName() != pgAggregateTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), fixture.PGAggregateColumns[i], pgAggregateTypes[i], scan)
		}
		if n, k := c.Nullable(); k != !pg || n != (!pg && i >= 20) {
			t.Fatal("nullability", i, n, k)
		}
		length, lengthKnown := int64(0), false
		if i >= 20 {
			// Both codecs expose unbounded TEXT as MaxInt64, known.
			length, lengthKnown = math.MaxInt64, true
		}
		if n, k := c.Length(); n != length || k != lengthKnown {
			t.Fatal("length", i, n, k)
		}
		if p, s, k := c.DecimalSize(); p != 0 || s != 0 || k {
			t.Fatal("decimal", i, p, s, k)
		}
	}
}
func pgAggregateMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGAggregateColumns) {
		t.Fatal("aggregate columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgAggregateSavedMetadata(t, meta, pg)
}
func pgAggregateRawStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, fixture.PGAggregateColumns, pgAggregateTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgAggregateMetadata(t, r, b.PG) }
	return s
}
func pgAggregateBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Prepared(s))
}
func pgAggregateCountRaw() [][]any {
	return [][]any{{"pg_catalog.count", "n", int64(0), "int8inc", "-", "int8pl", "-", "-", "int8inc", "int8dec", "-", false, false, "r", "r", int64(0), int64(20), int64(0), int64(20), int64(0), "0", "0"}}
}
func pgAggregateNumericProjection() (string, []string) {
	fields := append([]string(nil), fixture.PGAggregateColumns...)
	types := append([]string(nil), pgAggregateTypes...)
	for i, typ := range types {
		if typ == "REGPROC" {
			fields[i] += "::oid AS " + fixture.PGAggregateColumns[i]
			types[i] = "OID"
		}
	}
	return strings.Join(fields, ","), types
}
func pgAggregateAllowlist() string {
	keys := make([]string, len(pgAggregateReleased))
	for i, r := range pgAggregateReleased {
		keys[i] = strconv.FormatInt(r[0].(int64), 10)
	}
	return strings.Join(keys, ",")
}
func pgAggregateDefinitionStep(q string, want [][]any) fixture.Step {
	_, types := pgAggregateNumericProjection()
	return fixture.Select(q, fixture.PGAggregateColumns, types, want)
}
func pgAggregateDefinitions(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	projection, _ := pgAggregateNumericProjection()
	q := "SELECT " + projection + " FROM pg_catalog.pg_aggregate"
	if b.PG {
		q += " WHERE aggfnoid::oid IN (" + pgAggregateAllowlist() + ")"
	}
	s := pgAggregateDefinitionStep(q, pgAggregateReleased)
	s.Bag = true
	pgAggregateBoth(t, p, b, e, s)
}
func TestReferencePGAggregateDefinitions(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { pgAggregateDefinitions(t, p, b, b.DB) })
}
func TestReferencePGAggregateInventory(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT aggfnoid::oid AS aggfnoid FROM pg_catalog.pg_aggregate"
		if b.PG {
			q += " WHERE aggfnoid::oid IN (" + pgAggregateAllowlist() + ")"
		}
		want := make([][]any, len(pgAggregateReleased))
		for i, r := range pgAggregateReleased {
			want[i] = []any{r[0]}
		}
		s := fixture.Select(q, []string{"aggfnoid"}, []string{"OID"}, want)
		s.Bag = true
		pgAggregateBoth(t, p, b, b.DB, s)
		// These are valid released identities, but their native aggregate overloads
		// are deliberately not implemented. PostgreSQL legitimately owns their rows.
		if !b.PG {
			pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT aggfnoid::oid AS aggfnoid FROM pg_aggregate WHERE aggfnoid::oid IN (2125,2141,2050,2051,6373,6374,2797,2798,3526,3527,3564,3565,4189,4190,5099,5100)", []string{"aggfnoid"}, []string{"OID"}, [][]any{}))
		}
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT aggfnoid::oid AS aggfnoid FROM pg_aggregate WHERE aggfnoid::oid IN (3101,3102,3103,3104,3100,2333,2334)", []string{"aggfnoid"}, []string{"OID"}, [][]any{}))
	})
}
func TestReferencePGAggregateShape(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, rel := range []string{"pg_catalog.pg_aggregate", "pg_aggregate"} {
			pgAggregateBoth(t, p, b, b.DB, pgAggregateRawStep(b, "SELECT * FROM "+rel+" WHERE FALSE", [][]any{}))
			pgAggregateBoth(t, p, b, b.DB, pgAggregateRawStep(b, "SELECT * FROM "+rel+" WHERE aggfnoid::oid=2803", pgAggregateCountRaw()))
		}
	})
}
func TestReferencePGAggregateMetadata(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		stmt, err := b.DB.PrepareContext(p.Ctx, pgAggregateProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAggregateProbe}
		for _, v := range []any{"2803", "0", nil, "2803"} {
			want := [][]any{}
			if v == "2803" {
				want = pgAggregateCountRaw()
			}
			s := pgAggregateRawStep(b, pgAggregateProbe, want, v)
			pgAggregateBoth(t, p, b, b.DB, s)
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		}
		pgAggregateBoth(t, p, b, b.DB, pgAggregateRawStep(b, "SELECT * FROM pg_aggregate WHERE aggfnoid::oid='pg_catalog.count()'::regprocedure::oid", pgAggregateCountRaw()))
		rows, err := b.DB.QueryContext(p.Ctx, pgAggregateProbe, "2803")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		pgAggregateMetadata(t, rows, b.PG)
		saved, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("populated metadata cursor", rows.Err())
		}
		raw, ptrs := make([]any, 22), make([]any, 22)
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range raw {
			fixture.AssertRawValue(t, b.PG, pgAggregateTypes[i], v)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("metadata cursor EOF", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgAggregateBoth(t, p, b, b.DB, pgAggregateRawStep(b, pgAggregateProbe, [][]any{}, "0"))
		pgAggregateSavedMetadata(t, saved, b.PG)
		values := make([]any, 22)
		for i, v := range raw {
			values[i] = fixture.Normalize(v)
			if b.PG && pgAggregateTypes[i] == "OID" && v != nil {
				n, err := strconv.ParseUint(values[i].(string), 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				values[i] = int64(n)
			}
		}
		if !reflect.DeepEqual(values, pgAggregateCountRaw()[0]) {
			t.Fatal("saved raw cells changed after Close/fresh execution", values)
		}
	})
}
func TestReferencePGAggregateCatalogLinks(t *testing.T) {
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT oid,relname,relnamespace,relowner,relkind,relpersistence,relnatts,relhasindex FROM pg_class WHERE oid=2600", []string{"oid", "relname", "relnamespace", "relowner", "relkind", "relpersistence", "relnatts", "relhasindex"}, []string{"OID", "NAME", "OID", "OID", "CHAR", "CHAR", "INT2", "BOOL"}, [][]any{{int64(2600), "pg_aggregate", int64(11), int64(10), "r", "p", int64(22), true}}))
		want := make([][]any, 22)
		for i, n := range fixture.PGAggregateColumns {
			coll := int64(0)
			if i >= 20 {
				coll = 950
			}
			want[i] = []any{int64(i + 1), n, pgAggregateTypeIDs[i], int64(-1), i < 20, coll, int64(0)}
		}
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=2600 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, want))
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT reltype='pg_catalog.pg_aggregate'::regtype::oid AS linked FROM pg_class WHERE oid=2600", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT t.typrelid,t.typname,a.typname AS array_name,t.typnamespace,a.typnamespace AS array_namespace,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=2600", []string{"typrelid", "typname", "array_name", "typnamespace", "array_namespace", "element_link", "array_link"}, []string{"OID", "NAME", "NAME", "OID", "OID", "BOOL", "BOOL"}, [][]any{{int64(2600), "pg_aggregate", "_pg_aggregate", int64(11), int64(11), true, true}}))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT schemaname,tablename,tableowner,hasindexes FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_aggregate'", []string{"schemaname", "tablename", "tableowner", "hasindexes"}, []string{"NAME", "NAME", "NAME", "BOOL"}, [][]any{{"pg_catalog", "pg_aggregate", owner, true}}))
		pgAggregateBoth(t, p, b, b.DB, fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_aggregate'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
	})
}
