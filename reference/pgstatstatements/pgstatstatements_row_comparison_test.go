package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsRowComparisonIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		const table = "pgss_part.pgss_row_cmp_identity"
		s.exec("CREATE TABLE " + table + "(k integer,v integer)")
		s.run(fixture.Exec("INSERT INTO "+table+" VALUES(1,10),(2,20),(3,30)", 3))
		const suffix = " AS compared,k,v FROM " + table + " ORDER BY k"
		const pattern = "SELECT (k,v) % FROM " + table + " ORDER BY k"
		s.counts(pattern, 0, 0, 0)

		// Released RowCompareExpr selects cmptype independently of its operand
		// literals. Every shape keeps identical projections, labels and sources;
		// only the actual comparison kind separates these four query identities.
		for index, tc := range []struct {
			operator         string
			direct, prepared [3]bool
		}{
			{"<", [3]bool{true, false, false}, [3]bool{true, true, false}},
			{"<=", [3]bool{true, true, false}, [3]bool{true, true, false}},
			{">", [3]bool{false, false, true}, [3]bool{false, false, true}},
			{">=", [3]bool{false, true, true}, [3]bool{false, false, true}},
		} {
			prefix := "SELECT (k,v) " + tc.operator + " "
			representative := prefix + "($1,$2)" + suffix
			rows := func(truth [3]bool) [][]any {
				return [][]any{
					{truth[0], int64(1), int64(10)},
					{truth[1], int64(2), int64(20)},
					{truth[2], int64(3), int64(30)},
				}
			}
			s.work(fixture.Select(prefix+"(2,20)"+suffix, []string{"compared", "k", "v"}, []string{"BOOL", "INT4", "INT4"}, rows(tc.direct)))
			s.entry(representative, 1, 3)
			s.work(fixture.Prepared(fixture.Select(prefix+"(3,29)"+suffix, []string{"compared", "k", "v"}, []string{"BOOL", "INT4", "INT4"}, rows(tc.prepared))))
			s.entry(representative, 2, 6)
			s.counts(pattern, int64(index+1), int64(2*(index+1)), int64(6*(index+1)))
		}

		// Scope matches the shared counter reader: inspect only this principal,
		// database and new table-dependent shape, not unrelated cluster history.
		identities := "SELECT count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		s.both(fixture.Select(identities, []string{"identities"}, []string{"INT8"}, [][]any{{int64(4)}}, pattern))
		s.both(fixture.Select("SELECT k,v FROM "+table+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}}))
	})
}
