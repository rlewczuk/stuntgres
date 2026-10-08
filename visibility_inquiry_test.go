package stuntgres_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"stuntgres"
	"testing"
)

func visibilityInquiryRows(t *testing.T, rows *sql.Rows, err error, label string, want []any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{label}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" || types[0].ScanType() != reflect.TypeFor[bool]() {
		t.Fatal(types, err)
	}
	got := []any{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("independent visibility values: got %v want %v", got, want)
	}
}

func TestVisibilityInquiryFixedIdentities(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		fn   string
		oid  int64
		want any
	}{
		{"pg_table_is_visible", 1259, true},
		{"pg_type_is_visible", 23, true},
		{"pg_type_is_visible", 705, true},
		{"pg_type_is_visible", 2278, true},
		{"pg_collation_is_visible", 100, true},
		{"pg_collation_is_visible", 811, true},
		{"pg_collation_is_visible", 6411, true},
		{"pg_conversion_is_visible", 4402, true},
		{"pg_conversion_is_visible", 4518, true},
		{"pg_conversion_is_visible", 4531, true},
		{"pg_conversion_is_visible", 4450, nil},
		{"pg_conversion_is_visible", 4451, nil},
		{"pg_table_is_visible", 23, nil},
		{"pg_type_is_visible", 950, nil},
		{"pg_collation_is_visible", 4402, nil},
		{"pg_conversion_is_visible", 950, nil},
	} {
		t.Run(tc.fn+"/"+fmtVisibilityOID(tc.oid), func(t *testing.T) {
			rows, err := db.Query("SELECT pg_catalog."+tc.fn+"($1)", tc.oid)
			visibilityInquiryRows(t, rows, err, tc.fn, []any{tc.want})
		})
	}
	for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		stmt, err := db.Prepare("SELECT pg_catalog." + fn + "($1)")
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{nil, int64(0), int64(4294967295)} {
			rows, err := stmt.Query(arg)
			visibilityInquiryRows(t, rows, err, fn, []any{nil})
		}
		stmt.Close()
	}
}

func fmtVisibilityOID(oid int64) string {
	// Keep subtest labels independent of query construction.
	return strconv.FormatInt(oid, 10)
}

func TestVisibilityInquiryInventory(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, id := range []int64{16, 17, 18, 19, 1003, 20, 21, 23, 25, 26, 1042, 1043, 700, 701, 1082, 1083, 1114, 1184, 1186, 1266, 1700, 2950, 2205, 2278, 705} {
		rows, err := db.Query("SELECT pg_type_is_visible($1)", id)
		visibilityInquiryRows(t, rows, err, "pg_type_is_visible", []any{true})
	}
	for _, id := range []int64{100, 950, 951, 962, 963, 811, 6411} {
		rows, err := db.Query("SELECT pg_collation_is_visible($1)", id)
		visibilityInquiryRows(t, rows, err, "pg_collation_is_visible", []any{true})
	}
	for id := int64(4402); id <= 4531; id++ {
		var want any = true
		if id == 4450 || id == 4451 {
			want = nil
		}
		rows, err := db.Query("SELECT pg_conversion_is_visible($1)", id)
		visibilityInquiryRows(t, rows, err, "pg_conversion_is_visible", []any{want})
	}
}

func TestVisibilityInquiryShadowingAndFreshness(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.t(id INT)", "CREATE TABLE b.t(id INT)", "CREATE SEQUENCE a.shared", "CREATE VIEW b.shared AS SELECT id FROM b.t", "CREATE SEQUENCE public.temp_s", "CREATE TEMP SEQUENCE temp_s"} {
		execSQL(t, db, q)
	}
	var candidates map[int64]bool
	discover := func(path string) int64 {
		t.Helper()
		execSQL(t, db, "SET search_path="+path)
		rows, err := db.Query("SELECT oid,pg_table_is_visible(oid) FROM pg_catalog.pg_class WHERE relname='t'")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var found int64
		seen := make(map[int64]bool)
		for rows.Next() {
			var oid int64
			var visible bool
			if err := rows.Scan(&oid, &visible); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 || oid > 4294967295 {
				t.Fatal("invalid relation identity", oid)
			}
			if _, duplicate := seen[oid]; duplicate {
				t.Fatal("duplicate candidate identity", oid)
			}
			seen[oid] = visible
			if visible {
				if found != 0 {
					t.Fatal("multiple visible same-name relations")
				}
				found = oid
			}
		}
		if rows.Err() != nil || found == 0 {
			t.Fatal("missing visible relation", path, rows.Err())
		}
		if len(seen) != 2 {
			t.Fatal("expected exactly two relation candidates", seen)
		}
		if candidates != nil {
			for oid := range candidates {
				if _, exists := seen[oid]; !exists {
					t.Fatal("candidate identity bag changed", candidates, seen)
				}
			}
		}
		candidates = seen
		return found
	}
	a, b := discover("a,b"), discover("b,a")
	if a == b {
		t.Fatal("same-name relations share an identity", a)
	}
	stmt, err := db.Prepare("SELECT pg_table_is_visible($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		path string
		a, b bool
	}{{"a,b", true, false}, {"b,a", false, true}, {"public", false, false}} {
		execSQL(t, db, "SET search_path="+tc.path)
		for _, object := range []struct {
			oid  int64
			want bool
		}{{a, tc.a}, {b, tc.b}} {
			rows, err := stmt.Query(object.oid)
			visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{object.want})
		}
	}
	execSQL(t, db, "SET search_path=a,b")
	wantTuples(t, db, "SELECT pg_table_is_visible('a.shared'::regclass),pg_table_is_visible('b.shared'::regclass)", [][]any{{true, false}})
	execSQL(t, db, "SET search_path=b,a")
	wantTuples(t, db, "SELECT pg_table_is_visible('a.shared'::regclass),pg_table_is_visible('b.shared'::regclass)", [][]any{{false, true}})
	execSQL(t, db, "SET search_path=public")
	wantTuples(t, db, "SELECT pg_table_is_visible('public.temp_s'::regclass),pg_table_is_visible('pg_temp.temp_s'::regclass)", [][]any{{false, true}})
	execSQL(t, db, "SET search_path=public,pg_temp")
	wantTuples(t, db, "SELECT pg_table_is_visible('public.temp_s'::regclass),pg_table_is_visible('pg_temp.temp_s'::regclass)", [][]any{{true, false}})
	execSQL(t, db, "SET search_path=a,b")
	execSQL(t, db, "ALTER TABLE a.t RENAME TO renamed")
	rows, err := stmt.Query(b)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
	execSQL(t, db, "ALTER TABLE a.renamed SET SCHEMA public")
	rows, err = stmt.Query(a)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
	execSQL(t, db, "SET search_path=public")
	rows, err = stmt.Query(a)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
	execSQL(t, db, "DROP TABLE public.renamed")
	rows, err = stmt.Query(a)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{nil})
	execSQL(t, db, "CREATE TABLE a.t(id INT)")
	// Recreating intentionally changes the catalog identity set; start a fresh
	// discovery bag rather than comparing it with the pre-drop frontier.
	candidates = nil
	recreated := discover("a,b")
	if recreated == a || recreated == b {
		t.Fatal("recreated relation reused a live or dropped identity", recreated, a, b)
	}
	rows, err = stmt.Query(a)
	visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{nil})
}

func TestVisibilityInquiryCompositionAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE ids(id OID)", "INSERT INTO ids VALUES(23),(23),(0),(NULL)"} {
		execSQL(t, db, q)
	}
	execSQL(t, db, "CREATE VIEW visibility_filter AS SELECT id,pg_type_is_visible(id) AS visible FROM ids")
	wantTuples(t, db, "SELECT visible,count(*) FROM visibility_filter GROUP BY visible ORDER BY visible", [][]any{{true, int64(2)}, {nil, int64(2)}})
	wantTuples(t, db, "SELECT COALESCE(visible,FALSE) FROM visibility_filter ORDER BY id", [][]any{{false}, {true}, {true}, {false}})
	wantTuples(t, db, "SELECT id::bigint FROM visibility_filter WHERE visible", [][]any{{int64(23)}, {int64(23)}})
	wantTuples(t, db, "SELECT CASE WHEN id=23 THEN visible ELSE FALSE END FROM visibility_filter ORDER BY id", [][]any{{false}, {true}, {true}, {false}})
	wantTuples(t, db, "SELECT visible IS NULL FROM visibility_filter ORDER BY id", [][]any{{true}, {false}, {false}, {true}})
	execSQL(t, db, "CREATE TABLE checked(id OID CONSTRAINT known CHECK(pg_type_is_visible(id) IS NOT NULL))")
	checkExecError(t, db, false, "INSERT INTO checked VALUES(23),(0)", checkViolation("checked", "known", "0"))
	checkExecError(t, db, true, "INSERT INTO checked VALUES(23),(0)", checkViolation("checked", "known", "0"))
	wantTuples(t, db, "SELECT count(*) FROM checked", [][]any{{int64(0)}})
	execSQL(t, db, "INSERT INTO checked VALUES(23)")
	wantTuples(t, db, "SELECT pg_type_is_visible(id) FROM checked", [][]any{{true}})
	wantTuples(t, db, "UPDATE checked SET id=26 RETURNING pg_type_is_visible(id)", [][]any{{true}})
	wantTuples(t, db, "DELETE FROM checked RETURNING pg_type_is_visible(id)", [][]any{{true}})
	rows, err := db.Query("SELECT pg_type_is_visible(id) FROM checked")
	visibilityInquiryRows(t, rows, err, "pg_type_is_visible", []any{})
}

func TestVisibilityInquiryDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		for _, tc := range []struct{ suffix, signature string }{{"()", "()"}, {"(1,2)", "(integer, integer)"}, {"(TRUE)", "(boolean)"}, {"('1'::text)", "(text)"}} {
			nullCountError(t, db, "SELECT "+fn+tc.suffix, stuntgres.Error{Code: "42883", Message: "function " + fn + tc.signature + " does not exist", Position: "8", Hint: hint})
		}
		nullCountError(t, db, "SELECT public."+fn+"(1)", stuntgres.Error{Code: "42883", Message: "function public." + fn + "(integer) does not exist", Position: "8", Hint: hint})
		nullCountError(t, db, "SELECT missing_schema."+fn+"(1)", stuntgres.Error{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"})
		nullCountError(t, db, "SELECT /*é*/ "+fn+"(TRUE)", stuntgres.Error{Code: "42883", Message: "function " + fn + "(boolean) does not exist", Position: "14", Hint: hint})
		nullCountError(t, db, "SELECT "+fn+"('bad')", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))})
		nullCountError(t, db, "SELECT "+fn+"('4294967296')", stuntgres.Error{Code: "22003", Message: `value "4294967296" is out of range for type oid`, Position: strconv.Itoa(9 + len(fn))})
		rows, err := db.Query("SELECT " + fn + "(VARIADIC NULL)")
		visibilityInquiryRows(t, rows, err, fn, []any{nil})
	}
}

func TestVisibilityInquiryCatalogPlacement(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SCHEMA a")
	execSQL(t, db, "CREATE TABLE a.pg_class(id INT)")
	for _, tc := range []struct {
		path          string
		catalog, user bool
	}{{"a", true, false}, {"a,pg_catalog", false, true}, {"pg_catalog,a", true, false}, {"''", true, false}, {"missing,a", true, false}} {
		execSQL(t, db, "SET search_path="+tc.path)
		wantTuples(t, db, "SELECT pg_table_is_visible('pg_catalog.pg_class'::regclass),pg_table_is_visible('a.pg_class'::regclass)", [][]any{{tc.catalog, tc.user}})
		wantTuples(t, db, "SELECT pg_type_is_visible(23),pg_collation_is_visible(811),pg_conversion_is_visible(4402)", [][]any{{true, true, true}})
	}
}
