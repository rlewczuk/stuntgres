package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "fmt"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionConcurrency(t *testing.T) {
 definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  definitionAnnotate(t,p,b,c)
  first,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer first.Close()
  second,err:=b.DB.Conn(p.Ctx); if err!=nil { t.Fatal(err) }; defer second.Close()
  // Read independent descriptor identities before either CREATE. Generated
  // allocator OIDs belong in the exact diagnostic, never a pinned golden.
  var typeOID,languageOID int64
  if err=c.QueryRowContext(p.Ctx,"SELECT 'hstore'::regtype::oid::bigint,(SELECT oid::bigint FROM pg_language WHERE lanname='td_lang')").Scan(&typeOID,&languageOID); err!=nil { t.Fatal(err) }
  hostExec(t,p,b,first,"BEGIN")
  defer routineLanguagesCleanup(t,first,"ROLLBACK")()
  q:=definitionCreate("td_a_from","td_b_to",false)
  hostExec(t,p,b,first,q)
  // The first real definition is deliberately uncommitted and invisible to
  // the second backend. Admit the second CREATE into the actual unique-index
  // transaction wait before committing, instead of racing goroutine launch.
  definitionAbsent(t,p,b,c)
  join:=fixture.DefinitionLockOperation(t,p.Ctx,p,b,second,first,q,"transactionid","ShareLock",0,0)
  hostExec(t,p,b,first,"COMMIT")
  outcome:=join()
  if outcome.Rows!=nil { outcome.Rows.Close(); t.Error("failed CREATE returned rows") }
  if outcome.Stmt!=nil { outcome.Stmt.Close() }
  if outcome.Result!=nil { t.Error("failed CREATE returned a result") }
  // Authentic admitted concurrent insertion is23505, not a serial42710
  // alternate. The same logical unique-name claim is required natively.
  want:=fixture.ControlsReferenceDiagnostic("23505","duplicate key value violates unique constraint \"pg_transform_type_lang_index\"")
  want.Detail=fmt.Sprintf("Key (trftype, trflang)=(%d, %d) already exists.",typeOID,languageOID)
  want.Schema,want.Table,want.Constraint="pg_catalog","pg_transform","pg_transform_type_lang_index"
  fixture.AssertDiagnostic(t,outcome.Err,&want)
  oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
  definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  if !b.PG {
   before:=transformsDump(t,p.Ctx,p.Native)
   canceled,stop:=context.WithCancel(p.Ctx); stop()
   for _,query:=range []string{definitionCreate("td_b_from","td_b_to",true),"DROP TRANSFORM FOR hstore LANGUAGE td_lang"} {
    _,err:=c.ExecContext(canceled,query)
    if !errors.Is(err,context.Canceled) { t.Errorf("pre-canceled %q=%v",query,err) }
   }
   after:=transformsDump(t,p.Ctx,p.Native)
   if string(before)!=string(after) { t.Fatal("pre-canceled DDL published state") }
   definitionEdges(t,p,b,c,oid,"td_a_from","td_b_to")
  }
 })
}
