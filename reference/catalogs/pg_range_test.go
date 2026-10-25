package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgRangeColumns = []string{"rngtypid", "rngsubtype", "rngmultitypid", "rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"}
var pgRangeTypes = []string{"OID", "OID", "OID", "OID", "OID", "REGPROC", "REGPROC"}

const pgRangeProbe = "SELECT * FROM pg_catalog.pg_range WHERE rngtypid=$1::oid"
const pgRangeNumericQuery = "SELECT rngtypid,rngsubtype,rngmultitypid,rngcollation,rngsubopc,rngcanonical::oid,rngsubdiff::oid FROM pg_catalog.pg_range ORDER BY rngtypid"

// Independently specified REL_18_0 pg_range.dat facts, never native metadata.
func pgRangeNumericFacts() [][]any {
	return [][]any{
		{int64(3904), int64(23), int64(4451), int64(0), int64(1978), int64(3914), int64(3922)},
		{int64(3906), int64(1700), int64(4532), int64(0), int64(3125), int64(0), int64(3924)},
		{int64(3908), int64(1114), int64(4533), int64(0), int64(3128), int64(0), int64(3929)},
		{int64(3910), int64(1184), int64(4534), int64(0), int64(3127), int64(0), int64(3930)},
		{int64(3912), int64(1082), int64(4535), int64(0), int64(3122), int64(3915), int64(3925)},
		{int64(3926), int64(20), int64(4536), int64(0), int64(3124), int64(3928), int64(3923)},
	}
}
func pgRangeFacts() [][]any {
	rows := pgRangeNumericFacts()
	canonical := []string{"int4range_canonical", "-", "-", "-", "daterange_canonical", "int8range_canonical"}
	diff := []string{"int4range_subdiff", "numrange_subdiff", "tsrange_subdiff", "tstzrange_subdiff", "daterange_subdiff", "int8range_subdiff"}
	for i := range rows {
		rows[i][5], rows[i][6] = canonical[i], diff[i]
	}
	return rows
}
func pgRangeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}
func pgRangeMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, pgRangeColumns) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgRangeSavedMetadata(t, types, pg)
}
func pgRangeSavedMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 7 {
		t.Fatal("range descriptor width", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[int64]()
		if i >= 5 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != pgRangeColumns[i] || c.DatabaseTypeName() != pgRangeTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %q %q %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if n, known := c.Nullable(); n || known == pg {
			t.Fatal("nullable", i, n, known)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("decimal", i, p, s, known)
		}
	}
}
func pgRangeStep(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, pgRangeColumns, pgRangeTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { pgRangeMetadata(t, r, b.PG) }
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		want := reflect.TypeFor[int64]()
		if col >= 5 {
			want = reflect.TypeFor[string]()
		}
		if b.PG {
			want = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(v) != want {
			t.Errorf("raw column=%d row=%d got=%T/%v want=%v", col, row, v, v, want)
		}
		return true
	}
	return s
}
func pgRangeRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want [][]any, prepared bool, args ...any) {
	t.Helper()
	s := pgRangeStep(b, q, want, args...)
	s.Prepared = prepared
	fixture.PgDescriptionExecutorRows(t, p, b, e, s)
}
func pgRangeLookup(value any) [][]any {
	for _, row := range pgRangeFacts() {
		if value == row[0] {
			return [][]any{row}
		}
	}
	return nil
}
func pgRangeReads(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	stmt, err := e.PrepareContext(p.Ctx, pgRangeProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: pgRangeProbe}
	for _, value := range []any{int64(3904), int64(3908), int64(0), nil, int64(3926), int64(3904)} {
		for _, prepared := range []bool{false, true} {
			pgRangeRows(t, p, b, e, pgRangeProbe, pgRangeLookup(value), prepared, value)
		}
		pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(value), false, value)
	}
}
func pgRangeInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		pgRangeRows(t, p, b, b.DB, "SELECT * FROM pg_catalog.pg_range ORDER BY rngtypid", pgRangeFacts(), prepared)
		pgRangeRows(t, p, b, b.DB, "SELECT * FROM pg_range ORDER BY rngtypid", pgRangeFacts(), prepared)
		s := fixture.Select(pgRangeNumericQuery, pgRangeColumns, []string{"OID", "OID", "OID", "OID", "OID", "OID", "OID"}, pgRangeNumericFacts())
		s.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, s)
	}
	s := pgRangeStep(b, "SELECT * FROM pg_catalog.pg_range", pgRangeFacts())
	s.Bag = true
	fixture.PgDescriptionRows(t, p, b, s)
}
func TestReferencePGRangeInventory(t *testing.T) {
	pgRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgRangeInventory(t, p, b)
		pgRangeReads(t, p, b, b.DB)
	})
}
func TestReferencePGRangeShape(t *testing.T) {
	pgRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			pgRangeRows(t, p, b, b.DB, "SELECT * FROM pg_range WHERE FALSE", nil, prepared)
			pgRangeRows(t, p, b, b.DB, pgRangeProbe, nil, prepared, int64(0))
			pgRangeRows(t, p, b, b.DB, pgRangeProbe, nil, prepared, nil)
		}
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_range ORDER BY rngtypid")
		if err != nil {
			t.Fatal(err)
		}
		pgRangeMetadata(t, rows, b.PG)
		saved, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pgRangeSavedMetadata(t, saved, b.PG)
		checks := []fixture.Step{
			fixture.Select("SELECT oid,reltype,relnatts,relkind,relhasindex FROM pg_class WHERE oid=3541", []string{"oid", "reltype", "relnatts", "relkind", "relhasindex"}, []string{"OID", "OID", "INT2", "CHAR", "BOOL"}, [][]any{{int64(3541), int64(10101), int64(7), "r", true}}),
			fixture.Select("SELECT 'pg_catalog.pg_range'::regtype::oid AS rowtype,'pg_catalog.pg_range[]'::regtype::oid AS arraytype FROM pg_range WHERE rngtypid=3904", []string{"rowtype", "arraytype"}, []string{"OID", "OID"}, [][]any{{int64(10101), int64(10100)}}),
			fixture.Select("SELECT * FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_range'", fixture.PGTableColumns, fixture.PGTableTypes, [][]any{fixture.PGTableRow("pg_catalog", "pg_range", fixture.PGTableViewsOwner(b), true)}),
		}
		var attrs [][]any
		for i, name := range pgRangeColumns {
			typ := int64(26)
			if i >= 5 {
				typ = 24
			}
			attrs = append(attrs, []any{int64(i + 1), name, typ, true, int64(-1), int64(0), int64(0)})
		}
		checks = append(checks, fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=3541 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attrs))
		checks = append(checks, fixture.Select("SELECT oid,typrelid,typarray,typelem FROM pg_type WHERE oid IN (10100,10101) ORDER BY oid", []string{"oid", "typrelid", "typarray", "typelem"}, []string{"OID", "OID", "OID", "OID"}, [][]any{{int64(10100), int64(0), int64(0), int64(10101)}, {int64(10101), int64(3541), int64(10100), int64(0)}}))
		for _, s := range checks {
			fixture.PgDescriptionRows(t, p, b, s)
			fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		}
	})
}
