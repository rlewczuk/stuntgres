package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserPrivileges(t *testing.T) {
	for _, tc := range []struct { signature, name, call string; columns, types []string; rows [][]any }{
		{"ts_parse(text,text)","ts_parse","ts_parse('default'::text,'Word'::text)",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}}},
		{"ts_parse(oid,text)","ts_parse","ts_parse(3722::oid,'Word'::text)",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}}},
		{"ts_token_type(text)","ts_token_type","ts_token_type('default'::text)",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors()},
		{"ts_token_type(oid)","ts_token_type","ts_token_type(3722::oid)",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors()},
	} {
		t.Run(tc.signature,func(t *testing.T) {
			fixture.RoleReferenceCleanup(t,[]string{"fts_parser_reader"})
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				conn,err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
				run(fixture.Exec("CREATE ROLE fts_parser_reader",0))
				query := "SELECT * FROM pg_catalog."+tc.call+" ORDER BY tokid"
				stmt,err := conn.PrepareContext(p.Ctx,query)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:query}
				// Cleanup restores global ACL even after a failed discovery assertion.
				defer func() {
					ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
					defer cancel()
					for _, sql := range []string{"RESET ROLE","GRANT EXECUTE ON FUNCTION pg_catalog."+tc.signature+" TO PUBLIC","REVOKE EXECUTE ON FUNCTION pg_catalog."+tc.signature+" FROM fts_parser_reader"} {
						if _,err := conn.ExecContext(ctx,sql); err != nil { t.Error(err) }
					}
				}()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+tc.signature+" FROM PUBLIC",0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+tc.signature+" FROM fts_parser_reader",0))
				run(fixture.Exec("SET ROLE fts_parser_reader",0))
				for _, prepared := range []bool{false,true} {
					s := fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name))
					s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
					run(s)
				}
				s := fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+tc.name))
				s.SelectRows,s.ErrorStage = true,"execute"
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				run(fixture.Exec("RESET ROLE",0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+tc.signature+" TO fts_parser_reader",0))
				run(fixture.Exec("SET ROLE fts_parser_reader",0))
				fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,tc.columns,tc.types,tc.rows))
				run(fixture.Exec("RESET ROLE",0))
			})
		})
	}
	for _, call := range []string{"ts_parse('fts_parser_private.default','Word')","ts_token_type('fts_parser_private.default')"} {
		t.Run(call,func(t *testing.T) {
			fixture.RoleReferenceCleanup(t,[]string{"fts_parser_reader"})
			fixture.SchemaReferenceCleanup(t,"fts_parser_private")
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				conn,err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
				run(fixture.Exec("CREATE ROLE fts_parser_reader",0))
				run(fixture.Exec("CREATE SCHEMA fts_parser_private",0))
				run(fixture.Exec("REVOKE ALL ON SCHEMA fts_parser_private FROM PUBLIC",0))
				defer func() {
					ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
					defer cancel()
					if _,err := conn.ExecContext(ctx,"RESET ROLE"); err != nil { t.Error(err) }
				}()
				run(fixture.Exec("SET ROLE fts_parser_reader",0))
				for _, prepared := range []bool{false,true} {
					s := fixture.ErrorDiagnostic("SELECT * FROM "+call,fixture.ControlsReferenceDiagnostic("42501","permission denied for schema fts_parser_private"))
					s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
					run(s)
				}
				run(fixture.Exec("RESET ROLE",0))
			})
		})
	}
}
