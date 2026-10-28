package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func activityDiagnostic(code, message string, position int) fixture.Diagnostic {
	return fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:code, Message:message, Position:fixture.Position(position)}
}
func activityError(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, want fixture.Diagnostic) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if rows != nil { rows.Close(); t.Fatal("failed query published rows") }
	fixture.AssertDiagnostic(t, err, &want)
	stmt, err := c.PrepareContext(p.Ctx, query)
	if stmt != nil { stmt.Close(); t.Fatal("failed prepare published statement") }
	fixture.AssertDiagnostic(t, err, &want)
}

func TestReferenceSessionActivityDiagnosticsAndCatalog(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		before := activityCapture(t, p, b, c)
		for _, name := range activityNames {
			for _, tc := range []struct { suffix, signature string }{{"(1)","integer"},{"(true)","boolean"},{"(NULL)","unknown"},{"(NULL::text)","text"},{"(1,2)","integer, integer"},{"(x => 1)","x => integer"}} {
				want := activityDiagnostic("42883", "function "+name+"("+tc.signature+") does not exist", 8)
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t,p,c,"SELECT "+name+tc.suffix,want)
			}
			for _, tc := range []struct{ call, signature string }{{"public."+name+"()", "public."+name+"()"},{"\""+strings.ToUpper(name)+"\"()",strings.ToUpper(name)+"()"},{name+"_missing()", name+"_missing()"}} {
				want := activityDiagnostic("42883", "function "+tc.signature+" does not exist",8); want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
				activityError(t,p,c,"SELECT "+tc.call,want)
			}
			want := activityDiagnostic("42883", "function "+name+"(integer) does not exist",13); want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
			activityError(t,p,c,"SELECT 'λ', "+name+"(1)",want)
			for _, tc := range []struct{ suffix, message string }{{"(*)",name+"(*) specified, but "+name+" is not an aggregate function"},{"() FILTER (WHERE true)","FILTER specified, but "+name+" is not an aggregate function"},{"() OVER ()","OVER specified, but "+name+" is not a window function nor an aggregate function"}} { activityError(t,p,c,"SELECT "+name+tc.suffix,activityDiagnostic("42809",tc.message,8)) }
			activityError(t,p,c,"SELECT "+name+"(activity_missing_column)",activityDiagnostic("42703", "column \"activity_missing_column\" does not exist",9+len(name)))
		}
		activitySameRuntime(t,before,activityCapture(t,p,b,c))
		query := `SELECT oid::bigint AS oid, proname::text AS name, pronargs::bigint AS nargs,
prorettype::bigint AS result, proisstrict AS strict, provolatile::text AS volatility,
proparallel::text AS parallel, proretset AS is_set, prorows::text AS rows,
procost::text AS cost, proargtypes::text AS inputs, proallargtypes::text AS alltypes,
proargmodes::text AS modes, proargnames::text AS names, pronargdefaults::bigint AS defaults,
prosecdef AS definer, proleakproof AS leakproof, provariadic::bigint AS variadic,
prosupport::oid::bigint AS support, prolang::bigint AS language, prosrc AS source
FROM pg_proc WHERE oid IN (817,2026,2034,2560) ORDER BY oid`
		columns:=[]string{"oid","name","nargs","result","strict","volatility","parallel","is_set","rows","cost","inputs","alltypes","modes","names","defaults","definer","leakproof","variadic","support","language","source"}
		types:=[]string{"INT8","TEXT","INT8","INT8","BOOL","TEXT","TEXT","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT","INT8","BOOL","BOOL","INT8","INT8","INT8","TEXT"}
		var rows [][]any
		for _, tc := range []struct{ oid, result int64; name string; strict bool; volatility, parallel string }{{817,25,"current_query",false,"v","r"},{2026,23,"pg_backend_pid",true,"s","r"},{2034,1184,"pg_conf_load_time",true,"s","r"},{2560,1184,"pg_postmaster_start_time",true,"s","s"}} {
			rows=append(rows,[]any{tc.oid,tc.name,int64(0),tc.result,tc.strict,tc.volatility,tc.parallel,false,"0","1","",nil,nil,nil,int64(0),false,false,int64(0),int64(0),int64(12),tc.name})
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select(query,columns,types,rows))
	})
}

func TestReferenceSessionActivityVisibilityAndPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"activity_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		activityExec(t,p,c,"CREATE ROLE activity_reader")
		defer activityExec(t,p,c,"RESET ROLE")
		for _, name := range activityNames {
			query:="SELECT pg_catalog."+name+"()"
			stmt,err:=c.PrepareContext(p.Ctx,query); if err!=nil { t.Fatal(err) }
			defer stmt.Close()
			activityExec(t,p,c,"REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"() FROM PUBLIC")
			defer func() { activityExec(t,p,c,"RESET ROLE"); activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION pg_catalog."+name+"() TO PUBLIC") }()
			activityExec(t,p,c,"SET ROLE activity_reader")
			want:=activityDiagnostic("42501","permission denied for function "+name,0)
			rows,err:=c.QueryContext(p.Ctx,query); if rows!=nil { rows.Close(); t.Fatal("denied rows") }; fixture.AssertDiagnostic(t,err,&want)
			rows,err=stmt.QueryContext(p.Ctx); if rows!=nil { rows.Close(); t.Fatal("denied prepared rows") }; fixture.AssertDiagnostic(t,err,&want)
			activityExec(t,p,c,"RESET ROLE")
			activityExec(t,p,c,"GRANT EXECUTE ON FUNCTION pg_catalog."+name+"() TO PUBLIC")
			activityExec(t,p,c,"SET ROLE activity_reader")
			rows,err=stmt.QueryContext(p.Ctx); if err!=nil { t.Fatal(err) }; if !rows.Next() { t.Fatalf("grant no row %v",rows.Err()) }; rows.Close(); stmt.Close()
			activityExec(t,p,c,"RESET ROLE")
		}
		heldSQL:="SELECT current_query()"
		held,err:=c.PrepareContext(p.Ctx,heldSQL);if err!=nil { t.Fatal(err) };defer held.Close()
		activityExec(t,p,c,"CREATE FUNCTION public.current_query() RETURNS text LANGUAGE SQL AS 'SELECT ''shadow''::text'")
		activityExec(t,p,c,"SET search_path = public, pg_catalog")
		var value string
		if err:=c.QueryRowContext(p.Ctx,"SELECT current_query()").Scan(&value); err!=nil || value!="shadow" { t.Fatalf("shadow %q %v",value,err) }
		if err:=held.QueryRowContext(p.Ctx).Scan(&value);err!=nil || value!="shadow" { t.Fatalf("prepared shadow %q %v",value,err) }
		activityExec(t,p,c,"SET search_path = pg_catalog, public")
		if err:=held.QueryRowContext(p.Ctx).Scan(&value);err!=nil || value!=heldSQL { t.Fatalf("prepared live resolution %q %v",value,err) }
		query:="SELECT pg_catalog.current_query()"
		if err:=c.QueryRowContext(p.Ctx,query).Scan(&value); err!=nil || value!=query { t.Fatalf("qualified %q %v",value,err) }
	})
}
