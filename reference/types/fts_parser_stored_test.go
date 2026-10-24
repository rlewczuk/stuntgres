package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserStored(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"fts_parser_owner"})
	fixture.SchemaReferenceCleanup(t,"fts_parser_private")
	ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		conn,err:=b.DB.Conn(p.Ctx)
		if err!=nil { t.Fatal(err) }
		defer conn.Close()
		run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
		run(fixture.Exec("CREATE ROLE fts_parser_owner",0))
		run(fixture.Exec("CREATE SCHEMA fts_parser_private",0))
		run(fixture.Exec("CREATE TABLE fts_parser_stored_input(parser text,txt text)",0))
		run(fixture.Exec("INSERT INTO fts_parser_stored_input VALUES('default','Word')",1))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fts_parser_owner",0))
		run(fixture.Exec("GRANT SELECT ON fts_parser_stored_input TO fts_parser_owner",0))
		run(fixture.Exec("CREATE VIEW fts_parser_stored_view AS SELECT p.* FROM fts_parser_stored_input i CROSS JOIN LATERAL ts_parse(txt => i.txt,parser_name => i.parser) p",0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW fts_parser_stored_mv AS SELECT p.* FROM fts_parser_stored_input i CROSS JOIN LATERAL ts_parse(i.parser,i.txt) p WITH NO DATA",0))
		run(fixture.Exec("ALTER MATERIALIZED VIEW fts_parser_stored_mv OWNER TO fts_parser_owner",0))
		query:="SELECT * FROM fts_parser_stored_view"
		stmt,err:=conn.PrepareContext(p.Ctx,query)
		if err!=nil { t.Fatal(err) }
		defer stmt.Close()
		held:=fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:query}
		refresh:="REFRESH MATERIALIZED VIEW fts_parser_stored_mv"
		refreshStmt,err:=conn.PrepareContext(p.Ctx,refresh)
		if err!=nil { t.Fatal(err) }
		defer refreshStmt.Close()
		heldRefresh:=fixture.HeldStatement{Executor:conn,Statement:refreshStmt,SQL:refresh}
		check:=func(text string) {
			t.Helper()
			want:=[][]any{{int64(1),text}}
			s:=fixture.Select(query,[]string{"tokid","token"},[]string{"INT4","TEXT"},want)
			run(s); run(fixture.Prepared(s))
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			run(fixture.Select("SELECT * FROM fts_parser_stored_mv",[]string{"tokid","token"},[]string{"INT4","TEXT"},want))
		}
		run(fixture.Exec(refresh,0))
		check("Word")
		run(fixture.Exec("UPDATE fts_parser_stored_input SET txt='Again'",1))
		fixture.RunStep(t,p.Ctx,heldRefresh,b.Recorder,fixture.Exec(refresh,0))
		check("Again")
		run(fixture.Exec("UPDATE fts_parser_stored_input SET parser='fts_parser_private.default'",1))
		for _,prepared:=range []bool{false,true} {
			s:=fixture.ErrorDiagnostic(refresh,fixture.ControlsReferenceDiagnostic("42501","permission denied for schema fts_parser_private"))
			s.Prepared,s.ErrorStage=prepared,"execute"
			run(s)
		}
		// Caller is a superuser, but population must resolve with the owner.
		s:=fixture.ErrorDiagnostic(refresh,fixture.ControlsReferenceDiagnostic("42501","permission denied for schema fts_parser_private"))
		s.ErrorStage="execute"
		fixture.RunStep(t,p.Ctx,heldRefresh,b.Recorder,s)
		run(fixture.Select("SELECT * FROM fts_parser_stored_mv",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Again"}}))
		run(fixture.Exec("GRANT USAGE ON SCHEMA fts_parser_private TO fts_parser_owner",0))
		s=fixture.ErrorDiagnostic(refresh,fixture.ControlsReferenceDiagnostic("42704",`text search parser "fts_parser_private.default" does not exist`))
		s.ErrorStage="execute"
		fixture.RunStep(t,p.Ctx,heldRefresh,b.Recorder,s)
		run(fixture.Exec("UPDATE fts_parser_stored_input SET parser='default'",1))
		fixture.RunStep(t,p.Ctx,heldRefresh,b.Recorder,fixture.Exec(refresh,0))
		check("Again")
	})
}
