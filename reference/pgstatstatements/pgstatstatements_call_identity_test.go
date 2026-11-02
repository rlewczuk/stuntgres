package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCallIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE PROCEDURE pgss_part.pgss_call_identity(IN inputvalue integer,OUT result integer) LANGUAGE plpgsql AS $$BEGIN result:=inputvalue+1; END$$")
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		s.counts("CALL pgss_part.pgss_call_identity%", 0, 0, 0)
		s.work(fixture.Select("CALL pgss_part.pgss_call_identity(1,NULL)", []string{"result"}, []string{"INT4"}, [][]any{{int64(2)}}))
		// Raw spelling is ignored for CallStmt: the selected authenticated
		// procedure identity, typed inputs and typed OUT expressions govern.
		s.exec("ALTER PROCEDURE pgss_part.pgss_call_identity(integer) RENAME TO pgss_call_identity_renamed")
		s.work(fixture.Prepared(fixture.Select("CALL pgss_part.pgss_call_identity_renamed(2,NULL)", []string{"result"}, []string{"INT4"}, [][]any{{int64(3)}})))
		s.counts("CALL pgss_part.pgss_call_identity%", 1, 2, 0)
		s.entry("CALL pgss_part.pgss_call_identity($1,$2)", 2, 0)
	})
}
