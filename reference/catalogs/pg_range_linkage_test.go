package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func pgRangeLinkage(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var classes [][]any
	for _, r := range []struct {
		id, subtype, class, family int64
		name                       string
	}{
		{3904, 23, 1978, 1976, "int4_ops"}, {3906, 1700, 3125, 1988, "numeric_ops"},
		{3908, 1114, 3128, 434, "timestamp_ops"}, {3910, 1184, 3127, 434, "timestamptz_ops"},
		{3912, 1082, 3122, 434, "date_ops"}, {3926, 20, 3124, 1976, "int8_ops"},
	} {
		classes = append(classes, []any{r.id, r.subtype, r.class, r.name, int64(403), int64(11), int64(10), r.family, r.subtype, true, int64(0)})
	}
	s := fixture.Select("SELECT r.rngtypid,r.rngsubtype,o.oid,o.opcname,o.opcmethod,o.opcnamespace,o.opcowner,o.opcfamily,o.opcintype,o.opcdefault,o.opckeytype FROM pg_range r JOIN pg_opclass o ON r.rngsubopc=o.oid ORDER BY r.rngtypid", []string{"rngtypid", "rngsubtype", "oid", "opcname", "opcmethod", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"OID", "OID", "OID", "NAME", "OID", "OID", "OID", "OID", "OID", "BOOL", "OID"}, classes)
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	names := [][]any{
		{"int4range", "integer", "int4multirange"}, {"numrange", "numeric", "nummultirange"},
		{"tsrange", "timestamp without time zone", "tsmultirange"}, {"tstzrange", "timestamp with time zone", "tstzmultirange"},
		{"daterange", "date", "datemultirange"}, {"int8range", "bigint", "int8multirange"},
	}
	s = fixture.Select("SELECT rngtypid::regtype::text AS range_type,rngsubtype::regtype::text AS subtype,rngmultitypid::regtype::text AS multirange_type FROM pg_range ORDER BY rngtypid", []string{"range_type", "subtype", "multirange_type"}, []string{"TEXT", "TEXT", "TEXT"}, names)
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	for _, f := range []struct {
		signature string
		oid       int64
	}{
		{"int4range_canonical(int4range)", 3914}, {"int8range_canonical(int8range)", 3928}, {"daterange_canonical(daterange)", 3915},
		{"int4range_subdiff(integer,integer)", 3922}, {"int8range_subdiff(bigint,bigint)", 3923}, {"numrange_subdiff(numeric,numeric)", 3924},
		{"daterange_subdiff(date,date)", 3925}, {"tsrange_subdiff(timestamp without time zone,timestamp without time zone)", 3929},
		{"tstzrange_subdiff(timestamp with time zone,timestamp with time zone)", 3930},
	} {
		s = fixture.Select("SELECT '"+f.signature+"'::regprocedure::oid AS routine FROM pg_range WHERE rngtypid=3904", []string{"routine"}, []string{"OID"}, [][]any{{f.oid}})
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	}
	var procs [][]any
	for _, r := range pgRangeNumericFacts() {
		canonical, diff := any(nil), any(nil)
		if b.PG {
			if r[5] != int64(0) {
				canonical = r[5]
			}
			diff = r[6]
		}
		procs = append(procs, []any{r[0], canonical, diff})
	}
	s = fixture.Select("SELECT r.rngtypid,c.oid AS canonical,d.oid AS subdiff FROM pg_range r LEFT JOIN pg_proc c ON c.oid=CAST(r.rngcanonical AS oid) LEFT JOIN pg_proc d ON d.oid=CAST(r.rngsubdiff AS oid) ORDER BY r.rngtypid", []string{"rngtypid", "canonical", "subdiff"}, []string{"OID", "OID", "OID"}, procs)
	fixture.PgDescriptionRows(t, p, b, s)
	if b.PG {
		// Supplemental upstream provider proof; native deliberately exposes only
		// the existing REGPROC/REGPROCEDURE identities for these routines.
		var signatures [][]any
		args := []string{"23 23", "1700 1700", "1114 1114", "1184 1184", "1082 1082", "20 20"}
		for i, r := range pgRangeNumericFacts() {
			signatures = append(signatures, []any{r[0], int64(701), args[i]})
		}
		s = fixture.Select("SELECT r.rngtypid,p.prorettype,p.proargtypes::text AS args FROM pg_range r JOIN pg_proc p ON p.oid=CAST(r.rngsubdiff AS oid) ORDER BY r.rngtypid", []string{"rngtypid", "prorettype", "args"}, []string{"OID", "OID", "TEXT"}, signatures)
		fixture.PgDescriptionRows(t, p, b, s)
		s = fixture.Select("SELECT r.rngtypid,p.prorettype,p.proargtypes::text AS args FROM pg_range r JOIN pg_proc p ON p.oid=CAST(r.rngcanonical AS oid) ORDER BY r.rngtypid", []string{"rngtypid", "prorettype", "args"}, []string{"OID", "OID", "TEXT"}, [][]any{{int64(3904), int64(3904), "3904"}, {int64(3912), int64(3912), "3912"}, {int64(3926), int64(3926), "3926"}})
		fixture.PgDescriptionRows(t, p, b, s)
	}
}
func pgRangeSemantics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
	run(fixture.Exec("SET DateStyle TO 'ISO, MDY'", 0))
	run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
	run(fixture.Exec("CREATE TABLE range_catalog_values(k integer,a int4range,b int8range,n numrange,d daterange,t tsrange,z tstzrange)", 0))
	run(fixture.Exec("INSERT INTO range_catalog_values VALUES(1,'[1,4]','[1,4]','[1,4]','[2000-02-28,2000-03-01]','[2000-01-01 00:00:00,2000-01-02 00:00:00]','[2000-01-01 00:00:00+00,2000-01-02 00:00:00+00]')", 1))
	run(fixture.Exec("INSERT INTO range_catalog_values VALUES(2,NULL,NULL,NULL,NULL,NULL,NULL)", 1))
	q := "SELECT k,a,b,n,d,t,z FROM range_catalog_values ORDER BY k"
	s := fixture.Select(q, []string{"k", "a", "b", "n", "d", "t", "z"}, []string{"INT4", "INT4RANGE", "INT8RANGE", "NUMRANGE", "DATERANGE", "TSRANGE", "TSTZRANGE"}, [][]any{
		{int64(1), "[1,5)", "[1,5)", "[1,4]", "[2000-02-28,2000-03-02)", `["2000-01-01 00:00:00","2000-01-02 00:00:00"]`, `["2000-01-01 00:00:00+00","2000-01-02 00:00:00+00"]`},
		{int64(2), nil, nil, nil, nil, nil, nil},
	})
	// Follow reference/types rangeSelect's public transport contract without
	// expanding the shared fixture's generic raw-type inventory.
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if column == 0 {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("range column %d row %d raw %T, want %v", column, row, value, want)
			}
			if data, ok := value.([]byte); ok && data == nil {
				t.Fatal("typed nil bytes are not SQL NULL")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range metadata {
			if i == 0 {
				continue
			}
			want := reflect.TypeFor[string]()
			if b.PG {
				want = reflect.TypeFor[any]()
			}
			if typ.ScanType() != want {
				t.Fatalf("%s ScanType %v, want %v", s.Types[i], typ.ScanType(), want)
			}
			if length, known := typ.Length(); length != 0 || known {
				t.Fatalf("%s length %d/%v", s.Types[i], length, known)
			}
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("%s precision/scale %d/%d/%v", s.Types[i], precision, scale, known)
			}
			n, known := typ.Nullable()
			if b.PG {
				if n || known {
					t.Fatalf("unknown nullable %v/%v, want false/false", n, known)
				}
			} else if !n || !known {
				t.Fatalf("native nullable %v/%v, want true/true", n, known)
			}
		}
	}
	run(s)
	run(fixture.Prepared(s))
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
	run(fixture.Exec("CREATE DOMAIN range_catalog_domain AS int4range", 0))
	run(fixture.Exec("ALTER TABLE range_catalog_values ADD COLUMN extra int4range", 0))
	// Resolve the generated domain identity relationally; do not pin allocators.
	run(fixture.Exec("CREATE TABLE range_catalog_domain_holder(d range_catalog_domain)", 0))
	fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT r.rngtypid FROM pg_range r JOIN pg_attribute a ON a.atttypid=r.rngtypid JOIN pg_class c ON c.oid=a.attrelid WHERE c.relname='range_catalog_domain_holder'", []string{"rngtypid"}, []string{"OID"}, nil))
	pgRangeInventory(t, p, b)
	pgRangeRows(t, p, b, conn, "SELECT * FROM pg_range WHERE rngtypid IN (4451,4532,4533,4534,4535,4536,3905,3907,3909,3911,3913,3927)", nil, false)
	run(fixture.Exec("DROP TABLE range_catalog_domain_holder", 0))
	run(fixture.Exec("DROP DOMAIN range_catalog_domain", 0))
	run(fixture.Exec("DROP TABLE range_catalog_values", 0))
	pgRangeInventory(t, p, b)
}
func TestReferencePGRangeLinkage(t *testing.T) {
	pgRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgRangeLinkage(t, p, b)
		pgRangeSemantics(t, p, b)
	})
}
