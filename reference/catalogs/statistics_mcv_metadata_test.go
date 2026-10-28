package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsMCVMetadata(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		s.run(fixture.Select("SELECT t.oid::bigint,t.typsend::oid::bigint,t.typlen,t.typbyval FROM pg_type t WHERE t.typname='pg_mcv_list'", []string{"oid", "typsend", "typlen", "typbyval"}, []string{"INT8", "INT8", "INT2", "BOOL"}, [][]any{{int64(5017), int64(5021), int64(-1), false}}))
		for _, q := range []string{"SELECT stxdmcv FROM pg_statistic_ext_data", "SELECT stxdmcv FROM pg_statistic_ext_data WHERE FALSE"} {
			rows, err := s.conn.QueryContext(s.pair.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if len(meta) != 1 || meta[0].DatabaseTypeName() != statisticsDatabaseTypeName(s.pg, "pg_mcv_list") {
				rows.Close()
				t.Fatal(meta)
			}
			want := reflect.TypeOf("")
			if s.pg {
				want = reflect.TypeOf((*any)(nil)).Elem()
			}
			if meta[0].ScanType() != want {
				rows.Close()
				t.Fatal("MCV ScanType", meta[0].ScanType(), want)
			}
			for rows.Next() {
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if s.pg {
					if _, ok := raw.([]byte); !ok {
						t.Fatalf("pq MCV %T", raw)
					}
				} else if _, ok := raw.(string); !ok {
					t.Fatalf("native MCV %T", raw)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		_, v := readMCV(t, s, "mcv_s", false)
		if len(v.dimensions) != 2 || v.dimensions[0].oid != 23 || v.dimensions[1].oid != 23 || !v.dimensions[0].byval || v.dimensions[0].length != 4 {
			t.Fatal(v.dimensions)
		}
		s.run(fixture.Select("SELECT pg_mcv_list_send(NULL::pg_mcv_list) AS bytes,pg_mcv_list_out(NULL::pg_mcv_list) AS text", []string{"bytes", "text"}, []string{"BYTEA", "CSTRING"}, [][]any{{nil, nil}}))
		input := statisticsError("SELECT 'x'::pg_mcv_list", "0A000", "cannot accept a value of type pg_mcv_list")
		input.Diagnostic.Position = fixture.Position(8)
		if s.prepared {
			input.ErrorStage = "prepare"
		}
		s.run(input)
	})
}
func TestReferenceStatisticsMCVDiagnostics(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		before, _ := readMCV(t, s, "mcv_s", false)
		id := statisticExtDataID(t, s.conn, "mcv_s", "mcv_t")
		var notices []fixture.Diagnostic
		_, noticeConn, rec := fixture.NoticeReferencePool(t, s.pair, s.pg, &notices)
		for _, prepared := range []bool{false, true} {
			notices = nil
			step := fixture.Exec("ANALYZE mcv_t(a)", 0)
			step.Prepared = prepared
			fixture.RunStep(t, s.pair.Ctx, noticeConn, rec, step)
			want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.mcv_s" could not be computed for relation "public.mcv_t"`, Schema: "public", Table: "mcv_t"}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
				t.Fatal(notices)
			}
		}
		for _, c := range []struct{ query, code, message string }{
			{"ANALYZE mcv_t(missing)", "42703", `column "missing" of relation "mcv_t" does not exist`},
			{"ANALYZE mcv_t(a,a)", "42701", `column "a" of relation "mcv_t" appears more than once`},
			{"CREATE STATISTICS bad(bogus) ON a,b FROM mcv_t", "42601", `unrecognized statistics kind "bogus"`},
			{"CREATE STATISTICS bad(mcv) ON a,a FROM mcv_t", "42701", "duplicate column name in statistics definition"},
			{"CREATE STATISTICS bad(mcv) ON a FROM mcv_t", "42P17", "extended statistics require at least 2 columns"},
			{"SELECT 'x'::pg_mcv_list", "0A000", "cannot accept a value of type pg_mcv_list"},
		} {
			step := statisticsError(c.query, c.code, c.message)
			if c.query == "SELECT 'x'::pg_mcv_list" {
				step.Diagnostic.Position = fixture.Position(8)
				if s.prepared {
					step.ErrorStage = "prepare"
				}
			}
			s.run(step)
			after, _ := readMCV(t, s, "mcv_s", false)
			mcvSame(t, before, after)
			if statisticExtDataID(t, s.conn, "mcv_s", "mcv_t") != id {
				t.Fatal("failed utility replaced identity")
			}
		}
		s.run(fixture.Select("SELECT a,b FROM mcv_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}, {nil, nil}}))
	})
}

func mcvReadOn(t *testing.T, s ndistinctScenario, conn *sql.Conn, name string, want ...mcvExpected) {
	t.Helper()
	s.conn = conn
	mcvAssert(t, s, name, want...)
}
