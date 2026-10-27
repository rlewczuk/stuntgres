package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func relationSizeBodyError(q, code, message, hint string) fixture.Step {
	s := fixture.ImplicitError(q,fixture.Diagnostic{Code:code,Message:message,Hint:hint})
	s.SelectRows,s.ErrorStage = true,"execute"
	return s
}

func TestReferenceRelationSizeLookupForksNull(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"rs_path")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		relationSizeFixture(t,p,b,"rs_lookup")
		for _, prepared := range []bool{false,true} {
			for _, name := range []string{"pg_relation_size","pg_table_size","pg_indexes_size","pg_total_relation_size"} {
				for _, oid := range []string{"0","4294967295"} {
					s := relationSizeSelect("SELECT "+name+"('"+oid+"'::oid) AS v",[][]any{{nil}})
					s.Prepared=prepared; relationSizeRun(t,p,b,s)
				}
			}
			for _, call := range []string{"pg_relation_size(0::oid,'BAD')","pg_relation_size(NULL,'BAD')","pg_relation_size('rs_lookup',NULL)","pg_relation_size(NULL,NULL)"} {
				s:=relationSizeSelect("SELECT "+call+" AS v",[][]any{{nil}});s.Prepared=prepared;relationSizeRun(t,p,b,s)
			}
			for _, fork := range []string{"main","fsm","vm","init"} {
				s:=fixture.Select("SELECT pg_relation_size('rs_lookup','"+fork+"')>=0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}});s.Prepared=prepared;relationSizeRun(t,p,b,s)
			}
			for _, fork := range []string{"BAD","MAIN",""} {
				s:=relationSizeBodyError("SELECT pg_relation_size('rs_lookup','"+fork+"')","22023","invalid fork name",`Valid fork names are "main", "fsm", "vm", and "init".`)
				s.Prepared=prepared;relationSizeRun(t,p,b,s)
			}
			for _, q := range []string{"SELECT pg_indexes_size('rs_lookup_idx') AS v","SELECT pg_relation_size('rs_lookup','init') AS v"} {
				s:=relationSizeSelect(q,[][]any{{int64(0)}});s.Prepared=prepared;relationSizeRun(t,p,b,s)
			}
			s:=fixture.Select("SELECT pg_table_size('rs_lookup_idx')=pg_relation_size('rs_lookup_idx') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}});s.Prepared=prepared;relationSizeRun(t,p,b,s)
		}
		relationSizeRun(t,p,b,fixture.Exec("CREATE VIEW rs_lookup_view AS SELECT i FROM rs_lookup",0))
		relationSizeRun(t,p,b,relationSizeSelect("SELECT pg_total_relation_size('rs_lookup_view') AS v",[][]any{{int64(0)}}))
		relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE rs_parent(i INT) PARTITION BY RANGE(i)",0))
		relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE rs_child PARTITION OF rs_parent FOR VALUES FROM(0) TO(10)",0))
		relationSizeRun(t,p,b,fixture.Exec("INSERT INTO rs_parent VALUES(1)",1))
		relationSizeRun(t,p,b,relationSizeSelect("SELECT pg_total_relation_size('rs_parent') AS v",[][]any{{int64(0)}}))
		q:="SELECT pg_relation_size('rs_lookup')>=0 AS v"
		stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil { t.Fatal(err) };defer stmt.Close()
		var relationOID int64
		if err:=b.DB.QueryRowContext(p.Ctx,"SELECT 'rs_lookup'::regclass::oid").Scan(&relationOID);err!=nil{t.Fatal(err)}
		numericSQL:="SELECT pg_relation_size($1::oid)>=0 AS v"
		numeric,err:=b.DB.PrepareContext(p.Ctx,numericSQL);if err!=nil{t.Fatal(err)};defer numeric.Close()
		relationSizeRun(t,p,b,fixture.Exec("ALTER TABLE rs_lookup RENAME TO rs_renamed",0))
		// DDL invalidation reanalyzes the original implicit literal source;
		// preserve the pinned oracle's missing-old-name outcome.
		renamed:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42P01",Message:`relation "rs_lookup" does not exist`,Position:"25"})
		renamed.SelectRows,renamed.ErrorStage=true,"execute"
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q},b.Recorder,renamed)
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:b.DB,Statement:numeric,SQL:numericSQL},b.Recorder,fixture.Select(numericSQL,[]string{"v"},[]string{"BOOL"},[][]any{{true}},relationOID))
		oldName:=fixture.ImplicitError("SELECT pg_relation_size($1)>=0 AS v",fixture.Diagnostic{Code:"42P01",Message:`relation "rs_lookup" does not exist`,Context:`unnamed portal parameter $1 = '...'`},"rs_lookup")
		oldName.SelectRows,oldName.Prepared,oldName.ErrorStage=true,true,"execute"
		relationSizeRun(t,p,b,oldName)
		relationSizeRun(t,p,b,fixture.Prepared(fixture.Select("SELECT pg_relation_size($1::text)>=0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}},"rs_renamed")))
		conn,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer conn.Close()
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)}
		run(fixture.Exec("CREATE SCHEMA rs_path",0))
		run(fixture.Exec(`CREATE TABLE rs_path."Mixed Size"(i INT)`,0))
		run(fixture.Exec(`INSERT INTO rs_path."Mixed Size" VALUES(1),(2)`,2))
		run(fixture.Exec("CREATE TABLE public.rs_collision(i INT)",0))
		run(fixture.Exec("CREATE TABLE rs_path.rs_collision(i INT)",0))
		run(fixture.Exec("INSERT INTO rs_path.rs_collision VALUES(1)",1))
		run(fixture.Exec("SET search_path=rs_path,public",0))
		run(fixture.Select(`SELECT pg_relation_size('"Mixed Size"')=pg_relation_size('rs_path."Mixed Size"') AS v`,[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		run(fixture.Select("SELECT pg_relation_size('rs_collision')=pg_relation_size('rs_path.rs_collision') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		if !b.PG {run(fixture.Select("SELECT pg_relation_size('rs_collision')>pg_relation_size('public.rs_collision') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))}
		run(fixture.Exec("RESET search_path",0))
	}) }
}

func TestReferenceRelationSizeBindingErrors(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE rs_error(i INT)",0))
		relationSizeRun(t,p,b,fixture.Exec("INSERT INTO rs_error VALUES(99)",1))
		type sample struct { q,code,message,hint,at string }
		var cases []sample
		const hint="No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"pg_relation_size","pg_table_size","pg_indexes_size","pg_total_relation_size","pg_database_size","pg_tablespace_size"} {
			for _, tc := range []struct{call,signature string}{
				{name+"()",name+"()"},{name+"(true)",name+"(boolean)"},{name+"(1,2,3)",name+"(integer, integer, integer)"},
				{"public."+name+"(0)","public."+name+"(integer)"},{`"`+strings.ToUpper(name)+`"(0)`,strings.ToUpper(name)+"(integer)"},
				{name+"(target => 0)",name+"(target => integer)"},
			} { cases=append(cases,sample{"SELECT "+tc.call,"42883","function "+tc.signature+" does not exist",hint,tc.call}) }
			cases=append(cases,sample{"SELECT /* é界 */ "+name+"(true)","42883","function "+name+"(boolean) does not exist",hint,name})
			cases=append(cases,sample{"SELECT "+name+"(DISTINCT 0)","42809","DISTINCT specified, but "+name+" is not an aggregate function","",name})
			cases=append(cases,sample{"SELECT "+name+"(0) FILTER(WHERE TRUE)","42809","FILTER specified, but "+name+" is not an aggregate function","",name})
			cases=append(cases,sample{"SELECT "+name+"(0) OVER()","42809","OVER specified, but "+name+" is not a window function nor an aggregate function","",name})
			cases=append(cases,sample{"SELECT "+name+"(0 ORDER BY 1)","42809","ORDER BY specified, but "+name+" is not an aggregate function","",name})
			cases=append(cases,sample{"SELECT "+name+"(VARIADIC ARRAY[0])","42883","function "+name+"(integer[]) does not exist",hint,name})
		}
		cases=append(cases,
			sample{"SELECT pg_relation_size('rs_error',true)","42883","function pg_relation_size(unknown, boolean) does not exist",hint,"pg_relation_size"},
			sample{"SELECT pg_relation_size('rs_error'::name)","42883","function pg_relation_size(name) does not exist",hint,"pg_relation_size"},
			sample{"SELECT pg_relation_size_missing(0)","42883","function pg_relation_size_missing(integer) does not exist",hint,"pg_relation_size_missing"},
			sample{"SELECT absent_schema.pg_relation_size(0)","3F000",`schema "absent_schema" does not exist`,"","absent_schema.pg_relation_size"},
			sample{"SELECT pg_relation_size('rs_no_such_relation',NULL)","42P01",`relation "rs_no_such_relation" does not exist`,"","'rs_no_such_relation'"},
		)
		for _, tc:=range cases { for _, prepared:=range []bool{false,true} {
			index:=strings.Index(tc.q,tc.at)
			s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Hint:tc.hint,Position:fixture.Position(utf8.RuneCountInString(tc.q[:index])+1)})
			s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute";if prepared {s.ErrorStage="prepare"};relationSizeRun(t,p,b,s)
		} }
		for _, tc:=range []struct{call,code,message string}{
			{"pg_database_size('rs_missing_db')","3D000",`database "rs_missing_db" does not exist`},
			{"pg_database_size('4294967295'::oid)","42704","database with OID 4294967295 does not exist"},
			{"pg_tablespace_size('rs_missing_ts')","42704",`tablespace "rs_missing_ts" does not exist`},
			{"pg_tablespace_size('4294967295'::oid)","42704","tablespace with OID 4294967295 does not exist"},
		} {for _,prepared:=range []bool{false,true} {s:=relationSizeBodyError("SELECT "+tc.call,tc.code,tc.message,"");s.Prepared=prepared;relationSizeRun(t,p,b,s)}}
		for _,tc:=range []struct{name,code,label string}{{"pg_database_size","3D000","database"},{"pg_tablespace_size","42704","tablespace"}}{
			s:=relationSizeBodyError("SELECT "+tc.name+"($1)",tc.code,tc.label+` "0" does not exist`,"")
			s.Args=[]any{int64(0)};s.Prepared=true;relationSizeRun(t,p,b,s)
		}
		relationSizeRun(t,p,b,fixture.Prepared(relationSizeSelect("SELECT pg_relation_size($1) AS v",[][]any{{nil}},int64(0))))
		q:="SELECT pg_relation_size($1) AS v"
		s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42P01",Message:`relation "rs_no_such_parameter" does not exist`,Context:`unnamed portal parameter $1 = '...'`},"rs_no_such_parameter")
		s.SelectRows,s.Prepared,s.ErrorStage=true,true,"execute";relationSizeRun(t,p,b,s)
		relationSizeRun(t,p,b,fixture.Select("SELECT i FROM rs_error",[]string{"i"},[]string{"INT4"},[][]any{{int64(99)}}))
	}) }
}
