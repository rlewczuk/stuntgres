package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceStatsResetReplicationDiagnostics(t *testing.T) {
 activityScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  resetReplicationRuntimeError(t,p,b,c,"SELECT pg_stat_reset_replication_slot('reset_missing_slot')",activityDiagnostic("22023",`replication slot "reset_missing_slot" does not exist`,0))
  resetReplicationRuntimeError(t,p,b,c,"SELECT pg_stat_reset_replication_slot('')",activityDiagnostic("22023",`replication slot "" does not exist`,0))
  resetReplicationRuntimeError(t,p,b,c,"SELECT pg_stat_reset_replication_slot('Invalid slot/name')",activityDiagnostic("22023",`replication slot "Invalid slot/name" does not exist`,0))
  resetReplicationRuntimeError(t,p,b,c,"SELECT pg_stat_reset_subscription_stats(0::oid)",activityDiagnostic("22023","invalid subscription OID 0",0))
  for _,f:=range resetReplicationFunctions {
   failures:=[]struct{suffix,signature string}{{"()",""},{"(NULL,NULL)","unknown, unknown"},{"(true)","boolean"},{"(x => NULL)","x => unknown"}}
   if f.argument=="oid" { failures=append(failures,struct{suffix,signature string}{"('bad'::text)","text"}) }
   for _,tc:=range failures {
    want:=activityDiagnostic("42883","function "+f.name+"("+tc.signature+") does not exist",8)
    want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
    activityError(t,p,c,"SELECT "+f.name+tc.suffix,want)
   }
   for _,call:=range []struct{call,signature string}{{"public."+f.name+"(NULL)","public."+f.name+"(unknown)"},{"\""+strings.ToUpper(f.name)+"\"(NULL)",strings.ToUpper(f.name)+"(unknown)"},{f.name+"_missing(NULL)",f.name+"_missing(unknown)"}} {
    want:=activityDiagnostic("42883","function "+call.signature+" does not exist",8); want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
    activityError(t,p,c,"SELECT "+call.call,want)
   }
   want:=activityDiagnostic("42883","function "+f.name+"(boolean) does not exist",14); want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
   activityError(t,p,c,"/*é*/ SELECT "+f.name+"(true)",want)
   activityError(t,p,c,"SELECT "+f.name+"(reset_missing_column)",activityDiagnostic("42703",`column "reset_missing_column" does not exist`,9+len(f.name)))
   activityError(t,p,c,"SELECT reset_missing_schema."+f.name+"(NULL)",activityDiagnostic("3F000",`schema "reset_missing_schema" does not exist`,8))
   activityError(t,p,c,"SELECT "+f.name+"(1::reset_missing_type)",activityDiagnostic("42704",`type "reset_missing_type" does not exist`,12+len(f.name)))
   for _,decoration:=range []struct{suffix,message string}{
    {"(DISTINCT NULL::"+f.argument+")","DISTINCT specified, but "+f.name+" is not an aggregate function"},
    {"(NULL::"+f.argument+" ORDER BY 1)","ORDER BY specified, but "+f.name+" is not an aggregate function"},
    {"(NULL::"+f.argument+") FILTER (WHERE true)","FILTER specified, but "+f.name+" is not an aggregate function"},
    {"(NULL::"+f.argument+") OVER ()","OVER specified, but "+f.name+" is not a window function nor an aggregate function"},
   } { activityError(t,p,c,"SELECT "+f.name+decoration.suffix,activityDiagnostic("42809",decoration.message,8)) }
   activityError(t,p,c,"SELECT "+f.name+"("+strings.Repeat("NULL,",100)+"NULL)",activityDiagnostic("54023","cannot pass more than 100 arguments to a function",8))
   for _,syntax:=range []struct{arguments,token,message string}{{"x=>NULL,x=>NULL","x=>NULL)",`argument name "x" used more than once`},{"x=>NULL,NULL","NULL)","positional argument cannot follow named argument"}} {
    query:="SELECT "+f.name+"("+syntax.arguments+")"
    activityError(t,p,c,query,activityDiagnostic("42601",syntax.message,strings.Index(query,syntax.token)+1))
   }
  }
  // Unknown literals use the ordinary OID input codec before invocation.
  activityError(t,p,c,"SELECT pg_stat_reset_subscription_stats('bad')",activityDiagnostic("22P02",`invalid input syntax for type oid: "bad"`,41))
 })
}
