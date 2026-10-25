package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "unicode/utf8"

 "stuntgres/reference/fixture"
)

func TestReferenceCitextStringFilterDiagnostics(t *testing.T) {
 for _,call:=range []struct{name,valid,invalid,types string}{
  {"strpos","'a'::citext,'a'","'a'::citext,1","citext, integer"},
  {"replace","'a'::citext,'a','X'","'a'::citext,'a',1","citext, unknown, integer"},
  {"split_part","'a'::citext,',',1","'a'::citext,',',true","citext, unknown, boolean"},
  {"translate","'a'::citext,'a','X'","'a'::citext,1,'X'","citext, integer, unknown"},
 } {
  for _,signature:=range []struct{name,args string;valid bool}{
   {"existing",call.valid,true},{"invalid",call.invalid,false},
  } {
   for _,filter:=range []struct{name,expression,code,message string}{
    {"boolean","true","42809","FILTER specified, but public."+call.name+" is not an aggregate function"},
    {"integer","1","42804","argument of FILTER must be type boolean, not type integer"},
    {"missing_column","missing_filter","42703",`column "missing_filter" does not exist`},
   } {
    t.Run(call.name+"/"+signature.name+"/"+filter.name,func(t *testing.T){
     citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
      citextInstall(t,p,b,c)
      q:="SELECT /*é🙂*/ public."+call.name+"("+signature.args+") FILTER (WHERE "+filter.expression+")"
      want:=fixture.ControlsReferenceDiagnostic(filter.code,filter.message)
      if filter.name=="boolean" {
       want.Position=citextPosition(q,"public.")
       if !signature.valid {
        want.Code="42883"
        want.Message="function public."+call.name+"("+call.types+") does not exist"
        want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
       }
      } else {
       // Locate the FILTER input, not an earlier same-valued argument. The
       // multibyte prefix distinguishes SQL character positions from bytes.
       at:=strings.LastIndex(q,filter.expression)
       want.Position=fixture.Position(utf8.RuneCountInString(q[:at])+1)
      }
      for _,prepared:=range []bool{false,true} {
       citextError(t,p,b,c,q,prepared,true,true,want)
      }
     })
    })
   }
  }
 }
 t.Run("selected_filter_ownership",func(t *testing.T){
  citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   citextInstall(t,p,b,c)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE AGGREGATE public.cs_filter_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",0))
   // Different FILTERs on one selected aggregate remain executable Boolean
   // children, not a proof that every later FILTER is valid or true.
   for _,tc:=range []struct{filter string;want int64}{
    {"x>1",5},{"false",0},{"true",6},{"x>2",3},
   } {
    citextStringRoutes(t,p,b,c,citextStringValue("SELECT public.cs_filter_sum(x) FILTER (WHERE "+tc.filter+") AS v FROM(VALUES(1),(2),(3)) s(x)","INT4",tc.want))
   }
   // ORDER BY binds a nested call while the outer selected FILTER frame is
   // live. The inner aggregate must use y>1, not the outer x>2 expression.
   nested:="SELECT public.cs_filter_sum(x ORDER BY 1/CASE WHEN (SELECT public.cs_filter_sum(y) FILTER (WHERE y>1) FROM(VALUES(1),(2)) t(y))=2 THEN 1 ELSE 0 END) FILTER (WHERE x>2) AS v FROM(VALUES(1),(2),(3)) s(x)"
   citextStringRoutes(t,p,b,c,citextStringValue(nested,"INT4",int64(3)))
   malformed:="SELECT public.cs_filter_sum(x ORDER BY public.strpos('a'::citext,'a') FILTER (WHERE missing_nested_filter)) FILTER (WHERE x>2) FROM(VALUES(1),(2),(3)) s(x)"
   want:=fixture.ControlsReferenceDiagnostic("42703",`column "missing_nested_filter" does not exist`)
   want.Position=citextPosition(malformed,"missing_nested_filter")
   for _,prepared:=range []bool{false,true} {
    citextError(t,p,b,c,malformed,prepared,true,true,want)
   }
   // Error exits must also restore the frame before another scalar call.
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT public.strpos('AbA'::citext,'a') AS v","INT4",int64(1)))
   citextStringRoutes(t,p,b,c,citextStringValue("SELECT public.cs_filter_sum(x) FILTER (WHERE false) AS v FROM(VALUES(1),(2),(3)) s(x)","INT4",int64(0)))
  })
 })
 for _,namespace:=range []struct{name,code,message string;denied bool}{
  {"cs_missing","3F000",`schema "cs_missing" does not exist`,false},
  {"cs_denied","42501","permission denied for schema cs_denied",true},
 } {
  for _,call:=range []struct{name,args string}{
   {"strpos","'a'::citext,'a'"},
   {"replace","'a'::citext,'a','X'"},
   {"split_part","'a'::citext,',',1"},
   {"translate","'a'::citext,'a','X'"},
  } {
   t.Run("namespace_precedence/"+namespace.name+"/"+call.name,func(t *testing.T){
    if namespace.denied {
     fixture.RoleReferenceCleanup(t,[]string{"cs_filter_reader"})
     fixture.SchemaReferenceCleanup(t,namespace.name)
    }
    citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
     citextInstall(t,p,b,c)
     if namespace.denied {
      for _,q:=range []string{
       "CREATE ROLE cs_filter_reader",
       "GRANT USAGE ON SCHEMA public TO cs_filter_reader",
       "CREATE SCHEMA cs_denied",
       "REVOKE ALL ON SCHEMA cs_denied FROM PUBLIC",
       "SET ROLE cs_filter_reader",
      } {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
      defer c.ExecContext(p.Ctx,"RESET ROLE")
     }
     for _,operand:=range []struct{name,first,code,message,token string}{
      {"valid","'a'::citext","","",""},
      {"explicit_bad_cast","'bad'::integer","22P02",`invalid input syntax for type integer: "bad"`,"'bad'"},
      {"missing_column","missing_operand","42703",`column "missing_operand" does not exist`,"missing_operand"},
     } {
      for _,filter:=range []struct{name,expression,code,message string}{
       {"boolean","true","",""},
       {"integer","1","42804","argument of FILTER must be type boolean, not type integer"},
       {"missing_column","missing_filter","42703",`column "missing_filter" does not exist`},
      } {
       t.Run(operand.name+"/"+filter.name,func(t *testing.T){
        args:=strings.Replace(call.args,"'a'::citext",operand.first,1)
        q:="SELECT /*é🙂*/ "+namespace.name+"."+call.name+"("+args+") FILTER (WHERE "+filter.expression+")"
        want:=fixture.ControlsReferenceDiagnostic(namespace.code,namespace.message)
        want.Position=citextPosition(q,namespace.name+".")
        if filter.code!="" {
         want=fixture.ControlsReferenceDiagnostic(filter.code,filter.message)
         at:=strings.LastIndex(q,filter.expression)
         want.Position=fixture.Position(utf8.RuneCountInString(q[:at])+1)
        }
        // Explicit operand transformation precedes both FILTER analysis and
        // the deferred namespace error, even when FILTER itself is invalid.
        if operand.code!="" {
         want=fixture.ControlsReferenceDiagnostic(operand.code,operand.message)
         want.Position=citextPosition(q,operand.token)
        }
        for _,prepared:=range []bool{false,true} {
         citextError(t,p,b,c,q,prepared,true,true,want)
        }
       })
      }
     }
    })
   })
  }
 }
 t.Run("selected_conversion_precedence",func(t *testing.T){
  citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
   citextInstall(t,p,b,c)
   for _,operand:=range []struct{name,index string;explicit bool}{
    {"unknown_literal","'x'",false},{"explicit_bad_cast","'x'::integer",true},
   } {
    for _,decoration:=range []struct{name,sql,code,message,token string}{
     {"plain","","22P02",`invalid input syntax for type integer: "x"`,"'x'"},
     {"boolean_filter"," FILTER (WHERE true)","42809","FILTER specified, but public.split_part is not an aggregate function","public.split_part"},
     {"window"," OVER ()","42809","OVER specified, but public.split_part is not a window function nor an aggregate function","public.split_part"},
     {"integer_filter"," FILTER (WHERE 1)","42804","argument of FILTER must be type boolean, not type integer","WHERE 1"},
     {"missing_column_filter"," FILTER (WHERE missing_filter)","42703",`column "missing_filter" does not exist`,"missing_filter"},
    } {
     t.Run(operand.name+"/"+decoration.name,func(t *testing.T){
      q:="SELECT /*é🙂*/ public.split_part('a'::citext,',',"+operand.index+")"+decoration.sql
      want:=fixture.ControlsReferenceDiagnostic(decoration.code,decoration.message)
      want.Position=citextPosition(q,decoration.token)
      if decoration.name=="integer_filter" {
       want.Position=fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q,"1")])+1)
      }
      // An explicit cast fails while transforming the written operand. An
      // unknown literal is converted only after scalar-kind checks, whereas
      // malformed FILTER inputs fail before that selected-formal conversion.
      if operand.explicit {
       want=fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "x"`)
       want.Position=citextPosition(q,"'x'")
      }
      for _,prepared:=range []bool{false,true} {
       citextError(t,p,b,c,q,prepared,true,true,want)
      }
     })
    }
   }
  })
 })
}
