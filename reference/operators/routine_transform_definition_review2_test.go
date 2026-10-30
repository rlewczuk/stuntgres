package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionQualifiedCallbackDiagnostics(t *testing.T) {
	for _,spelling:=range []struct{name,schema,quotedSchema,quotedName,written string}{
		{"qualified","public","public","td_review_callback","public.td_review_callback"},
		{"quoted_multibyte",`Sché"ma`,`"Sché""ma"`,`"Dö""callback界"`,`Sché"ma.Dö"callback界`},
	} {
		for _,direction:=range []string{"from","to"} {
			for _,denial:=range []string{"owner","execute"} {
				t.Run(spelling.name+"/"+direction+"/"+denial,func(t *testing.T){
					fixture.RoleReferenceCleanup(t,[]string{"td_review_actor"})
					if spelling.schema!="public" {fixture.SchemaReferenceCleanup(t,spelling.schema)}
					definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
						definitionAnnotate(t,p,b,c)
						hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
						oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
						definitionFunction(t,p,b,c,"td_review_consumer","selection-v1",transformsSelectionPython)
						consumerSQL:="SELECT key,value FROM each(td_review_consumer($1::hstore)) ORDER BY key"
						held:=transformsHeld(t,p,c,consumerSQL)
						hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
						hostExec(t,p,b,c,"CREATE ROLE td_review_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
						hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA public TO td_review_actor")
						if spelling.schema!="public" {
							hostExec(t,p,b,c,"CREATE SCHEMA "+spelling.quotedSchema)
							hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA "+spelling.quotedSchema+" TO td_review_actor")
						}
						hostExec(t,p,b,c,"ALTER TYPE hstore OWNER TO td_review_actor")
						for _,name:=range []string{"td_a_from","td_b_to"} {hostExec(t,p,b,c,"ALTER FUNCTION "+name+"(internal) OWNER TO td_review_actor")}
						// Rename/move a genuine alternate converter, not a dummy
						// INTERNAL declaration. The existing pair remains untouched.
						original:="td_b_from";if direction=="to" {original="td_a_to"}
						hostExec(t,p,b,c,"ALTER FUNCTION public."+original+"(internal) RENAME TO "+spelling.quotedName)
						if spelling.schema!="public" {hostExec(t,p,b,c,"ALTER FUNCTION public."+spelling.quotedName+"(internal) SET SCHEMA "+spelling.quotedSchema)}
						callback:=spelling.quotedSchema+"."+spelling.quotedName
						message:="must be owner of function "+spelling.written
						if denial=="execute" {
							hostExec(t,p,b,c,"ALTER FUNCTION "+callback+"(internal) OWNER TO td_review_actor")
							hostExec(t,p,b,c,"REVOKE EXECUTE ON FUNCTION "+callback+"(internal) FROM PUBLIC,td_review_actor")
							message="permission denied for function "+spelling.written
						}
						for _,replace:=range []bool{false,true} {
							for _,prepared:=range []bool{false,true} {
								name:="create/direct";if replace {name="replace/direct"};if prepared {if replace {name="replace/prepared"} else {name="create/prepared"}}
								t.Run(name,func(t *testing.T){
									defer routineLanguagesCleanup(t,c)()
									hostExec(t,p,b,c,"SET ROLE td_review_actor")
									q:="CREATE ";if replace {q+="OR REPLACE "};q+="TRANSFORM FOR hstore LANGUAGE td_lang ("
									if direction=="from" {q+="FROM SQL WITH FUNCTION "+callback+"(internal), TO SQL WITH FUNCTION public.td_b_to(internal))"} else {q+="FROM SQL WITH FUNCTION public.td_a_from(internal), TO SQL WITH FUNCTION "+callback+"(internal))"}
									var executor fixture.Executor=c
									if prepared {executor=transformsHeld(t,p,c,q)}
									hostExec(t,p,b,c,"BEGIN")
									hostExec(t,p,b,c,"SAVEPOINT td_review")
									fixture.RunStep(t,p.Ctx,executor,b.Recorder,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501",message)))
									fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
									hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
									hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
									hostExec(t,p,b,c,"COMMIT")
									hostExec(t,p,b,c,"RESET ROLE")
									if got:=definitionPair(t,p,b,c,"td_a_from","td_b_to");got!=oid {t.Errorf("rejected declaration changed pair OID: %d want %d",got,oid)}
									definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
									hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
								})
							}
						}
					})
				})
			}
		}
	}
}

func TestReferenceRoutineTransformDefinitionLanguageNameDiagnostics(t *testing.T) {
	for _,language:=range []struct{name,sql,decoded string}{
		{"embedded_quote",`"td""review"`,`td"review`},
		{"backslash",`"td\review"`,`td\review`},
	} {
		for _,operation:=range []string{"conditional_absent_language","conditional_missing_pair","duplicate","missing_pair","create_absent_language","drop_absent_language"} {
			t.Run(language.name+"/"+operation,func(t *testing.T){
				definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,setup *sql.Conn){
					definitionAnnotate(t,p,b,setup)
					hostExec(t,p,b,setup,definitionCreate("td_a_from","td_b_to",false))
					oid:=definitionPair(t,p,b,setup,"td_a_from","td_b_to")
					definitionFunction(t,p,b,setup,"td_review_language_consumer","selection-v1",transformsSelectionPython)
					consumerSQL:="SELECT key,value FROM each(td_review_language_consumer($1::hstore)) ORDER BY key"
					var notices []fixture.Diagnostic
					_,c,record:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
					held:=transformsHeld(t,p,c,consumerSQL)
					hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
					exists:=operation=="conditional_missing_pair" || operation=="duplicate" || operation=="missing_pair"
					if exists {
						handler:="go_host_call_handler";if b.PG {handler="plpython3_call_handler"}
						hostExec(t,p,b,c,"CREATE LANGUAGE "+language.sql+" HANDLER "+handler)
						defer routineLanguagesCleanup(t,c,"DROP LANGUAGE "+language.sql+" CASCADE")()
					}
					create:="CREATE TRANSFORM FOR integer LANGUAGE "+language.sql+" (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))"
					var targetOID int64
					if operation=="duplicate" {
						hostExec(t,p,b,c,create)
						if err:=c.QueryRowContext(p.Ctx,"SELECT t.oid::bigint FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype='integer'::regtype AND l.lanname=$1",language.decoded).Scan(&targetOID);err!=nil {t.Fatal(err)}
						if targetOID<=0 {t.Fatalf("invalid target OID %d",targetOID)}
					}
					q:="DROP TRANSFORM FOR integer LANGUAGE "+language.sql
					code,message:="42704",`transform for type integer language "`+language.decoded+`" does not exist`
					guarded:=operation=="conditional_absent_language" || operation=="conditional_missing_pair"
					if guarded {q="DROP TRANSFORM IF EXISTS FOR pg_catalog.int4 LANGUAGE "+language.sql;code="00000";message=`transform for type pg_catalog.int4 language "`+language.decoded+`" does not exist, skipping`}
					if operation=="duplicate" {q=create;code="42710";message=`transform for type integer language "`+language.decoded+`" already exists`}
					if operation=="create_absent_language" {q=create}
					if operation=="create_absent_language" || operation=="drop_absent_language" {message=`language "`+language.decoded+`" does not exist`}
					unchanged:=func(t *testing.T){
						t.Helper()
						if got:=definitionPair(t,p,b,c,"td_a_from","td_b_to");got!=oid {t.Errorf("pair OID=%d want %d",got,oid)}
						definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
						if operation=="duplicate" {
							hostRows(t,p,b,c,"SELECT t.oid::bigint AS id,t.trffromsql='pg_catalog.dsimple_init(internal)'::regprocedure::oid AS callback,t.trftosql=0 AS omitted FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype='integer'::regtype AND l.lanname=$1",[]string{"id","callback","omitted"},[]string{"INT8","BOOL","BOOL"},[][]any{{targetOID,true,true}},language.decoded)
							hostRows(t,p,b,c,"SELECT objid=$1::oid AS source,objsubid=0 AS sub,refclassid='pg_language'::regclass AS class,refobjid=(SELECT oid FROM pg_language WHERE lanname=$2) AS target,refobjsubid=0 AS refsub,deptype='n' AS normal FROM pg_depend WHERE classid='pg_transform'::regclass AND objid=$1::oid",[]string{"source","sub","class","target","refsub","normal"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true}},targetOID,language.decoded)
						} else {
							hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype='integer'::regtype AND l.lanname=$1",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}},language.decoded)
						}
						hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_language WHERE lanname=$1",[]string{"n"},[]string{"INT8"},[][]any{{func()int64{if exists{return 1};return 0}()}},language.decoded)
						hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
					}
					unchanged(t)
					for _,prepared:=range []bool{false,true} {
						route:="direct";if prepared {route="prepared"}
						t.Run(route,func(t *testing.T){
							defer routineLanguagesCleanup(t,c)()
							notices=nil
							var executor fixture.Executor=c
							if prepared {executor=transformsHeld(t,p,c,q)}
							if len(notices)!=0 {t.Fatalf("prepare notices=%#v",notices)}
							hostExec(t,p,b,c,"BEGIN")
							hostExec(t,p,b,c,"SAVEPOINT td_review")
							if guarded {
								fixture.RunStep(t,p.Ctx,executor,record,fixture.Exec(q,0))
								want:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:code,Message:message}
								if len(notices)!=1 || notices[0]!=want {t.Errorf("notices=%#v want exactly %#v",notices,want)}
							} else {
								fixture.RunStep(t,p.Ctx,executor,record,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic(code,message)))
								if len(notices)!=0 {t.Errorf("error notices=%#v",notices)}
								fixture.RunStep(t,p.Ctx,c,record,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
								hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
							}
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							unchanged(t)
							hostExec(t,p,b,c,"COMMIT")
							unchanged(t)
						})
					}
				})
			})
		}
	}
}

func TestReferenceRoutineTransformDefinitionArrayTypeOwnershipDiagnostics(t *testing.T) {
	// REL_18_6 aclcheck_error_type formats an array's element type, not
	// format_type_be(array_oid). Scalar controls distinguish that rule from
	// stripping written qualification or removing quotes indiscriminately.
	for _,visibility:=range []struct{name,schema,typ,diagnostic string}{
		{"visible","public",`public."MixedType"`,`"MixedType"`},
		{"qualified","td Type Schema",`"td Type Schema"."MixedType"`,`"td Type Schema"."MixedType"`},
	} {
		for _,array:=range []bool{false,true} {
			kind:="scalar";if array {kind="array"}
			t.Run(visibility.name+"/"+kind,func(t *testing.T){
				fixture.RoleReferenceCleanup(t,[]string{"td_array_actor"})
				if visibility.schema!="public" {fixture.SchemaReferenceCleanup(t,visibility.schema)}
				definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
					definitionAnnotate(t,p,b,c)
					hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
					oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
					definitionFunction(t,p,b,c,"td_array_consumer","selection-v1",transformsSelectionPython)
					consumerSQL:="SELECT key,value FROM each(td_array_consumer($1::hstore)) ORDER BY key"
					held:=transformsHeld(t,p,c,consumerSQL)
					hostExec(t,p,b,c,"CREATE ROLE td_array_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
					hostExec(t,p,b,c,"GRANT USAGE ON SCHEMA public TO td_array_actor")
					if visibility.schema!="public" {
						hostExec(t,p,b,c,`CREATE SCHEMA "td Type Schema"`)
						hostExec(t,p,b,c,`GRANT USAGE ON SCHEMA "td Type Schema" TO td_array_actor`)
					}
					hostExec(t,p,b,c,"CREATE TYPE "+visibility.typ+" AS (v integer)")
					hostExec(t,p,b,c,"GRANT USAGE ON TYPE "+visibility.typ+" TO td_array_actor")
					typ:=visibility.typ;if array {typ+="[]"}
					q:="CREATE TRANSFORM FOR "+typ+" LANGUAGE td_lang (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))"
					unchanged:=func(t *testing.T){
						t.Helper()
						if got:=definitionPair(t,p,b,c,"td_a_from","td_b_to");got!=oid {t.Errorf("pair OID=%d want %d",got,oid)}
						definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
						hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_transform WHERE trftype IN ("+transformsLiteral(visibility.typ)+"::regtype,"+transformsLiteral(visibility.typ+"[]")+"::regtype)",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
						hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_depend d LEFT JOIN pg_transform t ON t.oid=d.objid WHERE d.classid='pg_transform'::regclass AND t.oid IS NULL",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
						hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
					}
					unchanged(t)
					for _,prepared:=range []bool{false,true} {
						route:="direct";if prepared {route="prepared"}
						t.Run(route,func(t *testing.T){
							defer routineLanguagesCleanup(t,c)()
							hostExec(t,p,b,c,"SET ROLE td_array_actor")
							var executor fixture.Executor=c
							if prepared {executor=transformsHeld(t,p,c,q)}
							hostExec(t,p,b,c,"BEGIN")
							hostExec(t,p,b,c,"SAVEPOINT td_review")
							fixture.RunStep(t,p.Ctx,executor,b.Recorder,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","must be owner of type "+visibility.diagnostic)))
							fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
							hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							hostExec(t,p,b,c,"COMMIT")
							hostExec(t,p,b,c,"RESET ROLE")
							unchanged(t)
						})
					}
				})
			})
		}
	}
}

func TestReferenceRoutineTransformDefinitionArrayTypeDropOwnershipDiagnostics(t *testing.T) {
	// Released OBJECT_TRANSFORM ownership uses aclcheck_error_type too.
	// The target must exist: a missing-pair error never reaches this check.
	for _,visibility:=range []struct{name,schema,typ,diagnostic string}{
		{"visible","public",`public."MixedType"`,`"MixedType"`},
		{"qualified","td Type Schema",`"td Type Schema"."MixedType"`,`"td Type Schema"."MixedType"`},
	} {
		for _,array:=range []bool{false,true} {
			kind:="scalar";if array {kind="array"}
			t.Run(visibility.name+"/"+kind,func(t *testing.T){
				fixture.RoleReferenceCleanup(t,[]string{"td_array_drop_actor"})
				if visibility.schema!="public" {fixture.SchemaReferenceCleanup(t,visibility.schema)}
				definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
					definitionAnnotate(t,p,b,c)
					hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
					baselineOID:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
					// This independent consumer executes genuine registered
					// converters; the composite target below is metadata only.
					definitionFunction(t,p,b,c,"td_array_drop_consumer","selection-v1",transformsSelectionPython)
					consumerSQL:="SELECT key,value FROM each(td_array_drop_consumer($1::hstore)) ORDER BY key"
					held:=transformsHeld(t,p,c,consumerSQL)
					hostExec(t,p,b,c,"CREATE ROLE td_array_drop_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
					hostExec(t,p,b,c,"GRANT USAGE ON SCHEMA public TO td_array_drop_actor")
					if visibility.schema!="public" {
						hostExec(t,p,b,c,`CREATE SCHEMA "td Type Schema"`)
						hostExec(t,p,b,c,`GRANT USAGE ON SCHEMA "td Type Schema" TO td_array_drop_actor`)
					}
					hostExec(t,p,b,c,"CREATE TYPE "+visibility.typ+" AS (v integer)")
					hostExec(t,p,b,c,"GRANT USAGE ON TYPE "+visibility.typ+" TO td_array_drop_actor")
					typ:=visibility.typ;if array {typ+="[]"}
					typeLiteral:=transformsLiteral(typ)+"::regtype"
					hostExec(t,p,b,c,"CREATE TRANSFORM FOR "+typ+" LANGUAGE td_lang (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))")
					var targetOID int64
					if err:=c.QueryRowContext(p.Ctx,"SELECT t.oid::bigint FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype="+typeLiteral+" AND l.lanname='td_lang'").Scan(&targetOID);err!=nil {t.Fatal(err)}
					if targetOID<=0 {t.Fatalf("invalid preexisting target OID %d",targetOID)}
					unchanged:=func(t *testing.T){
						t.Helper()
						hostRows(t,p,b,c,"SELECT t.oid::bigint AS id,t.trffromsql='pg_catalog.dsimple_init(internal)'::regprocedure::oid AS callback,t.trftosql=0 AS omitted FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE t.trftype="+typeLiteral+" AND l.lanname='td_lang'",[]string{"id","callback","omitted"},[]string{"INT8","BOOL","BOOL"},[][]any{{targetOID,true,true}})
						// The user type (including its actual array identity) and
						// unpinned language have normal edges; dsimple_init is pinned.
						step:=fixture.Select("SELECT refclassid::regclass::text AS class,objsubid=0 AS sub,((refclassid='pg_type'::regclass AND refobjid="+typeLiteral+") OR (refclassid='pg_language'::regclass AND refobjid=(SELECT oid FROM pg_language WHERE lanname='td_lang'))) AS target,refobjsubid=0 AS refsub,deptype='n' AS normal FROM pg_depend WHERE classid='pg_transform'::regclass AND objid=$1::oid",[]string{"class","sub","target","refsub","normal"},[]string{"TEXT","BOOL","BOOL","BOOL","BOOL"},[][]any{{"pg_language",true,true,true,true},{"pg_type",true,true,true,true}},targetOID)
						step.Bag=true
						fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
						if got:=definitionPair(t,p,b,c,"td_a_from","td_b_to");got!=baselineOID {t.Errorf("baseline OID=%d want %d",got,baselineOID)}
						definitionEdges(t,p,b,c,baselineOID,"td_a_from","td_b_to")
						hostRows(t,p,b,held,consumerSQL,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
					}
					unchanged(t)
					q:="DROP TRANSFORM FOR "+typ+" LANGUAGE td_lang"
					for _,prepared:=range []bool{false,true} {
						route:="direct";if prepared {route="prepared"}
						t.Run(route,func(t *testing.T){
							defer routineLanguagesCleanup(t,c)()
							hostExec(t,p,b,c,"SET ROLE td_array_drop_actor")
							var executor fixture.Executor=c
							if prepared {executor=transformsHeld(t,p,c,q)}
							hostExec(t,p,b,c,"BEGIN")
							hostExec(t,p,b,c,"SAVEPOINT td_review")
							fixture.RunStep(t,p.Ctx,executor,b.Recorder,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","must be owner of type "+visibility.diagnostic)))
							fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
							hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							hostExec(t,p,b,c,"COMMIT")
							hostExec(t,p,b,c,"RESET ROLE")
							unchanged(t)
						})
					}
				})
			})
		}
	}
}
