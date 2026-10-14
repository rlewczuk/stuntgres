package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewUnauthorized(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"temporary_view_reader_direct", "temporary_view_reader_prepared"})
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		reader := map[bool]string{false: "temporary_view_reader_direct", true: "temporary_view_reader_prepared"}[s.Prepared]
		s.Exec(s.C, "CREATE USER "+reader, 0)
		s.Utility("CREATE TABLE security_public(v INT)", nil)
		s.Utility("CREATE TEMP VIEW security_owner AS SELECT v FROM security_public", nil)
		temporaryViewNotices(t, n)
		schema := temporaryViewSchema(s, "security_owner")
		s.Exec(s.Peer, "SET SESSION AUTHORIZATION "+reader, 0)
		denied := fixture.ImplicitError("SELECT v FROM "+schema+".security_owner", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema " + schema, Position: "15"})
		denied.SelectRows = true
		denied.Prepared = s.Prepared
		fixture.RunStep(t, s.P.Ctx, s.Peer, s.Record, denied)
		s.Exec(s.Peer, "RESET SESSION AUTHORIZATION", 0)
		// Schema ownership supplies admitted USAGE without schema GRANT syntax.
		// The separately owned table still denies SELECT for a false/empty view.
		sourceSchema := reader + "_source"
		s.Utility("CREATE SCHEMA "+sourceSchema+" AUTHORIZATION "+reader, nil)
		t.Cleanup(func() {
			s.DB.SetMaxIdleConns(0)
			if err := s.Peer.Close(); err != nil {
				t.Error("invoker physical cleanup:", err)
			}
			if _, err := s.C.ExecContext(s.P.Ctx, "DROP SCHEMA "+sourceSchema+" CASCADE"); err != nil {
				t.Error("invoker source schema cleanup:", err)
			}
		})
		s.Utility("CREATE TABLE "+sourceSchema+".security_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO "+sourceSchema+".security_public VALUES(7)", 1)
		// Initialize the reader's own physical TEMP namespace: namespace owner10
		// does not replace the active session's ordinary CREATE/view authority.
		s.Exec(s.Peer, "SET SESSION AUTHORIZATION "+reader, 0)
		invoker := *s
		invoker.C = s.Peer
		invoker.Utility("CREATE TEMP VIEW security_invoker WITH(security_invoker=true) AS SELECT v FROM "+sourceSchema+".security_public WHERE false", nil)
		temporaryViewNotices(t, n)
		denied = fixture.ImplicitError("SELECT v FROM pg_temp.security_invoker", fixture.Diagnostic{Code: "42501", Message: "permission denied for table security_public"})
		denied.SelectRows = true
		denied.Prepared = s.Prepared
		denied.ErrorStage = "execute"
		fixture.RunStep(t, s.P.Ctx, s.Peer, s.Record, denied)
		s.Exec(s.Peer, "RESET SESSION AUTHORIZATION", 0)
		temporaryViewSelect(s, s.C, "SELECT v FROM "+sourceSchema+".security_public", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
	})
}
