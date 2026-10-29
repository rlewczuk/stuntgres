package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func routineDeparseFailure(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,q string,want fixture.Diagnostic,prepared,execute bool,args []any) {
	t.Helper()
	var err error
	stage := "execute"
	if prepared {
		var stmt *sql.Stmt
		stmt,err = c.PrepareContext(p.Ctx,q)
		if err != nil { stage = "prepare" } else {
			defer stmt.Close()
			var rows *sql.Rows
			rows,err = stmt.QueryContext(p.Ctx,args...)
			if rows != nil { rows.Close();t.Fatal("rejected inquiry published rows") }
		}
	} else {
		var rows *sql.Rows
		rows,err = c.QueryContext(p.Ctx,q,args...)
		if rows != nil { rows.Close();t.Fatal("rejected inquiry published rows") }
	}
	if err == nil { t.Fatal("missing diagnostic",q) }
	if prepared && (stage == "execute") != execute { t.Errorf("stage %s, execute=%v",stage,execute) }
	fixture.AssertDiagnostic(t,err,&want)
}

func TestReferenceRoutineDeparseDiagnostics(t *testing.T) {
	fixture.DeparseRelationsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		resultsExec(t,p,b,c,"CREATE TABLE rd_diagnostic(i integer)")
		for _,name := range routineDeparseNames {
			for _,tc := range []struct{call,signature string}{
				{name+"()",name+"()"},{name+"(0,0)",name+"(integer, integer)"},
				{name+"(true)",name+"(boolean)"},{name+"('1'::text)",name+"(text)"},
				{name+"(0::oid,false)",name+"(oid, boolean)"},
				{`"`+strings.ToUpper(name)+`"(0::oid)`,strings.ToUpper(name)+"(oid)"},
				{"public."+name+"(0::oid)","public."+name+"(oid)"},
			} {
				want := fixture.ControlsReferenceDiagnostic("42883","function "+tc.signature+" does not exist")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,"SELECT "+tc.call,want,prepared,false,nil) }
			}
			for _,tc := range []struct{call,code,message,token string}{
				{name+"('bad')","22P02",`invalid input syntax for type oid: "bad"`,"'bad'"},
				{name+"('4294967296')","22003",`value "4294967296" is out of range for type oid`,"'4294967296'"},
				{name+"(missing) FROM rd_diagnostic","42703",`column "missing" does not exist`,"missing"},
				{name+"(NULL::oid + missing) FROM rd_diagnostic","42703",`column "missing" does not exist`,"missing"},
				{"absent_schema."+name+"(0)","3F000",`schema "absent_schema" does not exist`,"absent_schema"},
			} {
				q := "SELECT "+tc.call
				want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
				want.Position = fixture.Position(strings.Index(q,tc.token)+1)
				for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,q,want,prepared,false,nil) }
			}
			q := "SELECT 'été', "+name+"() FROM rd_diagnostic"
			want := fixture.ControlsReferenceDiagnostic("42883","function "+name+"() does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q,name)])+1)
			for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,q,want,prepared,false,nil) }
			for _,tc := range []struct{value,code,message string}{
				{"bad","22P02",`invalid input syntax for type oid: "bad"`},
				{"4294967296","22003",`value "4294967296" is out of range for type oid`},
			} {
				want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
				want.Context = "unnamed portal parameter $1 = '...'"
				for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,"SELECT "+name+"($1)",want,prepared,true,[]any{tc.value}) }
			}
			routineDeparseText(t,p,b,c,"SELECT "+name+"(0::oid) AS definition",nil)
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM rd_diagnostic",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
	})
}
