package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionPrivileges(t *testing.T) {
 for _,tc:=range []struct{name,prepare,message string}{
  {"type_owner","","must be owner of type hstore"},
  {"from_owner","ALTER TYPE hstore OWNER TO td_actor","must be owner of function td_a_from"},
  {"to_owner","ALTER TYPE hstore OWNER TO td_actor; ALTER FUNCTION td_a_from(internal) OWNER TO td_actor","must be owner of function td_b_to"},
 } {
  t.Run(tc.name,func(t *testing.T) {
   fixture.RoleReferenceCleanup(t,[]string{"td_actor"})
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    hostExec(t,p,b,c,"CREATE ROLE td_actor NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT")
    hostExec(t,p,b,c,"GRANT USAGE,CREATE ON SCHEMA public TO td_actor")
    if tc.prepare!="" {
     // Keep each owner mutation an actual statement, not multi-command protocol.
     hostExec(t,p,b,c,"ALTER TYPE hstore OWNER TO td_actor")
     if tc.name=="to_owner" { hostExec(t,p,b,c,"ALTER FUNCTION td_a_from(internal) OWNER TO td_actor") }
    }
    hostExec(t,p,b,c,"SET ROLE td_actor")
    definitionDiagnostic(t,p,c,definitionCreate("td_a_from","td_b_to",false),fixture.ControlsReferenceDiagnostic("42501",tc.message),"execute")
    hostExec(t,p,b,c,"RESET ROLE")
    definitionAbsent(t,p,b,c)
   })
  })
 }
 t.Run("volatility_owner",func(t *testing.T) {
  fixture.RoleReferenceCleanup(t,[]string{"td_actor"})
  definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
   hostExec(t,p,b,c,"CREATE ROLE td_actor NOSUPERUSER NOINHERIT")
   hostExec(t,p,b,c,"GRANT USAGE ON SCHEMA public TO td_actor")
   hostRows(t,p,b,c,"SELECT has_schema_privilege('td_actor','public','USAGE') AS accessible",[]string{"accessible"},[]string{"BOOL"},[][]any{{true}})
   hostExec(t,p,b,c,"SET ROLE td_actor")
   definitionDiagnostic(t,p,c,"ALTER FUNCTION td_a_from(internal) STABLE",fixture.ControlsReferenceDiagnostic("42501","must be owner of function td_a_from"),"execute")
   hostExec(t,p,b,c,"RESET ROLE")
   expected:="v"; if b.PG { expected="i" }
   hostRows(t,p,b,c,"SELECT provolatile::text AS v FROM pg_proc WHERE proname='td_a_from'",[]string{"v"},[]string{"TEXT"},[][]any{{expected}})
  })
 })
}

func TestReferenceRoutineTransformDefinitionDependencies(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
  definitionFunction(t,p,b,c,"td_dependent","selection-v1",transformsSelectionPython)
  hostExec(t,p,b,c,"CREATE VIEW td_dependent_view AS SELECT td_dependent('a=>7,b=>NULL'::hstore) AS v")
  hostExec(t,p,b,c,"CREATE TABLE td_sentinel(v integer)")
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_sentinel VALUES (11)",1))
  want:=fixture.ControlsReferenceDiagnostic("2BP01","cannot drop transform for hstore language td_lang because other objects depend on it")
  want.Detail="function td_dependent(hstore) depends on transform for hstore language td_lang\nview td_dependent_view depends on function td_dependent(hstore)"
  want.Hint="Use DROP ... CASCADE to drop the dependent objects too."
  definitionDiagnostic(t,p,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang",want,"execute")
  definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  hostBag(t,p,b,c,"(SELECT v FROM td_dependent_view)",transformsMapRows)
  hostExec(t,p,b,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang CASCADE")
  definitionAbsent(t,p,b,c)
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_proc WHERE proname='td_dependent'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_class WHERE relname='td_dependent_view'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}})
  hostRows(t,p,b,c,"SELECT v FROM td_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
  hostRows(t,p,b,c,"SELECT count(*) AS n FROM pg_proc WHERE proname IN ('td_a_from','td_b_to')",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}})
  definitionDiagnostic(t,p,c,"DROP TRANSFORM FOR hstore LANGUAGE td_lang",fixture.ControlsReferenceDiagnostic("42704","transform for type hstore language \"td_lang\" does not exist"),"execute")
  var notices []fixture.Diagnostic
  _,noticeConn,record:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
  for _,prepared:=range []bool{false,true} {
   notices=nil
   step:=fixture.Exec("DROP TRANSFORM IF EXISTS FOR hstore LANGUAGE td_lang",0)
   if prepared { step=fixture.Prepared(step) }
   fixture.RunStep(t,p.Ctx,noticeConn,record,step)
   expected:=fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"transform for type hstore language \"td_lang\" does not exist, skipping"}
   if len(notices)!=1 { t.Errorf("prepared=%v notices=%#v want one complete=%#v",prepared,notices,expected) } else if notices[0]!=expected { t.Errorf("prepared=%v notice=%#v want complete=%#v",prepared,notices[0],expected) }
  }
 })
}

func TestReferenceRoutineTransformDefinitionAtomicity(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  hostExec(t,p,b,c,definitionCreate("td_a_from","td_a_to",false))
  oid:=definitionPair(t,p,b,c,"td_a_from","td_a_to")
  definitionFunction(t,p,b,c,"td_atomic","selection-v1",transformsSelectionPython)
  q:="SELECT key,value FROM each(td_atomic($1::hstore)) ORDER BY key"
  held:=transformsHeld(t,p,c,q)
  hostExec(t,p,b,c,"BEGIN")
  hostExec(t,p,b,c,definitionCreate("td_b_from","td_b_to",true))
  definitionPair(t,p,b,c,"td_b_from","td_b_to")
  hostExec(t,p,b,c,"ROLLBACK")
  if current:=definitionPair(t,p,b,c,"td_a_from","td_a_to"); current!=oid { t.Errorf("rollback changed OID") }
  definitionEdges(t,p,b,c,oid,"td_a_from","td_a_to")
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
  hostExec(t,p,b,c,"BEGIN")
  hostExec(t,p,b,c,"SAVEPOINT td_save")
  _,err:=c.ExecContext(p.Ctx,definitionCreate("td_b_from","td_a_from",true))
  fixture.AssertDiagnostic(t,err,func()*fixture.Diagnostic { d:=fixture.ControlsReferenceDiagnostic("42P17","return data type of TO SQL function must be the transform data type"); return &d }())
  hostExec(t,p,b,c,"ROLLBACK TO SAVEPOINT td_save")
  definitionPair(t,p,b,c,"td_a_from","td_a_to")
  hostExec(t,p,b,c,definitionCreate("td_b_from","td_b_to",true))
  hostExec(t,p,b,c,"COMMIT")
  definitionEdges(t,p,b,c,oid,"td_b_from","td_b_to")
  hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
 })
}

func TestReferenceRoutineTransformDefinitionBoundary(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  // A real non-host language may retain genuine callback metadata. This is
  // neither an external ABI invocation nor a fabricated callable SQL stub.
  hostExec(t,p,b,c,"CREATE TRANSFORM FOR hstore LANGUAGE sql (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal), TO SQL WITH FUNCTION td_b_to(internal))")
  hostRows(t,p,b,c,"SELECT pf.proname AS f,pt.proname AS t FROM pg_transform x JOIN pg_language l ON l.oid=x.trflang JOIN pg_proc pf ON pf.oid=x.trffromsql JOIN pg_proc pt ON pt.oid=x.trftosql WHERE l.lanname='sql' AND x.trftype='hstore'::regtype",[]string{"f","t"},[]string{"NAME","NAME"},[][]any{{"dsimple_init","td_b_to"}})
  // The genuine immutable simple-dictionary initializer has the exact declared
  // signature, but is NOT a language converter. Never invoke its INTERNAL ABI.
  hostExec(t,p,b,c,definitionCreate("pg_catalog.dsimple_init","td_b_to",false))
  oid:=definitionPair(t,p,b,c,"dsimple_init","td_b_to")
  // Pinned builtin callbacks remain explicit catalog identities, but PG does
  // not record normal pg_depend edges to pinned objects (root tests-5).
  definitionEdges(t,p,b,c,oid,"","td_b_to")
  if !b.PG {
   q:="CREATE FUNCTION td_boundary(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS 'selection-v1'"
   hostNativeError(t,p.Ctx,c,q,"unavailable host FROM SQL direction")
   definitionPair(t,p,b,c,"dsimple_init","td_b_to")
  }
 })
}
