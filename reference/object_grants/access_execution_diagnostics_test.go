package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessExecutionNullDiagnostics(t *testing.T) {
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		objects:=accessExecutionSetup(t,p,b,c,run)
		for _,o:=range objects {
			for _,call:=range accessExecutionCalls(t,p,c,o) {
				held:=accessExecutionHeld(t,p,c,call.query)
				for i:=range call.args {
					args:=append([]any(nil),call.args...); args[i]=nil
					s:=objectGrantAccessValue(call.query,nil,args...); run(s); fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					for j,value:=range args {
						if j==i {continue}
						switch value.(type) {case string:args[j]="ae_missing";case int64:args[j]=int64(4294967294)}
					}
					if i!=len(args)-1 {args[len(args)-1]="bad"}
					s=objectGrantAccessValue(call.query,nil,args...);run(s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
				// A failed runtime execution must not poison the same held recipe.
				bad:=append([]any(nil),call.args...); bad[len(bad)-1]="ALL"
				fixture.RunStep(t,p.Ctx,held,b.Recorder,accessExecutionError(call.query,"22023",`unrecognized privilege type: "ALL"`,bad...))
				fixture.RoleGrantActor(run,"og_object_reader",func(){fixture.RunStep(t,p.Ctx,held,b.Recorder,objectGrantAccessValue(call.query,o.initial,call.args...))})
			}
			q:="SELECT "+o.function+"($1::name,$2::text,$3::text) AS v"
			for _,tc:=range []struct{ role,object,privilege,code,message string }{
				{"ae_missing","ae_missing","bad","42704",`role "ae_missing" does not exist`},
				{"PUBLIC",o.name,o.privilege,"42704",`role "PUBLIC" does not exist`},
			} {run(accessExecutionError(q,tc.code,tc.message,tc.role,tc.object,tc.privilege))}
			wrongClass:="EXECUTE";if o.privilege=="EXECUTE" {wrongClass="USAGE"}
			run(accessExecutionError(q,"22023",fmt.Sprintf(`unrecognized privilege type: "%s"`,wrongClass),"og_object_reader",o.name,wrongClass))
			for _,bad:=range []string{"", "ALL",",",o.privilege+",",","+o.privilege,o.privilege+",bad",o.privilege+"  WITH GRANT OPTION",o.privilege+"\tWITH GRANT OPTION",o.privilege+" WITH GRANT OPTION bad"} {
				offending:=bad
				if i:=strings.IndexByte(bad,','); i>=0 { if i==0 {offending=""} else {offending=strings.TrimSpace(bad[i+1:])} }
				run(accessExecutionError(q,"22023",fmt.Sprintf(`unrecognized privilege type: "%s"`,offending),"og_object_reader",o.name,bad))
			}
			missingName:="ae_missing"; noun:=map[string]string{"has_language_privilege":"language","has_foreign_data_wrapper_privilege":"foreign-data wrapper","has_server_privilege":"server"}[o.function]
			if noun!="" { run(accessExecutionError(q,"42704",noun+` "ae_missing" does not exist`,"og_object_reader",missingName,"bad")) }
			if o.function=="has_function_privilege" {run(accessExecutionError(q,"42883",`function "ae_missing()" does not exist`,"og_object_reader","ae_missing()","bad"))}
			missingOID:="SELECT "+o.function+"('og_object_reader',4294967294::oid,$1::text) AS v"
			run(objectGrantAccessValue(missingOID,nil,o.privilege))
			run(objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',0::oid,$1::text) AS v",nil,o.privilege))
			run(accessExecutionError(missingOID,"22023",`unrecognized privilege type: "bad"`,"bad"))
			run(objectGrantAccessValue("SELECT "+o.function+"('postgres',4294967294::oid,$1::text) AS v",true,o.privilege))
			for _,prefix:=range []string{"SELECT ","/* é */ SELECT "} {
				for _,actual:=range []struct{ args,types string }{{"",""},{"true","boolean"},{"true,true,true,true","boolean, boolean, boolean, boolean"},{"true,'x','x'","boolean, unknown, unknown"},{"'x',true,'x'","unknown, boolean, unknown"},{"'x','x',true","unknown, unknown, boolean"}} {
					q:=prefix+o.function+"("+actual.args+")"
					position:="8"; if prefix!="SELECT " {position="16"}
					s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42883",Message:"function "+o.function+"("+actual.types+") does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:position})
					s.ErrorStage="execute"; if prepared {s.ErrorStage="prepare"}
					for _,query:=range []bool{false,true} {s.SelectRows=query;run(s)}
				}
			}
			analysisFailure:=func(query string,d fixture.Diagnostic){
				s:=fixture.ImplicitError(query,d);s.ErrorStage="execute";if prepared{s.ErrorStage="prepare"}
				for _,rows:=range []bool{false,true}{s.SelectRows=rows;run(s)}
			}
			hint:="No function matches the given name and argument types. You might need to add explicit type casts."
			for _,tc:=range []struct{query,signature string}{
				{`SELECT "`+strings.ToUpper(o.function)+`"('x','y')`,strings.ToUpper(o.function)+"(unknown, unknown)"},
				{"SELECT "+o.function+"_missing('x','y')",o.function+"_missing(unknown, unknown)"},
				{"SELECT "+o.function+"(who => 'x', privilege => 'y')",o.function+"(who => unknown, privilege => unknown)"},
			}{analysisFailure(tc.query,fixture.Diagnostic{Code:"42883",Message:"function "+tc.signature+" does not exist",Hint:hint,Position:"8"})}
			analysisFailure("SELECT ae_missing."+o.function+"('x','y')",fixture.Diagnostic{Code:"3F000",Message:`schema "ae_missing" does not exist`,Position:"8"})
			for _,decoration:=range []string{"DISTINCT","ORDER BY","FILTER"} {
				query:="SELECT "+o.function+"('x','y') FILTER (WHERE TRUE)"
				switch decoration {case "DISTINCT":query="SELECT "+o.function+"(DISTINCT 'x','y')";case "ORDER BY":query="SELECT "+o.function+"('x','y' ORDER BY 1)"}
				analysisFailure(query,fixture.Diagnostic{Code:"42809",Message:decoration+" specified, but "+o.function+" is not an aggregate function",Position:"8"})
			}
			// Runtime columns retain strictness without erasing the typed call.
			run(objectGrantAccessValue("SELECT "+o.function+"(v,'bad') AS v FROM (VALUES(NULL::text)) AS s(v)",nil))
			run(objectGrantAccessValue("SELECT CASE WHEN FALSE THEN "+o.function+"('ae_missing','bad') ELSE FALSE END AS v",false))
		}
	})
}

func TestReferenceAccessExecutionRoutineIdentity(t *testing.T) {
	objectGrantScenario(t,[]string{"ae_names"},func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		accessExecutionSetup(t,p,b,c,run)
		for _,q:=range []string{"CREATE SCHEMA ae_names","GRANT USAGE ON SCHEMA ae_names TO og_object_reader",`CREATE FUNCTION ae_names."Mixed"(integer,text) RETURNS integer RETURN $1`,"REVOKE EXECUTE ON FUNCTION ae_f(text) FROM PUBLIC"} {run(fixture.Exec(q,0))}
		for _,name:=range []string{"ae_f(int)","public.ae_f(int4)"," public . ae_f ( integer ) ",`ae_names."Mixed"(int,text)`} {run(objectGrantAccessValue("SELECT has_function_privilege('og_object_reader',$1::text,'EXECUTE') AS v",true,name))}
		run(objectGrantAccessValue("SELECT has_function_privilege('og_object_reader','ae_f(text)','EXECUTE') AS v",false))
		for _,tc:=range []struct{input,code,message string}{
			{"now","22P02","expected a left parenthesis"},
			{"ae_missing(integer)","42883",`function "ae_missing(integer)" does not exist`},
			{"ae_f(ae_missing)","42704",`type "ae_missing" does not exist`},
			{"-","42883",`function "-" does not exist`},
			{"0","42883",`function "0" does not exist`},
			{"4294967294","XX000","cache lookup failed for function 4294967294"},
		} {run(accessExecutionError("SELECT has_function_privilege('og_object_reader',$1::text,'EXECUTE') AS v",tc.code,tc.message,tc.input))}
		run(objectGrantAccessValue("SELECT has_function_privilege('postgres','4294967294','EXECUTE') AS v",true))
		var oid int64; if err:=c.QueryRowContext(p.Ctx,"SELECT 'ae_f(integer)'::regprocedure::oid").Scan(&oid);err!=nil {t.Fatal(err)}
		textCall:=accessExecutionHeld(t,p,c,"SELECT has_function_privilege('og_object_reader',$1::text,'EXECUTE') AS v")
		oidCall:=accessExecutionHeld(t,p,c,"SELECT has_function_privilege('og_object_reader',$1::oid,'EXECUTE') AS v")
		fixture.RunStep(t,p.Ctx,textCall,b.Recorder,objectGrantAccessValue(textCall.SQL,true,"ae_f(integer)"))
		run(fixture.Exec("ALTER FUNCTION ae_f(integer) RENAME TO ae_renamed",0))
		fixture.RunStep(t,p.Ctx,textCall,b.Recorder,accessExecutionError(textCall.SQL,"42883",`function "ae_f(integer)" does not exist`,"ae_f(integer)"))
		fixture.RunStep(t,p.Ctx,oidCall,b.Recorder,objectGrantAccessValue(oidCall.SQL,true,oid))
		run(fixture.Exec("SET search_path=ae_names,public",0))
		run(objectGrantAccessValue(`SELECT has_function_privilege('og_object_reader','"Mixed"(integer,text)','EXECUTE') AS v`,true))
	})
}
