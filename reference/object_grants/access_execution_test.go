package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

type accessExecutionObject struct {
	function, name, target, privilege, catalog, key string
	oid int64
	initial bool
}

type accessExecutionCall struct {
	query, signature string
	args []any
}

func accessExecutionResetConnection(t *testing.T, c *sql.Conn) {
	t.Helper()
	ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
	for _,query:=range []string{"ROLLBACK","RESET ROLE","RESET SESSION AUTHORIZATION","RESET search_path"} {
		if _,err:=c.ExecContext(ctx,query);err!=nil {t.Error("reset access inquiry connection",query,err)}
	}
}

func accessExecutionCleanup(t *testing.T, b fixture.Backend, languageOwner string, languageACL sql.NullString) {
	t.Helper()
	t.Cleanup(func(){
		ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
		c,err:=b.DB.Conn(ctx)
		if err!=nil {t.Error("access inquiry cleanup connection",err);return}
		defer c.Close()
		accessExecutionResetConnection(t,c)
		for _,query:=range []string{"DROP SERVER IF EXISTS ae_srv CASCADE","DROP SERVER IF EXISTS ae_srv_null CASCADE","DROP SERVER IF EXISTS ae_srv_empty CASCADE","DROP FOREIGN DATA WRAPPER IF EXISTS ae_fdw CASCADE","DROP FOREIGN DATA WRAPPER IF EXISTS ae_fdw_null CASCADE","DROP FOREIGN DATA WRAPPER IF EXISTS ae_fdw_empty CASCADE"} {
			if _,err:=c.ExecContext(ctx,query);err!=nil {t.Error("access inquiry object cleanup",query,err)}
		}
		if b.PG {
			// GRANT/REVOKE owns PostgreSQL's shared role dependencies. Merely
			// restoring lanacl with UPDATE would leave those dependencies behind.
			if _,err:=c.ExecContext(ctx,"REVOKE ALL ON LANGUAGE sql FROM og_object_reader,og_object_other CASCADE");err!=nil {t.Error("remove language grant dependencies",err)}
			if _,err:=c.ExecContext(ctx,"UPDATE pg_catalog.pg_language SET lanowner=$1::regrole,lanacl=$2::aclitem[] WHERE lanname='sql'",languageOwner,languageACL);err!=nil {t.Error("restore language authority",err)}
		}
	})
}

func accessExecutionSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) []accessExecutionObject {
	t.Helper()
	var languageACL sql.NullString
	var languageOwner string
	if b.PG {
		if err := c.QueryRowContext(p.Ctx,"SELECT lanacl::text,lanowner::regrole::text FROM pg_language WHERE lanname='sql'").Scan(&languageACL,&languageOwner); err != nil { t.Fatal(err) }
	}
	if b.PG {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
	}
	// Register global cleanup before creating objects, including failure paths.
	accessExecutionCleanup(t,b,languageOwner,languageACL)
	for _, q := range []string{
		"CREATE FUNCTION ae_f(integer) RETURNS integer RETURN $1+1",
		"CREATE FUNCTION ae_f(text) RETURNS text RETURN $1",
		"CREATE FOREIGN DATA WRAPPER ae_fdw NO HANDLER",
		"CREATE SERVER ae_srv FOREIGN DATA WRAPPER ae_fdw",
	} { run(fixture.Exec(q, 0)) }
	objects := []accessExecutionObject{
		{"has_function_privilege", "public.ae_f(integer)", "FUNCTION public.ae_f(integer)", "EXECUTE", "pg_proc", "oid='public.ae_f(integer)'::regprocedure", 0, true},
		{"has_language_privilege", "sql", "LANGUAGE sql", "USAGE", "pg_language", "lanname='sql'", 0, true},
		{"has_foreign_data_wrapper_privilege", "ae_fdw", "FOREIGN DATA WRAPPER ae_fdw", "USAGE", "pg_foreign_data_wrapper", "fdwname='ae_fdw'", 0, false},
		{"has_server_privilege", "ae_srv", "FOREIGN SERVER ae_srv", "USAGE", "pg_foreign_server", "srvname='ae_srv'", 0, false},
	}
	for i := range objects {
		query:="SELECT oid FROM "+objects[i].catalog+" WHERE "+objects[i].key
		var args []any
		if objects[i].function=="has_function_privilege" {
			query="SELECT $1::regprocedure::oid"
			args=[]any{objects[i].name}
		}
		if err := c.QueryRowContext(p.Ctx,query,args...).Scan(&objects[i].oid); err != nil { t.Fatal(err) }
	}
	return objects
}

func accessExecutionCalls(t *testing.T, p *fixture.Pair, c *sql.Conn, o accessExecutionObject) []accessExecutionCall {
	t.Helper()
	var actor int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_reader'").Scan(&actor); err != nil { t.Fatal(err) }
	var calls []accessExecutionCall
	for _, role := range []struct{ typ string; value any }{{"name", "og_object_reader"}, {"oid", actor}, {"", nil}} {
		for _, object := range []struct{ typ string; value any }{{"text", o.name}, {"oid", o.oid}} {
			formals := []string{object.typ,"text"}
			args := []any{object.value,o.privilege}
			if role.typ != "" { formals = append([]string{role.typ}, formals...); args = append([]any{role.value},args...) }
			var placeholders []string
			for i, typ := range formals { placeholders = append(placeholders,fmt.Sprintf("$%d::%s",i+1,typ)) }
			calls = append(calls,accessExecutionCall{"SELECT "+o.function+"("+strings.Join(placeholders,",")+") AS v", "pg_catalog."+o.function+"("+strings.Join(formals,",")+")",args})
		}
	}
	return calls
}

func accessExecutionHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx,query)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ stmt.Close() })
	return fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}
}

// accessExecutionPairedHeld runs s through h after the caller ran the same
// fresh step. A held statement runs identically in both routes, so only the
// prepared route runs it.
func accessExecutionPairedHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, h fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	if prepared {
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	}
}

func accessExecutionError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q,fixture.Diagnostic{Code:code,Message:message},args...)
	s.SelectRows, s.ErrorStage = true,"execute"
	return s
}

func accessExecutionColumnCall(call accessExecutionCall) string {
	formals := strings.Split(call.signature[strings.IndexByte(call.signature, '(')+1:len(call.signature)-1], ",")
	query := call.query
	for i, typ := range formals {
		column := "pr"
		if i == len(formals)-2 {
			column = "ot"
			if typ == "oid" { column = "oo" }
		} else if i < len(formals)-2 {
			column = "an"
			if typ == "oid" { column = "ao" }
		}
		query = strings.ReplaceAll(query, fmt.Sprintf("$%d::%s", i+1, typ), column)
	}
	return query
}

func TestReferenceAccessExecutionOverloads(t *testing.T) {
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		objects := accessExecutionSetup(t,p,b,c,run)
		run(fixture.Exec("CREATE TABLE ae_inputs(an name,ao oid,ot text,oo oid,pr text)", 0))
		run(fixture.Exec("GRANT SELECT ON ae_inputs TO og_object_reader", 0))
		for _, o := range objects {
			run(fixture.Exec("TRUNCATE ae_inputs", 0))
			run(fixture.Exec("INSERT INTO ae_inputs SELECT rolname,oid,$1::text,$2::oid,$3::text FROM pg_roles WHERE rolname='og_object_reader'", 1, o.name, o.oid, o.privilege))
			for _, call := range accessExecutionCalls(t,p,c,o) {
				held := accessExecutionHeld(t,p,c,call.query)
				fixture.RoleGrantActor(run,"og_object_reader",func(){
					s := objectGrantAccessValue(call.query,o.initial,call.args...)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						types,err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
						if len(types)!=1 || types[0].Name()!="v" || types[0].DatabaseTypeName()!="BOOL" { t.Fatal(types) }
						if b.PG { if _,known:=types[0].Nullable(); known { t.Fatal("pq nullability unexpectedly known") } } else {
							if types[0].ScanType()!=reflect.TypeOf(false) { t.Fatal("BOOL ScanType",types[0].ScanType()) }
							if nullable,known:=types[0].Nullable(); !known || !nullable { t.Fatal("computed inquiry nullability",nullable,known) }
						}
					}
					run(s)
					accessExecutionPairedHeld(t,p,b,prepared,held,s)
					literalQuery:=call.query
					for i,arg:=range call.args {
						literal:=fmt.Sprint(arg)
						if text,ok:=arg.(string);ok {literal=pq.QuoteLiteral(text)}
						literalQuery=strings.ReplaceAll(literalQuery,fmt.Sprintf("$%d",i+1),literal)
					}
					run(objectGrantAccessValue(literalQuery,o.initial))
					column := accessExecutionColumnCall(call)+" FROM ae_inputs"
					run(objectGrantAccessValue(column,o.initial))
					if prepared {
						columnHeld := accessExecutionHeld(t,p,c,column)
						fixture.RunStep(t,p.Ctx,columnHeld,b.Recorder,objectGrantAccessValue(column,o.initial))
					}
				})
			}
			run(objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1,$2) AS v",o.initial,o.name,o.privilege))
			run(objectGrantAccessValue("SELECT "+o.function+"(current_user,$1::text,$2::text) AS v",true,o.name,o.privilege))
		}
	})
}

func TestReferenceAccessExecutionACL(t *testing.T) {
	objectGrantScenario(t,nil,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step),prepared bool){
		defer accessExecutionResetConnection(t,c)
		objects := accessExecutionSetup(t,p,b,c,run)
		for _, o := range objects {
			calls := accessExecutionCalls(t,p,c,o)
			held := make([]fixture.HeldStatement,len(calls)); for i,call:=range calls { held[i]=accessExecutionHeld(t,p,c,call.query) }
			check := func(ordinary,options bool){
				fixture.RoleGrantActor(run,"og_object_reader",func(){
					for i,call:=range calls {
						for _, variant:=range []struct{ privilege string; value bool }{{o.privilege,ordinary},{o.privilege+" WITH GRANT OPTION",options},{" "+strings.ToLower(o.privilege)+", "+o.privilege+" WITH GRANT OPTION ",ordinary||options},{o.privilege+","+strings.ToLower(o.privilege),ordinary}} {
							args:=append([]any(nil),call.args...); args[len(args)-1]=variant.privilege
							s:=objectGrantAccessValue(call.query,variant.value,args...); run(s); accessExecutionPairedHeld(t,p,b,prepared,held[i],s)
						}
					}
				})
			}
			check(o.initial,false)
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM PUBLIC",0)); check(false,false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader",0)); check(true,false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader WITH GRANT OPTION",0)); check(true,true)
			run(fixture.Exec("REVOKE GRANT OPTION FOR "+o.privilege+" ON "+o.target+" FROM og_object_reader CASCADE",0)); check(true,false)
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_reader",0)); check(false,false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_other",0))
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE",0)); check(true,false)
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT FALSE",0)); check(false,false)
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader",0));check(true,false)
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_reader",0));check(false,false)
			run(fixture.Exec("REVOKE og_object_other FROM og_object_reader",0))
			for _, public:=range []bool{true,false} {
				verb:="REVOKE "; tail:=" FROM PUBLIC"; if public { verb="GRANT "; tail=" TO PUBLIC" }
				run(fixture.Exec(verb+o.privilege+" ON "+o.target+tail,0))
				for _,actor:=range []string{"'public'::name","4294967294::oid"} { run(objectGrantAccessValue("SELECT "+o.function+"("+actor+",$1::text,$2::text) AS v",public,o.name,o.privilege)) }
			}
			if o.function == "has_function_privilege" || o.function == "has_server_privilege" {
				run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM postgres",0))
				// An ordinary owner (not superuser) has revocable ordinary rights.
				target := strings.TrimPrefix(o.target, "FOREIGN ")
				if strings.HasPrefix(o.target, "FOREIGN DATA WRAPPER") { target = o.target }
				run(fixture.Exec("ALTER "+target+" OWNER TO og_object_other",0))
				run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM og_object_other",0))
				run(objectGrantAccessValue("SELECT "+o.function+"('og_object_other',$1::text,$2::text) AS v",false,o.name,o.privilege))
				run(objectGrantAccessValue("SELECT "+o.function+"('og_object_other',$1::text,$2::text) AS v",true,o.name,o.privilege+" WITH GRANT OPTION"))
				run(fixture.Exec("ALTER "+target+" OWNER TO postgres",0))
			}
		}
		for _, language:=range []string{"internal","c"} { run(objectGrantAccessValue("SELECT has_language_privilege('og_object_reader',$1,'USAGE') AS v",true,language)) }
		// Bootstrap language and wrapper owners are necessarily superusers.
		// Inquiry success must not confer permission to create untrusted bodies.
		run(objectGrantAccessValue("SELECT has_language_privilege(lanowner,'sql','USAGE WITH GRANT OPTION') AS v FROM pg_language WHERE lanname='sql'",true))
		run(fixture.Exec("GRANT CREATE ON SCHEMA public TO og_object_reader",0))
		fixture.RoleGrantActor(run,"og_object_reader",func(){
			run(fixture.ImplicitError("CREATE FUNCTION ae_untrusted() RETURNS integer LANGUAGE c AS 'missing_library','missing_symbol'",fixture.Diagnostic{Code:"42501",Message:"permission denied for language c"}))
		})
	})
}
