package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow PostgreSQL 18's shared pg_parameter_acl and guc_privs
// regression, independently of native output.
func parameterSharedScenario(t *testing.T, suffix string, body func(*testing.T, *fixture.Pair, fixture.Backend, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ps_reader", "ps_delegate", "ps_parent"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, role := range []string{"ps_reader", "ps_delegate", "ps_parent"} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", 0))
			}
			name := "ps_" + suffix + "_b"
			if b.PG { databases = append(databases, name) }
			if b.PG && (suffix == "admission" || suffix == "cleanup") { databases = append(databases, "ps_"+suffix+"_c") }
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DATABASE "+name, 0))
			db, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, name, "")
			peer := fixture.Backend{Name: b.Name, DB: db, Recorder: recorder, PG: b.PG}
			t.Cleanup(func() {
				for _, role := range []string{"ps_reader", "ps_delegate", "ps_parent"} {
					if _, err := b.DB.ExecContext(p.Ctx, "DROP OWNED BY "+role+" CASCADE"); err != nil { t.Error(err) }
				}
			})
			body(t, p, b, peer)
		})
	}
}

func parameterSharedRun(t *testing.T, p *fixture.Pair, b fixture.Backend) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
}

func parameterSharedInquiry(role, parameter, privilege string, want bool) fixture.Step {
	return fixture.Select("SELECT has_parameter_privilege('"+role+"','"+parameter+"','"+privilege+"') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}})
}

func parameterSharedConn(t *testing.T, p *fixture.Pair, b fixture.Backend, role string) *sql.Conn {
	t.Helper()
	var database string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil { t.Fatal(err) }
	pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, database, role)
	c, err := pool.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { c.Close() })
	return c
}

func parameterSharedConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, allowed bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"SET lc_messages='C'", "RESET lc_messages", "SELECT set_config('lc_messages','C',false)"} {
			var s fixture.Step
			if !allowed {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "lc_messages"`})
				s.SelectRows = q[0] == 'S' && q[1] == 'E' && len(q) > 6 && q[:6] == "SELECT"
				s.ErrorStage = "execute"
			} else if q[:3] == "SEL" {
				s = fixture.Select(q, []string{"set_config"}, []string{"TEXT"}, [][]any{{"C"}})
			} else { s = fixture.Exec(q, 0) }
			if prepared { s = fixture.Prepared(s) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
}

func TestReferenceParameterACLSharedAdmission(t *testing.T) {
	parameterSharedScenario(t, "admission", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
		ra, rb := parameterSharedRun(t, p, a), parameterSharedRun(t, p, b)
		reader := parameterSharedConn(t, p, b, "ps_reader")
		readerA := parameterSharedConn(t, p, a, "ps_reader")
		parameterSharedConsumers(t, p, b, reader, false)
		inquiry := parameterSharedInquiry("ps_reader", "lc_messages", "SET", false)
		stmt, err := reader.PrepareContext(p.Ctx, inquiry.Query)
		if err != nil { t.Fatal("prepare held inquiry before grant", err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: inquiry.Query}
		const setting = "SELECT set_config('lc_messages',$1,false)"
		setStmt, err := reader.PrepareContext(p.Ctx, setting)
		if err != nil { t.Fatal("prepare held set_config before grant", err) }
		defer setStmt.Close()
		heldSet := fixture.HeldStatement{Executor: reader, Statement: setStmt, SQL: setting}
		for _, allowed := range []bool{true, false, true} {
			if allowed { ra(fixture.Exec(`GRANT SET ON PARAMETER "LC_MESSAGES" TO ps_reader`, 0)) } else { rb(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM ps_reader", 0)) }
			parameterSharedConsumers(t, p, b, reader, allowed)
			parameterSharedConsumers(t, p, a, readerA, allowed)
			inquiry.Rows = [][]any{{allowed}}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, inquiry)
			var s fixture.Step
			if allowed { s = fixture.Select(setting, []string{"set_config"}, []string{"TEXT"}, [][]any{{"C"}}, "C") } else {
				s = fixture.ImplicitError(setting, fixture.Diagnostic{Code: "42501", Message: `permission denied to set parameter "lc_messages"`}, "C")
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			fixture.RunStep(t, p.Ctx, heldSet, b.Recorder, s)
			for _, view := range []fixture.Backend{a, b} {
				run := parameterSharedRun(t, p, view)
				run(parameterSharedInquiry("ps_reader", "lc_messages", "SET", allowed))
				run(parameterSharedInquiry("ps_reader", "lc_messages", "ALTER SYSTEM", false))
				run(parameterSharedInquiry("ps_reader", "lc_messages", "SET WITH GRANT OPTION", false))
			}
		}
		owner := "postgres"
		if a.PG { owner = "stuntgres" }
		acl := "{"+owner+"=sA/"+owner+",ps_reader=s/"+owner+"}"
		ra(fixture.Exec("CREATE DATABASE ps_admission_c", 0))
		cDB, _, cRecord := fixture.DatabaseScenarioPool(t, p, a.PG, "ps_admission_c", "")
		var identity int64
		if err := a.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname='lc_messages'").Scan(&identity); err != nil || identity <= 0 || identity > 4294967295 { t.Fatal("valid parameter identity", identity, err) }
		for _, view := range []fixture.Backend{a, b, {DB: cDB, Recorder: cRecord, PG: a.PG}} {
			var oid int64
			if err := view.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_parameter_acl WHERE parname='lc_messages'").Scan(&oid); err != nil || oid != identity { t.Fatal("cluster parameter identity", identity, oid, err) }
			parameterSharedRun(t, p, view)(fixture.Select("SELECT parname,paracl::text AS acl FROM pg_parameter_acl WHERE parname='lc_messages'", []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, [][]any{{"lc_messages", acl}}))
			parameterSharedRun(t, p, view)(parameterSharedInquiry("ps_reader", "lc_messages", "SET", true))
		}
	})
}
