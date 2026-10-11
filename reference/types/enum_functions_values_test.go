package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumFunctionsValues(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for _, tc := range []struct {
			expr, want string
			nativeType string
		}{
			{"enum_first(NULL::mood)", "z", ""}, {"enum_first('a'::mood)", "z", ""},
			{"enum_last(NULL::mood)", "m", ""}, {"enum_last('a'::mood)", "m", ""},
			{"enum_range(NULL::mood)", "{z,a,m}", "_MOOD"}, {"enum_range('a'::mood)", "{z,a,m}", "_MOOD"},
			{"enum_range('z'::mood,'a'::mood)", "{z,a}", "_MOOD"},
			{"enum_range('a'::mood,'a'::mood)", "{a}", "_MOOD"},
			{"enum_range(NULL::mood,'a'::mood)", "{z,a}", "_MOOD"},
			{"enum_range('a'::mood,NULL::mood)", "{a,m}", "_MOOD"},
			{"enum_range(NULL::mood,NULL::mood)", "{z,a,m}", "_MOOD"},
			{"enum_range('m'::mood,'z'::mood)", "{}", "_MOOD"},
			{"enum_range(NULL::empty_enum)", "{}", "_EMPTY_ENUM"},
			{"enum_range(NULL::empty_enum,NULL::empty_enum)", "{}", "_EMPTY_ENUM"},
			{"enum_first(enum_last(NULL::mood))", "z", ""},
			{"pg_catalog.enum_last(NULL::mood)", "m", ""},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{enumFunctionsTypeName(b.PG, tc.nativeType)}, [][]any{{tc.want}})
					s.RawTypes = []string{"_ENUM"}
					s.Prepared = prepared
					fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`CREATE TYPE special_enum AS ENUM ('','NULL','x,y','{x}','a"b',E'a\\b',' a ')`, 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT enum_range(NULL::special_enum) AS v", []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_SPECIAL_ENUM")}, [][]any{{`{"","NULL","x,y","{x}","a\"b","a\\b"," a "}`}})
			s.RawTypes = []string{"_ENUM"}
			s.Prepared = prepared
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, s)
			s = fixture.Select("SELECT enum_range(lo,hi) AS v FROM enum_endpoints ORDER BY id", []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_MOOD")}, [][]any{{"{z,a}"}, {"{a,m}"}, {"{z,a}"}, {"{a,m}"}, {"{z,a,m}"}})
			s.RawTypes = []string{"_ENUM"}
			s.Prepared = prepared
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
}
