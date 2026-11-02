package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCallsRows(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				// Distinct resolved objects and text isolate each stream from prior
				// cases in the package-owned cluster without SQL reset support.
				suffix := "direct"
				if prepared {
					suffix = "prepared"
				}
				source := "pgss_part.pgss_rows_source_" + suffix
				target := "pgss_part.pgss_rows_target_" + suffix
				returning := "pgss_part.pgss_rows_returning_" + suffix
				s.exec("CREATE TABLE " + source + "(k integer)")
				s.exec("CREATE TABLE " + target + "(k integer,v integer DEFAULT 0)")
				s.exec("CREATE TABLE " + returning + "(k integer,v integer DEFAULT 0)")
				s.run(fixture.Exec("INSERT INTO "+source+" VALUES(1),(2)", 2))
				s.run(fixture.Exec("INSERT INTO "+returning+"(k) VALUES(1),(2)", 2))
				work := func(step fixture.Step) {
					if prepared {
						step = fixture.Prepared(step)
					}
					s.work(step)
				}

				selectPrefix := "SELECT k AS pgss_rows_value FROM " + source
				s.counts(selectPrefix+"%", 0, 0, 0)
				work(fixture.Select(selectPrefix+" WHERE k<=2 ORDER BY k", []string{"pgss_rows_value"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				work(fixture.Select(selectPrefix+" WHERE k<=1 ORDER BY k", []string{"pgss_rows_value"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.counts(selectPrefix+"%", 1, 2, 3)
				s.entry(selectPrefix+" WHERE k<=$1 ORDER BY k", 2, 3)

				insertPrefix := "INSERT INTO " + target + "(k) SELECT k FROM " + source
				s.counts(insertPrefix+"%", 0, 0, 0)
				work(fixture.Exec(insertPrefix+" WHERE k<=1", 1))
				work(fixture.Exec(insertPrefix+" WHERE k<=2", 2))
				s.counts(insertPrefix+"%", 1, 2, 3)
				s.entry(insertPrefix+" WHERE k<=$1", 2, 3)

				updatePrefix := "UPDATE " + target + " SET v=v+"
				s.counts(updatePrefix+"%", 0, 0, 0)
				work(fixture.Exec(updatePrefix+"1 WHERE k=1", 2))
				work(fixture.Exec(updatePrefix+"1 WHERE k=2", 1))
				s.counts(updatePrefix+"%", 1, 2, 3)
				s.entry("UPDATE "+target+" SET v=v+$1 WHERE k=$2", 2, 3)

				returnPrefix := "UPDATE " + returning + " SET v=v+"
				s.counts(returnPrefix+"%", 0, 0, 0)
				step := fixture.Select(returnPrefix+"1 WHERE k<=2 RETURNING k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
				step.Bag = true
				work(step)
				work(fixture.Select(returnPrefix+"1 WHERE k<=1 RETURNING k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.counts(returnPrefix+"%", 1, 2, 3)
				s.entry("UPDATE "+returning+" SET v=v+$1 WHERE k<=$2 RETURNING k", 2, 3)

				deletePrefix := "DELETE FROM " + target
				s.counts(deletePrefix+"%", 0, 0, 0)
				work(fixture.Exec(deletePrefix+" WHERE k=1", 2))
				work(fixture.Exec(deletePrefix+" WHERE k=9", 0))
				s.counts(deletePrefix+"%", 1, 2, 2)
				s.entry(deletePrefix+" WHERE k=$1", 2, 2)
				s.both(fixture.Select("SELECT k,v FROM "+target+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
				s.both(fixture.Select("SELECT k,v FROM "+returning+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}))

				rollbackPrefix := "SELECT k AS pgss_rollback_value FROM " + source
				s.counts(rollbackPrefix+"%", 0, 0, 0)
				s.work(fixture.Exec("BEGIN", 0))
				work(fixture.Select(rollbackPrefix+" WHERE k=1", []string{"pgss_rollback_value"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.work(fixture.Exec("ROLLBACK", 0))
				// Statistics measure completed work, not committed table changes.
				s.counts(rollbackPrefix+"%", 1, 1, 1)
				s.entry(rollbackPrefix+" WHERE k=$1", 1, 1)
			})
		})
	}
}
