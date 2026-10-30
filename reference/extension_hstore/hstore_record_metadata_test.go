package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreRecordRaw(b fixture.Backend, q string, values [][]any, stored bool, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"v"}, []string{""}, values, args...)
	s.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, stored)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if v != nil {
			if b.PG {
				if _, ok := v.([]byte); !ok {
					t.Fatalf("composite raw %T want []byte", v)
				}
			} else if _, ok := v.(string); !ok {
				t.Fatalf("composite raw %T want string", v)
			}
		}
		return true
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		want := values[index][0]
		if v.Valid != (want != nil) || (want != nil && v.String != want) {
			t.Fatalf("composite scan %#v want %#v", v, want)
		}
	}
	return s
}

func TestReferenceHstoreRecordMetadata(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, tc := range []struct {
			expression string
			value      any
		}{
			{"hstore('(7,keep)'::hs_record_pair)", `"i"=>"7", "n"=>"keep"`},
			{"hstore(NULL::hs_record_pair)", `"i"=>NULL, "n"=>NULL`},
		} {
			for _, empty := range []bool{false, true} {
				q := "SELECT " + tc.expression + " AS v"
				values := [][]any{{tc.value}}
				if empty {
					q += " WHERE false"
					values = [][]any{}
				}
				hstoreRecordBoth(t, p, b, c, hstoreSelect(b, q, []string{"v"}, values, nil))
			}
		}
		for _, operator := range []bool{false, true} {
			for _, tc := range []struct {
				base, source string
				value        any
			}{
				{"'(7,keep)'::hs_record_pair", "'i=>42'::hstore", "(42,keep)"},
				{"NULL::hs_record_pair", "NULL::hstore", nil},
				{"NULL::hs_record_pair", "''::hstore", "(,)"},
			} {
				for _, empty := range []bool{false, true} {
					q := "SELECT " + hstoreRecordCall(operator, tc.base, tc.source) + " AS v"
					values := [][]any{{tc.value}}
					if empty {
						q += " WHERE false"
						values = [][]any{}
					}
					hstoreRecordBoth(t, p, b, c, hstoreRecordRaw(b, q, values, false))
				}
			}
			if operator {
				for _, empty := range []bool{false, true} {
					q := "SELECT (v).* FROM (SELECT " + hstoreRecordCall(true, "'(7,keep)'::hs_record_pair", "'i=>42'::hstore") + " AS v) s"
					values := [][]any{{int64(42), "keep"}}
					if empty {
						q += " WHERE false"
						values = [][]any{}
					}
					hstoreRecordBoth(t, p, b, c, fixture.Select(q, []string{"i", "n"}, []string{"INT4", "TEXT"}, values))
				}
			}
		}
		// Exact catalog type IDs prove named-result identity without depending
		// on the separately owned pg_typeof inquiry provider.
		for _, prepared := range []bool{false, true} {
			create := fixture.Exec("CREATE VIEW hs_record_result_identity AS SELECT populate_record(NULL::hs_record_pair,''::hstore) AS named,(NULL::hs_record_pair #= ''::hstore) AS operated", 0)
			if prepared {
				create = fixture.Prepared(create)
			}
			hstoreRun(t, p, b, c, create)
			hstoreRecordBoth(t, p, b, c, fixture.Select("SELECT attname,atttypid='hs_record_pair'::regtype::oid AS exact_type FROM pg_attribute WHERE attrelid='hs_record_result_identity'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "exact_type"}, []string{"NAME", "BOOL"}, [][]any{{"named", true}, {"operated", true}}))
			for _, column := range []string{"named", "operated"} {
				hstoreRecordBoth(t, p, b, c, hstoreRecordRaw(b, "SELECT "+column+" AS v FROM hs_record_result_identity", [][]any{{"(,)"}}, true))
			}
			hstoreRun(t, p, b, c, fixture.Exec("DROP VIEW hs_record_result_identity", 0))
		}
	})
}
