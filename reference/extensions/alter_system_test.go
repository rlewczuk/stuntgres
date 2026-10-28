package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

const alterSystemMessage = "ALTER SYSTEM is not allowed in this environment"

var alterSystemCommands = []string{
	"ALTER SYSTEM SET application_name = 'external'",
	"ALTER SYSTEM SET application_name TO DEFAULT",
	"ALTER SYSTEM RESET application_name",
	"ALTER SYSTEM RESET ALL",
	"ALTER SYSTEM SET search_path TO public, pg_catalog",
	`ALTER SYSTEM SET "Custom"."Value" = true`,
	`ALTER SYSTEM RESET "Custom"."Value"`,
	"ALTER SYSTEM SET absent_setting = 'anything'",
	"ALTER SYSTEM SET config_file = '/tmp/not-a-config'",
	`ALTER SYSTEM SET application_name = E'line\nbreak'`,
	"ALTER SYSTEM SET work_mem = 1.25",
	"ALTER SYSTEM SET work_mem = -2e3",
	"ALTER SYSTEM SET work_mem = +0.5",
	"ALTER SYSTEM SET work_mem = 0x2a",
	"ALTER SYSTEM SET work_mem = 1_000",
	"ALTER SYSTEM SET work_mem = 999999999999999999999999999999999999999999",
}

type alterSystemState struct {
	p *fixture.Pair
	b fixture.Backend
	a, peer *sql.Conn
	showA, showB fixture.HeldStatement
}

func alterSystemScenario(t *testing.T, body func(*testing.T, *alterSystemState)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer a.Close()
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer peer.Close()
			s := &alterSystemState{p:p, b:b, a:a, peer:peer}
			s.exec(t, "CREATE TABLE alter_system_sentinel(v integer)")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO alter_system_sentinel VALUES(7)",1))
			s.exec(t,"SET application_name='alter-system-a'")
			fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Exec("SET application_name='alter-system-b'",0))
			s.showA = loadHeld(t,p,a,"SHOW application_name")
			s.showB = loadHeld(t,p,peer,"SHOW application_name")
			body(t,s)
		})
	}
}

func (s *alterSystemState) exec(t *testing.T, q string) {
	t.Helper()
	fixture.RunStep(t,s.p.Ctx,s.a,s.b.Recorder,fixture.Exec(q,0))
}

func (s *alterSystemState) unchanged(t *testing.T) {
	t.Helper()
	for _, item := range []struct{ c fixture.Executor; held fixture.HeldStatement; value string }{
		{s.a,s.showA,"alter-system-a"},{s.peer,s.showB,"alter-system-b"},
	} {
		step := fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{item.value}})
		fixture.RunStep(t,s.p.Ctx,item.c,s.b.Recorder,step)
		fixture.RunStep(t,s.p.Ctx,item.held,s.b.Recorder,step)
	}
	fixture.RunStep(t,s.p.Ctx,s.a,s.b.Recorder,fixture.Select("SELECT v FROM alter_system_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
}

func (s *alterSystemState) failure(t *testing.T, e fixture.Executor, q string, want fixture.Diagnostic, prepared, query, parse bool) {
	t.Helper()
	step := fixture.ErrorDiagnostic(q,want)
	step.Prepared, step.SelectRows, step.ErrorStage = prepared,query,"execute"
	if prepared && parse { step.ErrorStage = "prepare" }
	fixture.RunStep(t,s.p.Ctx,e,s.b.Recorder,step)
}

func TestReferenceAlterSystemBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_system_reader"})
	alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
		want := loadDiagnostic("0A000",alterSystemMessage)
		for _, q := range alterSystemCommands {
			for _, prepared := range []bool{false,true} {
				for _, query := range []bool{false,true} {
					s.failure(t,s.a,q,want,prepared,query,false)
					s.unchanged(t)
				}
			}
		}
		held := loadHeld(t,s.p,s.a,"ALTER SYSTEM RESET ALL")
		for _, granted := range []bool{false,true} {
			if !granted { s.exec(t,"CREATE ROLE alter_system_reader") }
			if granted { s.exec(t,"GRANT ALTER SYSTEM ON PARAMETER application_name TO alter_system_reader") }
			s.exec(t,"SET ROLE alter_system_reader")
			for _, query := range []bool{false,true} { s.failure(t,held,held.SQL,want,false,query,false) }
			s.exec(t,"RESET ROLE")
		}
		s.exec(t,"DROP OWNED BY alter_system_reader")
		s.exec(t,"DROP ROLE alter_system_reader")
		s.exec(t,"SET application_name TO DEFAULT")
		fixture.RunStep(t,s.p.Ctx,s.a,s.b.Recorder,fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{""}}))
		s.exec(t,"SET application_name='temporary'")
		s.exec(t,"RESET application_name")
		fixture.RunStep(t,s.p.Ctx,s.showA,s.b.Recorder,fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{""}}))
		s.exec(t,"SET application_name='alter-system-a'")
		s.unchanged(t)
	})
}

func TestReferenceAlterSystemSyntax(t *testing.T) {
	alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
		for _, tc := range []struct{ q, token string; pos int }{
			{"ALTER SYSTEM","",13},
			{"ALTER SYSTEM SET application_name =","",36},
			{"ALTER SYSTEM SET application_name = NULL","NULL",37},
			{"ALTER SYSTEM SET application_name = $1","$1",37},
			{"ALTER SYSTEM SET application_name FROM CURRENT","FROM",35},
			{"ALTER SYSTEM SET SESSION application_name='x'","application_name",26},
			{"ALTER SYSTEM RESET","",19},
			{"ALTER SYSTEM RESET ALL junk","junk",24},
			{"SET application_name='must-not-run'; ALTER SYSTEM RESET ALL junk","junk",61},
			{"PREPARE alter_system_sql AS ALTER SYSTEM RESET ALL","ALTER",29},
		} {
			message := "syntax error at end of input"
			if tc.token != "" { message = "syntax error at or near " + strconv.Quote(tc.token) }
			want := loadDiagnostic("42601",message)
			want.Position = strconv.Itoa(tc.pos)
			for _, prepared := range []bool{false,true} {
				for _, query := range []bool{false,true} { s.failure(t,s.a,tc.q,want,prepared,query,true) }
			}
			s.unchanged(t)
		}
	})
}
