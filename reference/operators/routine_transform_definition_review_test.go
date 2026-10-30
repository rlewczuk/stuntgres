package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionDropMissingLanguage(t *testing.T) {
	// Predictions come from REL_18_6 objectaddress.c and dropcmds.c, not
	// native output. IF EXISTS applies to both components of a transform pair.
	for _,tc:=range []struct{name,typ,language,code,message string; guarded bool}{
		{"missing_language_guarded","pg_catalog.int4","td_absent_language","00000","transform for type pg_catalog.int4 language \"td_absent_language\" does not exist, skipping",true},
		{"missing_language","integer","td_absent_language","42704","language \"td_absent_language\" does not exist",false},
		{"missing_type_guarded","td_absent_type","td_absent_language","00000","type \"td_absent_type\" does not exist, skipping",true},
		{"missing_type","td_absent_type","td_absent_language","42704","type \"td_absent_type\" does not exist",false},
		{"missing_schema_guarded","td_absent_schema.integer","td_absent_language","00000","schema \"td_absent_schema\" does not exist, skipping",true},
		{"missing_schema","td_absent_schema.integer","td_absent_language","3F000","schema \"td_absent_schema\" does not exist",false},
		{"missing_pair_guarded","pg_catalog.int8","sql","00000","transform for type pg_catalog.int8 language \"sql\" does not exist, skipping",true},
		{"missing_pair","bigint","sql","42704","transform for type bigint language \"sql\" does not exist",false},
	} {
		for _,prepared:=range []bool{false,true} {
			route:="direct"; if prepared {route="prepared"}
			t.Run(tc.name+"/"+route,func(t *testing.T){
				p:=fixture.NewScenario(t)
				for _,b:=range p.Backends(){
					t.Run(b.Name,func(t *testing.T){
						var notices []fixture.Diagnostic
						_,c,record:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
						// A genuine pinned builtin is metadata only: never invoke its
						// INTERNAL dictionary initializer ABI as a converter.
						hostExec(t,p,b,c,"CREATE TRANSFORM FOR integer LANGUAGE sql (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))")
						// Builtin-type transforms outlive public-schema reset.
						defer routineLanguagesCleanup(t,c,"DROP TRANSFORM FOR integer LANGUAGE sql")()
						var oid int64
						if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_transform WHERE trftype='integer'::regtype AND trflang=(SELECT oid FROM pg_language WHERE lanname='sql')").Scan(&oid);err!=nil {t.Fatal(err)}
						if oid<=0 {t.Fatalf("invalid sentinel transform OID %d",oid)}
						unchanged:=func(){
							t.Helper()
							hostRows(t,p,b,c,"SELECT oid::bigint AS id, trftype='integer'::regtype AS ty, trflang=(SELECT oid FROM pg_language WHERE lanname='sql') AS lang, trffromsql='pg_catalog.dsimple_init(internal)'::regprocedure::oid AS callback, trftosql=0 AS omitted FROM pg_transform",[]string{"id","ty","lang","callback","omitted"},[]string{"INT8","BOOL","BOOL","BOOL","BOOL"},[][]any{{oid,true,true,true,true}})
							// integer, sql and dsimple_init are all pinned. The
							// public catalog must omit every dependency to them.
							hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_transform'::regclass",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
							hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_language WHERE lanname='td_absent_language'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
						}
						unchanged()
						q:="DROP TRANSFORM ";if tc.guarded {q+="IF EXISTS "};q+="FOR "+tc.typ+" LANGUAGE "+tc.language
						var executor fixture.Executor=c
						if prepared {
							// Utility semantic lookup belongs to Execute, not Prepare.
							executor=transformsHeld(t,p,c,q)
						}
						if len(notices)!=0 {t.Fatalf("setup/prepare notices=%#v",notices)}
						hostExec(t,p,b,c,"BEGIN")
						hostExec(t,p,b,c,"SAVEPOINT td_review")
						if tc.guarded {
							fixture.RunStep(t,p.Ctx,executor,record,fixture.Exec(q,0))
							want:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:tc.code,Message:tc.message}
							if len(notices)!=1 || notices[0]!=want {t.Errorf("execute notices=%#v want exactly %#v",notices,want)}
							// A conditional miss must not abort the explicit transaction.
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							unchanged()
						} else {
							fixture.RunStep(t,p.Ctx,executor,record,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic(tc.code,tc.message)))
							if len(notices)!=0 {t.Errorf("unguarded error emitted notices=%#v",notices)}
							fixture.RunStep(t,p.Ctx,c,record,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
							hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							unchanged()
						}
						hostExec(t,p,b,c,"COMMIT")
						unchanged()
					})
				}
			})
		}
	}
}

func TestReferenceRoutineTransformDefinitionWrittenTypeDiagnostics(t *testing.T) {
	for _,tc:=range []struct{name,typ,language,callback,message string}{
		{"qualified_pseudo","pg_catalog.internal","sql","pg_catalog.dsimple_init(internal)","data type pg_catalog.internal is a pseudo-type"},
		{"pseudo_before_language","pg_catalog.internal","td_absent_language","pg_catalog.dsimple_init(internal)","data type pg_catalog.internal is a pseudo-type"},
		{"pseudo_before_callback","pg_catalog.internal","sql","td_absent_callback(internal)","data type pg_catalog.internal is a pseudo-type"},
		{"qualified_domain","public.td_review_domain","sql","pg_catalog.dsimple_init(internal)","data type public.td_review_domain is a domain"},
		{"quoted_domain",`"public"."td Review Domain"`,"sql","pg_catalog.dsimple_init(internal)","data type public.td Review Domain is a domain"},
		{"multibyte_domain",`public."Dömäin界"`,"sql","pg_catalog.dsimple_init(internal)","data type public.Dömäin界 is a domain"},
		{"embedded_quote_domain",`"public"."Dö""mäin界"`,"sql","pg_catalog.dsimple_init(internal)",`data type public.Dö"mäin界 is a domain`},
		{"domain_before_language",`public."Dömäin界"`,"td_absent_language","pg_catalog.dsimple_init(internal)","data type public.Dömäin界 is a domain"},
		{"domain_before_callback",`public."Dömäin界"`,"sql","td_absent_callback(internal)","data type public.Dömäin界 is a domain"},
	} {
		t.Run(tc.name,func(t *testing.T){
			p:=fixture.NewScenario(t)
			for _,b:=range p.Backends(){
				t.Run(b.Name,func(t *testing.T){
					c,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer c.Close()
					for _,q:=range []string{
						"CREATE DOMAIN td_review_domain AS integer",
						`CREATE DOMAIN "td Review Domain" AS integer`,
						`CREATE DOMAIN "Dömäin界" AS integer`,
						`CREATE DOMAIN "Dö""mäin界" AS integer`,
						"CREATE TRANSFORM FOR integer LANGUAGE sql (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))",
					} {hostExec(t,p,b,c,q)}
					defer routineLanguagesCleanup(t,c,"DROP TRANSFORM FOR integer LANGUAGE sql")()
					var oid int64
					if err=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_transform WHERE trftype='integer'::regtype").Scan(&oid);err!=nil {t.Fatal(err)}
					if oid<=0 {t.Fatalf("invalid sentinel transform OID %d",oid)}
					q:="CREATE TRANSFORM FOR "+tc.typ+" LANGUAGE "+tc.language+" (FROM SQL WITH FUNCTION "+tc.callback+")"
					// TypeNameToString joins written identifier components; it does
					// not re-quote them or hide a visible schema like format_type_be.
					for _,prepared:=range []bool{false,true} {
						route:="direct";if prepared {route="prepared"}
						t.Run(route,func(t *testing.T){
							// A failed assertion must not leave this shared borrower
							// aborted and prevent the prepared sibling from executing.
							defer routineLanguagesCleanup(t,c)()
							var executor fixture.Executor=c
							if prepared {executor=transformsHeld(t,p,c,q)}
							hostExec(t,p,b,c,"BEGIN")
							hostExec(t,p,b,c,"SAVEPOINT td_review")
							fixture.RunStep(t,p.Ctx,executor,b.Recorder,fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42809",tc.message)))
							fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("SELECT 17",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
							hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_review")
							hostRows(t,p,b,c,"SELECT oid::bigint AS id, trftype='integer'::regtype AS ty, trflang=(SELECT oid FROM pg_language WHERE lanname='sql') AS lang, trffromsql='pg_catalog.dsimple_init(internal)'::regprocedure::oid AS callback, trftosql=0 AS omitted FROM pg_transform",[]string{"id","ty","lang","callback","omitted"},[]string{"INT8","BOOL","BOOL","BOOL","BOOL"},[][]any{{oid,true,true,true,true}})
							hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_transform'::regclass",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
							hostRows(t,p,b,c,"SELECT 17 AS healthy",[]string{"healthy"},[]string{"INT4"},[][]any{{int64(17)}})
							hostExec(t,p,b,c,"COMMIT")
						})
					}
					// Rejected type validation must not poison a later genuine
					// declaration with the same language and callback metadata.
					hostExec(t,p,b,c,"CREATE TRANSFORM FOR bigint LANGUAGE sql (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))")
					defer routineLanguagesCleanup(t,c,"DROP TRANSFORM FOR bigint LANGUAGE sql")()
					hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_transform",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}})
				})
			}
		})
	}
}
