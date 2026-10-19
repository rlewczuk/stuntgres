package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesMetadata(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE fts_meta(id integer,v tsvector NOT NULL,q tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_meta VALUES(1,'a','b'),(2,'',NULL)", 2))
		for _, tc := range []struct {
			query    string
			rows     [][]any
			args     []any
			declared bool
		}{
			{"SELECT v,q FROM fts_meta ORDER BY id", [][]any{{"'a'", "'b'"}, {"", nil}}, nil, true},
			{"SELECT TSVECTOR 'a' AS v,pg_catalog.tsquery 'b' AS q", [][]any{{"'a'", "'b'"}}, nil, false},
			{"SELECT ''::tsvector AS v,''::tsquery AS q", [][]any{{"", ""}}, nil, false},
			{"SELECT NULL::tsvector AS v,NULL::tsquery AS q", [][]any{{nil, nil}}, nil, false},
			{"SELECT $1::tsvector AS v,$2::tsquery AS q", [][]any{{"'a'", "'b'"}}, []any{[]byte("a"), "b"}, false},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tc.query, []string{"v", "q"}, []string{"TSVECTOR", "TSQUERY"}, tc.rows, tc.args...)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, c := range cols {
						scan := reflect.TypeFor[string]()
						if b.PG {
							scan = reflect.TypeFor[any]()
						}
						if c.ScanType() != scan {
							t.Fatalf("column %d scan=%v want=%v", i, c.ScanType(), scan)
						}
						if n, ok := c.Length(); n != 0 || ok {
							t.Fatalf("length=%d/%t", n, ok)
						}
						if precision, scale, ok := c.DecimalSize(); precision != 0 || scale != 0 || ok {
							t.Fatalf("precision=%d,%d/%t", precision, scale, ok)
						}
						n, ok := c.Nullable()
						wantKnown := tc.declared && !b.PG
						wantNullable := wantKnown && i == 1
						if n != wantNullable || ok != wantKnown {
							t.Fatalf("column %d nullable=%t/%t want=%t/%t", i, n, ok, wantNullable, wantKnown)
						}
					}
				}
				run(s)
			}
		}
		q := "SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typinput::oid::bigint,typoutput::oid::bigint,typreceive::oid::bigint,typsend::oid::bigint,typanalyze::oid::bigint FROM pg_type WHERE oid IN (3614,3615) ORDER BY oid"
		run(fixture.Select(q, []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typinput", "typoutput", "typreceive", "typsend", "typanalyze"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{
			{int64(3614), "tsvector", int64(-1), false, "i", "x", "b", "U", false, int64(0), int64(0), int64(3643), int64(3610), int64(3611), int64(3639), int64(3638), int64(3688)},
			{int64(3615), "tsquery", int64(-1), false, "i", "p", "b", "U", false, int64(0), int64(0), int64(3645), int64(3612), int64(3613), int64(3641), int64(3640), int64(0)},
		}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='fts_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{
			{"id", int64(23), int64(-1), int64(0), "p"}, {"v", int64(3614), int64(-1), int64(0), "x"}, {"q", int64(3615), int64(-1), int64(0), "p"},
		}))
		for _, tc := range []struct {
			args []any
			rows [][]any
		}{
			{[]any{nil, nil}, [][]any{{nil, nil}}},
			{[]any{[]byte(nil), []byte(nil)}, [][]any{{nil, nil}}},
			{[]any{[]byte{}, []byte{}}, [][]any{{"", ""}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT $1::tsvector AS v,$2::tsquery AS q", []string{"v", "q"}, []string{"TSVECTOR", "TSQUERY"}, tc.rows, tc.args...)
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
