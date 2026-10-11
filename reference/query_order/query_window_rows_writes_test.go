package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowRowsWrites(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE rows_input(id INT)", 0),
			fixture.Exec("INSERT INTO rows_input VALUES(1),(2),(3)", 3),
			fixture.Exec("CREATE TABLE rows_written(id INT,n BIGINT)", 0),
			fixture.Exec("CREATE TABLE rows_small(n SMALLINT)", 0),
			fixture.Exec("INSERT INTO rows_small VALUES(99)", 1),
			fixture.Exec("CREATE TABLE rows_checked(n BIGINT CONSTRAINT rows_positive CHECK(n>0))", 0),
			fixture.Exec("INSERT INTO rows_checked VALUES(99)", 1),
		} {
			queryWindowRun(t, p, b, s)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO rows_written SELECT id,count(*) OVER(ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM rows_input", 3)
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
			for _, tc := range []struct {
				query, table, typ string
				diagnostic        fixture.Diagnostic
			}{
				{"INSERT INTO rows_small SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING)+32766 FROM rows_input", "rows_small", "INT2", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"}},
				{"INSERT INTO rows_checked SELECT count(*) OVER(ORDER BY id ROWS BETWEEN 1 FOLLOWING AND 1 FOLLOWING) FROM rows_input", "rows_checked", "INT8", fixture.Diagnostic{Code: "23514", Message: "new row for relation \"rows_checked\" violates check constraint \"rows_positive\"", Detail: "Failing row contains (0).", Schema: "public", Table: "rows_checked", Constraint: "rows_positive"}},
			} {
				s = fixture.ImplicitError(tc.query, tc.diagnostic)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
				queryWindowRoutes(t, p, b, fixture.Select("SELECT n FROM "+tc.table, []string{"n"}, []string{tc.typ}, [][]any{{int64(99)}}))
			}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,n FROM rows_written ORDER BY id,n", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(3), int64(2)}, {int64(3), int64(2)}}))
	})
}
