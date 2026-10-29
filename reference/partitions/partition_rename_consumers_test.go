package reference_test

import (
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePartitionRenameColumnPrepared(t *testing.T){referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
 c,e:=b.DB.Conn(p.Ctx);if e!=nil{t.Fatal(e)};defer c.Close()
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)};partitionRenameSetup(run)
 queries:=[]string{"SELECT * FROM pr_root ORDER BY 1","SELECT k FROM pr_root ORDER BY k","SELECT payload FROM pr_root ORDER BY payload","INSERT INTO pr_root(k,bucket,payload) VALUES($1,$2,$3)","INSERT INTO pr_root VALUES($1,$2,$3)"}
 star:=partitionRenamePrepare(t,p,c,queries[0]);named:=partitionRenamePrepare(t,p,c,queries[1]);other:=partitionRenamePrepare(t,p,c,queries[2]);target:=partitionRenamePrepare(t,p,c,queries[3]);positional:=partitionRenamePrepare(t,p,c,queries[4])
 fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,star,queries[0]},b.Recorder,fixture.Select(queries[0],[]string{"k","bucket","payload"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"},{int64(3),int64(0),"three"}}))
 fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,named,queries[1]},b.Recorder,fixture.Select(queries[1],[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
 fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,other,queries[2]},b.Recorder,fixture.Select(queries[2],[]string{"payload"},[]string{"TEXT"},[][]any{{"one"},{"three"},{"two"}}))
 var heldColumns []string
 var heldRows [][]any
 if !b.PG{
  rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM pr_h0 ORDER BY k");if err!=nil{t.Fatal(err)};defer rows.Close()
  heldColumns,err=rows.Columns();if err!=nil{t.Fatal(err)}
  // Native eager rows remain usable after schema publication on another borrower.
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER TABLE pr_root RENAME k TO renamed",0))
  for rows.Next(){var k,bucket int64;var payload string;if err:=rows.Scan(&k,&bucket,&payload);err!=nil{t.Fatal(err)};heldRows=append(heldRows,[]any{k,bucket,payload})};if err:=rows.Err();err!=nil{t.Fatal(err)}
  if !reflect.DeepEqual(heldColumns,[]string{"k","bucket","payload"})||!reflect.DeepEqual(heldRows,[][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"}}){t.Fatal("retained native reader mutated",heldColumns,heldRows)}
 }else{run(fixture.Exec("ALTER TABLE pr_root RENAME k TO renamed",0))}
 for _,tc:=range []struct{q,code,msg,pos string}{{queries[0],"0A000","cached plan must not change result type",""},{queries[1],"42703",`column "k" does not exist`,"8"}}{
  stmt:=star;if tc.q==queries[1]{stmt=named};s:=referenceRangeError(tc.q,tc.code,tc.msg,fixture.Diagnostic{Position:tc.pos});s.SelectRows=true;fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,stmt,tc.q},b.Recorder,s)
 }
 s:=referenceRangeError(queries[3],"42703",`column "k" of relation "pr_root" does not exist`,fixture.Diagnostic{Position:referenceRangePosition(queries[3],"k,")});s.Args=[]any{4,0,"bad"};fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,target,queries[3]},b.Recorder,s)
 fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,other,queries[2]},b.Recorder,fixture.Select(queries[2],[]string{"payload"},[]string{"TEXT"},[][]any{{"one"},{"three"},{"two"}}))
 fixture.RunStep(t,p.Ctx,fixture.HeldStatement{c,positional,queries[4]},b.Recorder,fixture.Exec(queries[4],1,4,1,"four"))
 run(fixture.Prepared(fixture.Exec("INSERT INTO pr_root(renamed,bucket,payload) VALUES($1,$2,$3)",1,5,0,"five")))
 run(fixture.Prepared(fixture.Select("SELECT renamed FROM pr_root ORDER BY renamed",[]string{"renamed"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)},{int64(5)}})))
})}

func partitionRenameExpressions(run func(fixture.Step)){
 for _,q:=range []string{"CREATE TABLE pr_expr(k INT CONSTRAINT pr_positive CHECK(k>0) CONSTRAINT pr_upper CHECK(k<3),payload TEXT DEFAULT 'default') PARTITION BY RANGE((k+1))","CREATE TABLE pr_expr_leaf PARTITION OF pr_expr FOR VALUES FROM(2) TO(5)","CREATE INDEX pr_expr_idx ON pr_expr_leaf((k+10))","CREATE VIEW pr_view AS SELECT k AS original,k+10 AS calculated FROM pr_expr"}{run(fixture.Exec(q,0))}
 run(fixture.Exec("INSERT INTO pr_expr(k) VALUES(1)",1));run(fixture.Exec(`ALTER TABLE pr_expr RENAME k TO "New Key"`,0))
 run(fixture.Prepared(fixture.Exec(`INSERT INTO pr_expr("New Key") VALUES(2)`,1)))
 run(fixture.Select(`SELECT * FROM pr_expr ORDER BY "New Key"`,[]string{"New Key","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"default"},{int64(2),"default"}}))
 run(fixture.Select("SELECT * FROM pr_view ORDER BY original",[]string{"original","calculated"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(11)},{int64(2),int64(12)}}))
 run(fixture.Select(`SELECT "New Key"+10 AS indexed FROM pr_expr_leaf WHERE "New Key"+10=12`,[]string{"indexed"},[]string{"INT4"},[][]any{{int64(12)}}))
 run(referenceRangeError(`INSERT INTO pr_expr("New Key") VALUES(3)`,"23514",`new row for relation "pr_expr_leaf" violates check constraint "pr_upper"`,fixture.Diagnostic{Detail:"Failing row contains (3, default).",Schema:"public",Table:"pr_expr_leaf",Constraint:"pr_upper"}))
 run(referenceRangeMiss(`INSERT INTO pr_expr("New Key") VALUES(20)`,"pr_expr",`("New Key" + 1)`,"21"))
}
func TestReferencePartitionRenameColumnExpressions(t *testing.T){referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){partitionRenameExpressions(func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)})})}
