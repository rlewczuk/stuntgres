package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsNDistinctSyntax(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE nd_t(a INT,b INT)", "CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t"} {
			s.run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ suffix, token string }{{"2147483648", "2147483648"}, {"-2147483649", "2147483649"}, {"-2147483648", "2147483648"}, {"+2147483648", "2147483648"}, {"1.5", "1.5"}, {"0 trailing", "trailing"}} {
			q := "ALTER STATISTICS nd_s SET STATISTICS " + tc.suffix
			want := fixture.ControlsReferenceDiagnostic("42601", fmt.Sprintf(`syntax error at or near "%s"`, tc.token))
			want.Position = fmt.Sprint(strings.Index(q, tc.token) + 1)
			s.run(fixture.ErrorDiagnostic(q, want))
			s.run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='nd_s'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{nil}}))
		}
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS +1", 0))
		s.run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='nd_s'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{int64(1)}}))
	})
}
