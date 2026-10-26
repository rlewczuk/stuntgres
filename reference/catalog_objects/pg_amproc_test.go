package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgAmprocColumns = []string{"oid", "amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}
var pgAmprocTypes = []string{"OID", "OID", "OID", "OID", "INT2", "REGPROC"}

const pgAmprocPredicate = "(amprocfamily=1976 AND ((amproclefttype=23 AND amprocrighttype=23) OR (amproclefttype=21 AND amprocrighttype=20)) AND amprocnum=1) OR (amprocfamily=1977 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum IN (1,2)) OR (amprocfamily=3253 AND amproclefttype=3220 AND amprocrighttype=3220 AND amprocnum=1) OR (amprocfamily=4054 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum IN (1,2,3,4))"
const pgAmprocWitnessQuery = "SELECT * FROM pg_catalog.pg_amproc WHERE " + pgAmprocPredicate + " ORDER BY amprocfamily,amproclefttype,amprocrighttype,amprocnum"
const pgAmprocProbe = "SELECT * FROM pg_catalog.pg_amproc WHERE oid=$1::oid"

// Independently specified semantic keys; generated member identities are learned
// only to test identity validity/stability within a backend, never for parity.
func pgAmprocSemanticFacts() [][]any {
	return [][]any{
		{int64(1976), int64(21), int64(20), int64(1), "btint28cmp"},
		{int64(1976), int64(23), int64(23), int64(1), "btint4cmp"},
		{int64(1977), int64(23), int64(23), int64(1), "hashint4"},
		{int64(1977), int64(23), int64(23), int64(2), "hashint4extended"},
		{int64(3253), int64(3220), int64(3220), int64(1), "pg_lsn_cmp"},
		{int64(4054), int64(23), int64(23), int64(1), "brin_minmax_opcinfo"},
		{int64(4054), int64(23), int64(23), int64(2), "brin_minmax_add_value"},
		{int64(4054), int64(23), int64(23), int64(3), "brin_minmax_consistent"},
		{int64(4054), int64(23), int64(23), int64(4), "brin_minmax_union"},
	}
}
func pgAmprocResultMetadata(t *testing.T, r *sql.Rows, pg, nullable bool) {
	t.Helper()
	cols, err := r.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgAmprocColumns) {
		t.Fatal(cols, err)
	}
	types, err := r.ColumnTypes()
	if err != nil || len(types) != 6 {
		t.Fatal(types, err)
	}
	for i, c := range types {
		scan := reflect.TypeFor[int64]()
		if i == 5 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 4 {
				scan = reflect.TypeFor[int16]()
			}
		}
		if c.Name() != cols[i] || c.DatabaseTypeName() != pgAmprocTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %q %q %v; expected %q %q %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), cols[i], pgAmprocTypes[i], scan)
		}
		if n, k := c.Nullable(); n != (!pg && nullable) || k == pg {
			t.Fatal("nullability", i, n, k)
		}
		if n, k := c.Length(); n != 0 || k {
			t.Fatal("length", i, n, k)
		}
		if a, z, k := c.DecimalSize(); a != 0 || z != 0 || k {
			t.Fatal("decimal", i, a, z, k)
		}
	}
}
func pgAmprocMetadata(t *testing.T, r *sql.Rows, pg bool) { pgAmprocResultMetadata(t, r, pg, false) }
func pgAmprocRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want [][]any, prepared bool, args ...any) {
	t.Helper()
	s := fixture.Select(q, pgAmprocColumns, pgAmprocTypes, want, args...)
	s.Prepared = prepared
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		want := reflect.TypeFor[int64]()
		if column == 5 {
			want = reflect.TypeFor[string]()
		}
		if b.PG && column != 4 {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != want {
			t.Fatalf("raw column %d row %d: %T; want %v", column, row, value, want)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgAmprocMetadata(t, r, b.PG) }
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
}
func pgAmprocConsume(t *testing.T, r *sql.Rows) [][]any {
	t.Helper()
	var got [][]any
	for r.Next() {
		var oid, f, l, z, n int64
		var proc string
		if err := r.Scan(&oid, &f, &l, &z, &n, &proc); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{oid, f, l, z, n, proc})
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}
func pgAmprocFacts(t *testing.T, p *fixture.Pair, b fixture.Backend) [][]any {
	t.Helper()
	r, err := b.DB.QueryContext(p.Ctx, pgAmprocWitnessQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := pgAmprocConsume(t, r)
	facts := pgAmprocSemanticFacts()
	if len(got) != len(facts) {
		t.Fatal("membership count", got)
	}
	seen := map[int64]bool{}
	var want [][]any
	for i, f := range facts {
		id := got[i][0].(int64)
		if id <= 0 || id > 4294967295 || seen[id] {
			t.Fatal("member identity", id)
		}
		seen[id] = true
		want = append(want, append([]any{id}, f...))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("independent semantic facts", got, want)
	}
	return want
}
func pgAmprocInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	facts := pgAmprocFacts(t, p, b)
	for _, prepared := range []bool{false, true} {
		pgAmprocRows(t, p, b, b.DB, pgAmprocWitnessQuery, facts, prepared)
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgAmprocProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAmprocProbe}
	for _, route := range []struct {
		e        fixture.Executor
		prepared bool
	}{{b.DB, false}, {b.DB, true}, {held, false}} {
		for _, row := range facts {
			pgAmprocRows(t, p, b, route.e, pgAmprocProbe, [][]any{row}, route.prepared, row[0])
		}
		for _, v := range []any{nil, int64(0)} {
			pgAmprocRows(t, p, b, route.e, pgAmprocProbe, nil, route.prepared, v)
		}
	}
	ids := []int64{2192, 351, 450, 425, 3251, 3383, 3384, 3385, 3386}
	var joins [][]any
	for i, f := range pgAmprocSemanticFacts() {
		method := int64(403)
		if i == 2 || i == 3 {
			method = 405
		}
		if i >= 5 {
			method = 3580
		}
		joins = append(joins, []any{f[0], f[1], f[2], f[3], ids[i], ids[i], method})
	}
	q := "SELECT a.amprocfamily,l.oid,r.oid,a.amprocnum,a.amproc::oid,p.oid,m.oid FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily JOIN pg_am m ON m.oid=f.opfmethod JOIN pg_type l ON l.oid=a.amproclefttype JOIN pg_type r ON r.oid=a.amprocrighttype JOIN pg_proc p ON p.oid=a.amproc::oid WHERE " + pgAmprocPredicate + " ORDER BY a.amprocfamily,a.amproclefttype,a.amprocrighttype,a.amprocnum"
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q, []string{"amprocfamily", "oid", "oid", "amprocnum", "amproc", "oid", "oid"}, []string{"OID", "OID", "OID", "INT2", "OID", "OID", "OID"}, joins)
		s.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, s)
	}
	for _, id := range []int64{3130, 3359, 4128, 5051, 6403} {
		want := [][]any(nil)
		if b.PG {
			want = [][]any{{id}}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT DISTINCT amproc::oid AS amproc FROM pg_amproc WHERE amproc::oid=$1::oid", []string{"amproc"}, []string{"OID"}, want, id))
	}
	for _, tc := range []struct{ family, typ, slot, proc int64 }{{1976, 23, 2, 3130}, {3253, 3220, 4, 5051}} {
		var want [][]any
		if b.PG {
			want = [][]any{{tc.proc}}
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT amproc::oid AS amproc FROM pg_amproc WHERE amprocfamily=$1::oid AND amproclefttype=$2::oid AND amprocrighttype=$2::oid AND amprocnum=$3::int2", []string{"amproc"}, []string{"OID"}, want, tc.family, tc.typ, tc.slot))
	}
	all, err := b.DB.QueryContext(p.Ctx, "SELECT oid,amprocfamily,amproclefttype,amprocrighttype,amprocnum FROM pg_amproc")
	if err != nil {
		t.Fatal(err)
	}
	seenOID := map[int64]bool{}
	seenKey := map[[4]int64]bool{}
	for all.Next() {
		var oid int64
		var key [4]int64
		if err := all.Scan(&oid, &key[0], &key[1], &key[2], &key[3]); err != nil {
			t.Fatal(err)
		}
		if oid <= 0 || oid > 4294967295 || seenOID[oid] || seenKey[key] {
			t.Fatal("duplicate/invalid membership", oid, key)
		}
		seenOID[oid] = true
		seenKey[key] = true
	}
	if err := all.Err(); err != nil {
		t.Fatal(err)
	}
	all.Close()
	fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT a.amproclefttype,a.amprocrighttype,p.proargtypes::text FROM pg_amproc a JOIN pg_proc p ON p.oid=a.amproc::oid WHERE a.amprocfamily=4054 AND a.amproclefttype=23 AND a.amprocrighttype=23 AND a.amprocnum IN (1,2,3,4) ORDER BY a.amprocnum", []string{"amproclefttype", "amprocrighttype", "proargtypes"}, []string{"OID", "OID", "TEXT"}, [][]any{{int64(23), int64(23), "2281"}, {int64(23), int64(23), "2281 2281 2281 2281"}, {int64(23), int64(23), "2281 2281 2281"}, {int64(23), int64(23), "2281 2281 2281"}}))
}
func TestReferencePGAmprocInventory(t *testing.T) { pgOpclassScenario(t, pgAmprocInventory) }
