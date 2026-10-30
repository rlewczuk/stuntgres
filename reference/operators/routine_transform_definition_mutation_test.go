package reference_test

import (
 "context"
 "database/sql"
 "reflect"
 "regexp"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionMutationCancellation(t *testing.T) {
 for _,mutation:=range []string{"replace","drop"} {
  t.Run(mutation,func(t *testing.T){
   definitionScenarioRuntime(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *transformRuntime){
    definitionAnnotate(t,p,b,c)
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    oid:=definitionPair(t,p,b,c,"td_a_from","td_b_to")
    program:="selection-v1";if !b.PG {program="wait-v1"}
    definitionFunction(t,p,b,c,"td_mutation_wait",program,transformsSelectionPython)
    q:="SELECT key,value FROM each(td_mutation_wait($1::hstore)) ORDER BY key"
    held:=transformsHeld(t,p,c,q)
    holder,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer holder.Close()
    waiter,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer waiter.Close()
    // Native callback admission is real and deliberately independent of the
    // transaction definition lock. PostgreSQL's shared arm proves its actual
    // catalog transaction wait without pretending to execute a Go callback.
    var release chan struct{}
    var done chan fixture.RelationOutcome
    var cancel context.CancelFunc
    beforeBody:=r.body.Load()
    joined:=false
    if !b.PG {
     entered:=make(chan struct{});release=make(chan struct{});done=make(chan fixture.RelationOutcome,1)
     r.mu.Lock();r.gate=func(ctx context.Context)error{close(entered);select{case <-release:return nil;case <-ctx.Done():return ctx.Err()}};r.mu.Unlock()
     active,stop:=context.WithCancel(p.Ctx);cancel=stop
     defer func(){stop();if !joined {outcome:=<-done;if outcome.Rows!=nil {outcome.Rows.Close()}};r.mu.Lock();r.gate=nil;r.mu.Unlock()}()
     go func(){rows,err:=held.QueryContext(active,q,"a=>7,b=>NULL");done<-fixture.RelationOutcome{Rows:rows,Err:err}}()
     select{case <-entered:case <-p.Ctx.Done():t.Fatal(p.Ctx.Err())}
    }
    hostExec(t,p,b,holder,"BEGIN")
    defer routineLanguagesCleanup(t,holder,"ROLLBACK")()
    hostExec(t,p,b,holder,definitionCreate("td_b_from","td_a_to",true))
    statement:=definitionCreate("td_b_from","td_b_to",true)
    if mutation=="drop" {statement="DROP TRANSFORM FOR hstore LANGUAGE td_lang CASCADE"}
    diagnostic:=func(t *testing.T,err error){
     action:="updating";if mutation=="drop" {action="deleting"}
     got:=fixture.ExtractDiagnostic(t,err)
     if !regexp.MustCompile(`^while `+action+` tuple \([0-9]+,[1-9][0-9]*\) in relation "pg_transform"$`).MatchString(got.Context) {t.Fatalf("transform mutation physical Context=%q",got.Context)}
     want:=fixture.ControlsReferenceDiagnostic("57014","canceling statement due to user request")
     want.Context=got.Context // Only independently validated heap coordinates vary.
     fixture.AssertDiagnostic(t,err,&want)
    }
    fixture.DefinitionActiveCancel(t,p,b,waiter,holder,statement,"transactionid","ShareLock",0,0,"",nil,diagnostic)
    hostExec(t,p,b,holder,"ROLLBACK")
    if !b.PG {
     close(release)
     outcome:=<-done;joined=true
     if outcome.Err!=nil {t.Fatal(outcome.Err)}
     rows:=outcome.Rows;if rows==nil {t.Fatal("admitted callback lost result")}
     columns,err:=rows.Columns();if err!=nil {rows.Close();t.Fatal(err)}
     if !reflect.DeepEqual(columns,[]string{"key","value"}) {t.Errorf("admitted columns=%v",columns)}
     var values [][]any
     for rows.Next(){var key string;var value sql.NullString;if err=rows.Scan(&key,&value);err!=nil{rows.Close();t.Fatal(err)};var text any;if value.Valid{text=value.String};values=append(values,[]any{key,text})}
     if err=rows.Err();err!=nil{t.Error(err)};rows.Close()
     cancel()
     if !reflect.DeepEqual(values,transformsMapRows) {t.Fatalf("admitted old callback output=%#v",values)}
     if r.body.Load()!=beforeBody+1 {t.Fatal("callback retried during mutation cancellation")}
     r.mu.Lock();r.gate=nil;r.mu.Unlock()
    }
    if current:=definitionPair(t,p,b,waiter,"td_a_from","td_b_to");current!=oid {t.Fatal("canceled mutation changed pair identity")}
    definitionEdges(t,p,b,waiter,oid,"td_a_from","td_b_to")
    hostExec(t,p,b,c,"CREATE OR REPLACE FUNCTION td_mutation_wait(v hstore) RETURNS hstore LANGUAGE td_lang TRANSFORM FOR TYPE hstore AS "+transformsLiteral(transformsSource(b,"selection-v1",transformsSelectionPython)))
    hostRows(t,p,b,held,q,[]string{"key","value"},[]string{"TEXT","TEXT"},transformsMapRows,"a=>7,b=>NULL")
   })
  })
 }
}
