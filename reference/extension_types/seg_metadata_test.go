package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func segMetadata(t *testing.T, pg bool, cols []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(cols) != len(names) {
		t.Fatalf("column count %d", len(cols))
	}
	for i, c := range cols {
		if c.Name() != names[i] {
			t.Fatalf("name %q want %q", c.Name(), names[i])
		}
		if names[i] == "id" {
			continue
		}
		name, scan := "SEG", reflect.TypeFor[string]()
		if pg {
			name, scan = "", reflect.TypeFor[any]()
		}
		if c.DatabaseTypeName() != name || c.ScanType() != scan {
			t.Fatalf("type %q/%v want %q/%v", c.DatabaseTypeName(), c.ScanType(), name, scan)
		}
		n, k := c.Nullable()
		if pg || nullable == nil {
			if n || k {
				t.Fatalf("unknown nullable %v/%v", n, k)
			}
		} else if !k || n != nullable[i] {
			t.Fatalf("nullable %v/%v", n, k)
		}
		if n, k := c.Length(); n != 0 || k {
			t.Fatalf("length %d/%v", n, k)
		}
		if p, s, k := c.DecimalSize(); p != 0 || s != 0 || k {
			t.Fatalf("decimal %d/%d/%v", p, s, k)
		}
	}
}

func TestReferenceSegMetadata(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE seg_metadata(id integer NOT NULL,v seg,n seg NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE seg_vacant(v seg NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO seg_metadata VALUES (1,'1.00','0...'),(2,NULL,'~2.50')", 2))
		segCatalog(t, p, b, c)
		for _, tc := range []struct {
			q        string
			names    []string
			nullable []bool
			values   [][]any
		}{
			{"SELECT v,n FROM seg_metadata ORDER BY id", []string{"v", "n"}, []bool{true, false}, [][]any{{"1.00", "0 .."}, {nil, "~2.50"}}},
			{"SELECT v,n FROM seg_metadata WHERE id=99", []string{"v", "n"}, []bool{true, false}, [][]any{}},
			{"SELECT v AS alias FROM seg_metadata WHERE id=2", []string{"alias"}, []bool{true}, [][]any{{nil}}},
			{"SELECT v FROM seg_vacant", []string{"v"}, []bool{false}, [][]any{}},
			{"SELECT NULL::seg AS v", []string{"v"}, nil, [][]any{{nil}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := segSelect(b, tc.q, tc.names, tc.values)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					cols, e := r.ColumnTypes()
					if e != nil {
						t.Fatal(e)
					}
					segMetadata(t, b.PG, cols, tc.names, tc.nullable)
				}
				run(s)
			}
		}
		q := "SELECT v FROM seg_metadata WHERE id=$1"
		stmt, e := c.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			id   int
			rows [][]any
		}{{1, [][]any{{"1.00"}}}, {2, [][]any{{nil}}}, {99, [][]any{}}} {
			s := segSelect(b, q, []string{"v"}, tc.rows, tc.id)
			var cols []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				var e error
				cols, e = r.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				segMetadata(t, b.PG, cols, s.Columns, []bool{true})
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			segMetadata(t, b.PG, cols, s.Columns, []bool{true})
		}
		var oid, array, length int64
		var kind, category, align, storage string
		var byval, preferred bool
		e = c.QueryRowContext(p.Ctx, "SELECT oid,typarray,typlen,typtype,typcategory,typalign,typstorage,typbyval,typispreferred FROM pg_type WHERE oid=('seg'::regtype)::oid").Scan(&oid, &array, &length, &kind, &category, &align, &storage, &byval, &preferred)
		if e != nil {
			t.Fatal(e)
		}
		if oid <= 0 || array <= 0 || oid == array || length != 12 || kind != "b" || category != "U" || align != "i" || storage != "p" || byval || preferred {
			t.Fatalf("catalog %d/%d/%d %s/%s/%s/%s %v/%v", oid, array, length, kind, category, align, storage, byval, preferred)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT a.typname,a.typelem=t.oid AS element_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=('seg'::regtype)::oid", []string{"typname", "element_identity"}, []string{"NAME", "BOOL"}, [][]any{{"_seg", true}}),
			fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid=('seg[]'::regtype)::oid AND refclassid=1247 AND refobjid=('seg'::regtype)::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}),
			fixture.Select("SELECT p.proname,p.prorettype=('seg'::regtype)::oid AS returns_seg,p.proisstrict,p.provolatile,p.proparallel FROM pg_proc p WHERE p.oid IN ((SELECT typinput::oid FROM pg_type WHERE oid=('seg'::regtype)::oid),(SELECT typoutput::oid FROM pg_type WHERE oid=('seg'::regtype)::oid)) ORDER BY p.proname", []string{"proname", "returns_seg", "proisstrict", "provolatile", "proparallel"}, []string{"NAME", "BOOL", "BOOL", "CHAR", "CHAR"}, [][]any{{"seg_in", true, true, "i", "s"}, {"seg_out", false, true, "i", "s"}}),
		} {
			run(s)
		}
	})
}

// Catalog identities are checked relationally after each destination remints
// its installation; no expected OID is copied from another allocator.
func segCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT typtype,typlen,typbyval,typalign,typstorage,typcategory,typispreferred,typcollation::text AS collation,typtypmod FROM pg_type WHERE oid=('seg'::regtype)::oid", []string{"typtype", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typispreferred", "collation", "typtypmod"}, []string{"CHAR", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "TEXT", "INT4"}, [][]any{{"b", int64(12), false, "i", "p", "U", false, "0", int64(-1)}}),
		fixture.Select("SELECT a.typname,a.typelem=t.oid AS same_element FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=('seg'::regtype)::oid", []string{"typname", "same_element"}, []string{"NAME", "BOOL"}, [][]any{{"_seg", true}}),
		fixture.Select("SELECT p.proname,p.pronargs,p.prorettype=2275 AS returns_cstring,l.lanname FROM pg_type t JOIN pg_proc p ON p.oid=t.typinput OR p.oid=t.typoutput JOIN pg_language l ON l.oid=p.prolang WHERE t.oid=('seg'::regtype)::oid ORDER BY p.proname", []string{"proname", "pronargs", "returns_cstring", "lanname"}, []string{"NAME", "INT2", "BOOL", "NAME"}, [][]any{{"seg_in", int64(1), false, "c"}, {"seg_out", int64(1), true, "c"}}),
		fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='seg'", []string{"extversion", "extrelocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"1.4", true}}),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
