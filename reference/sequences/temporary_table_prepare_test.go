package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTablePrepare(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "BEGIN", 0)
		absentDrop := s.Held(s.C, "DROP TABLE pg_temp.prepared_absent")
		if err := absentDrop.Statement.Close(); err != nil {
			s.T.Fatal(err)
		}
		q := "CREATE TEMP TABLE prepared_quiet(v INT) ON COMMIT DROP"
		held := s.Held(s.C, q)
		defer held.Statement.Close()
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Select("SELECT relname FROM pg_class WHERE relname='prepared_quiet'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		s.Exec(s.C, "INSERT INTO prepared_quiet VALUES(11)", 1)
		fixture.TemporaryTableRows(s, s.C, "prepared_quiet", 11)
		s.Exec(s.C, "COMMIT", 0)
		s.Missing(s.C, "prepared_quiet", "v", "15")
	})
}
