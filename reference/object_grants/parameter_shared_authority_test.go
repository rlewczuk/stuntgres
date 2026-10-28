package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceParameterACLSharedDelegation(t *testing.T) {
	parameterSharedScenario(t, "delegation", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
		ra, rb := parameterSharedRun(t, p, a), parameterSharedRun(t, p, b)
		reader := parameterSharedConn(t, p, b, "ps_reader")
		rr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
		owner := "postgres"
		if a.PG { owner = "stuntgres" }
		check := func(acl string) {
			for _, view := range []fixture.Backend{a, b} {
				parameterSharedRun(t, p, view)(fixture.Select("SELECT paracl::text AS acl FROM pg_parameter_acl WHERE parname='lc_messages'", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
			}
		}
		fixture.RoleGrantPermissionDenied(rr, "GRANT SET ON PARAMETER lc_messages TO ps_delegate", "parameter", "lc_messages")
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_reader WITH GRANT OPTION", 0))
		rr(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_delegate", 0))
		check("{"+owner+"=sA/"+owner+",ps_reader=s*/"+owner+",ps_delegate=s/ps_reader}")
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("REVOKE GRANT OPTION FOR SET ON PARAMETER lc_messages FROM ps_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
			s.Prepared, s.ErrorStage = prepared, "execute"
			rb(s)
			check("{"+owner+"=sA/"+owner+",ps_reader=s*/"+owner+",ps_delegate=s/ps_reader}")
		}
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_delegate", 0))
		rb(fixture.Exec("REVOKE GRANT OPTION FOR SET ON PARAMETER lc_messages FROM ps_reader CASCADE", 0))
		check("{"+owner+"=sA/"+owner+",ps_reader=s/"+owner+",ps_delegate=s/"+owner+"}")
		rb(parameterSharedInquiry("ps_delegate", "lc_messages", "SET", true))
		rb(parameterSharedInquiry("ps_reader", "lc_messages", "SET WITH GRANT OPTION", false))
		var notices []fixture.Diagnostic
		_, noticeConn, record := fixture.NoticeReferencePool(t, p, a.PG, &notices)
		fixture.RoleGrantNoticeExec(t, p, noticeConn, record, false, &notices, "SET SESSION AUTHORIZATION ps_reader")
		fixture.RoleGrantNoticeExec(t, p, noticeConn, record, false, &notices, "GRANT SET ON PARAMETER lc_messages TO ps_delegate", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "lc_messages"`})
		fixture.RoleGrantNoticeExec(t, p, noticeConn, record, true, &notices, "REVOKE SET ON PARAMETER lc_messages FROM ps_delegate", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `no privileges could be revoked for "lc_messages"`})
		fixture.RoleGrantNoticeExec(t, p, noticeConn, record, false, &notices, "RESET SESSION AUTHORIZATION")
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_parent", 0))
		ra(fixture.Exec("GRANT ps_parent TO ps_reader", 0))
		rb(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM ps_reader", 0))
		parameterSharedConsumers(t, p, b, reader, true)
		ra(fixture.Exec("GRANT ps_parent TO ps_reader WITH INHERIT FALSE", 0))
		parameterSharedConsumers(t, p, b, reader, false)
		rr(fixture.Exec("SET ROLE ps_parent", 0))
		parameterSharedConsumers(t, p, b, reader, true)
		rr(fixture.Exec("RESET ROLE", 0))
		ra(fixture.Exec("GRANT ps_parent TO ps_reader WITH INHERIT TRUE", 0))
		parameterSharedConsumers(t, p, b, reader, true)
		rb(fixture.Exec("REVOKE ps_parent FROM ps_reader", 0))
		parameterSharedConsumers(t, p, b, reader, false)
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO PUBLIC", 0))
		parameterSharedConsumers(t, p, b, reader, true)
		rb(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM PUBLIC", 0))
		parameterSharedConsumers(t, p, b, reader, false)
		ra(fixture.ImplicitError("GRANT SET ON PARAMETER lc_messages TO PUBLIC WITH GRANT OPTION", fixture.Diagnostic{Code: "0LP01", Message: "grant options can only be granted to roles"}))
		ra(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO ps_reader", 0))
		rb(parameterSharedInquiry("ps_reader", "lc_messages", "ALTER SYSTEM", true))
		parameterSharedConsumers(t, p, b, reader, false)
		rr(fixture.Exec("SET application_name='shared_acl_userset'", 0))
		rb(parameterSharedInquiry("ps_reader", "application_name", "SET", false))
		ra(fixture.Exec("GRANT ALL ON PARAMETER server_encoding TO ps_reader", 0))
		for _, q := range []string{"SET server_encoding='UTF8'", "RESET server_encoding", "SELECT set_config('server_encoding','UTF8',false)"} {
			fixture.RoleGrantError(rr, q, "55P02", `parameter "server_encoding" cannot be changed`)
		}
	})
}

func TestReferenceParameterACLSharedRoleCleanup(t *testing.T) {
	parameterSharedScenario(t, "cleanup", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
		ra, rb := parameterSharedRun(t, p, a), parameterSharedRun(t, p, b)
		const parameter = "ps_cleanup.value"
		const dependency = "privileges for parameter ps_cleanup.value"
		refusal := func() {
			for _, prepared := range []bool{false, true} {
				rb(fixture.RoleReferenceError("DROP ROLE ps_reader", "2BP01", `role "ps_reader" cannot be dropped because some objects depend on it`, dependency, prepared, "execute"))
			}
		}
		ra(fixture.Exec("GRANT SET ON PARAMETER "+parameter+" TO ps_reader", 0))
		ra(fixture.Exec("CREATE DATABASE ps_cleanup_c", 0))
		cDB, _, cRecord := fixture.DatabaseScenarioPool(t, p, a.PG, "ps_cleanup_c", "")
		future := fixture.Backend{DB: cDB, Recorder: cRecord, PG: a.PG}
		parameterSharedRun(t, p, future)(parameterSharedInquiry("ps_reader", parameter, "SET", true))
		var oldOID int64
		if err := a.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='ps_reader'").Scan(&oldOID); err != nil { t.Fatal(err) }
		q := parameterSharedInquiry("ps_reader", parameter, "SET", true)
		stmt, err := b.DB.PrepareContext(p.Ctx, q.Query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q.Query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, q)
		refusal()
		rb(fixture.Exec("REASSIGN OWNED BY ps_reader TO ps_parent", 0))
		refusal()
		rb(fixture.Exec("DROP OWNED BY ps_reader", 0))
		for _, view := range []fixture.Backend{a, b, future} { parameterSharedRun(t, p, view)(parameterSharedInquiry("ps_reader", parameter, "SET", false)) }
		rb(fixture.Exec("DROP ROLE ps_reader", 0))
		ra(fixture.Exec("CREATE ROLE ps_reader LOGIN PASSWORD 'role-core-fixture'", 0))
		q.Rows = [][]any{{false}}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, q)
		rb(fixture.Select("SELECT has_parameter_privilege($1::oid,'ps_cleanup.value','SET') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, oldOID))
		ra(fixture.Exec("GRANT SET ON PARAMETER "+parameter+" TO ps_reader WITH GRANT OPTION", 0))
		reader := parameterSharedConn(t, p, a, "ps_reader")
		fixture.RunStep(t, p.Ctx, reader, a.Recorder, fixture.Exec("GRANT SET ON PARAMETER "+parameter+" TO ps_delegate", 0))
		fixture.RunStep(t, p.Ctx, reader, a.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		reader.Close()
		c, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer c.Close()
		for _, sql := range []string{"BEGIN", "DROP OWNED BY ps_reader CASCADE", "DROP ROLE ps_reader", "COMMIT"} { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(sql, 0)) }
		ra(fixture.Select("SELECT parname FROM pg_parameter_acl WHERE parname='ps_cleanup.value'", []string{"parname"}, []string{"TEXT"}, nil))
		ra(fixture.Exec("CREATE ROLE ps_reader LOGIN PASSWORD 'role-core-fixture'", 0))
		// Genuine database-local dependencies in A must still block retirement in B.
		ra(fixture.Exec("CREATE TABLE ps_cleanup_local (v int)", 0))
		ra(fixture.Exec("ALTER TABLE ps_cleanup_local OWNER TO ps_reader", 0))
		rb(fixture.Exec("DROP OWNED BY ps_reader", 0))
		// PostgreSQL summarizes remote database-local dependencies by database.
		var database string
		if err := a.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil { t.Fatal(err) }
		for _, prepared := range []bool{false, true} {
			rb(fixture.RoleReferenceError("DROP ROLE ps_reader", "2BP01", `role "ps_reader" cannot be dropped because some objects depend on it`, "1 object in database "+database, prepared, "execute"))
		}
		ra(fixture.Exec("DROP OWNED BY ps_reader CASCADE", 0))
		rb(fixture.Exec("DROP ROLE ps_reader", 0))
		ra(fixture.Exec("CREATE ROLE ps_reader LOGIN PASSWORD 'role-core-fixture'", 0))
	})
}
