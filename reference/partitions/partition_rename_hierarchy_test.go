package reference_test

import (
 "database/sql"
 "reflect"
 "testing"

 "stuntgres/reference/fixture"
)

func partitionRenameSetup(run func(fixture.Step)) {
 for _, q := range []string{
  "CREATE TABLE pr_root(k INT,bucket INT,payload TEXT) PARTITION BY RANGE(k)",
  "CREATE TABLE pr_mid PARTITION OF pr_root FOR VALUES FROM(0) TO(10) PARTITION BY LIST(bucket)",
  "CREATE TABLE pr_hash PARTITION OF pr_mid FOR VALUES IN(0,1) PARTITION BY HASH(k)",
  "CREATE TABLE pr_h0 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
  "CREATE TABLE pr_h1 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 2,REMAINDER 1)",
 } { run(fixture.Exec(q,0)) }
 run(fixture.Exec("INSERT INTO pr_root VALUES(1,0,'one'),(2,1,'two'),(3,0,'three')",3))
}

func partitionRenameRows(run func(fixture.Step), key, payload string) {
 names, types := []string{key,"bucket",payload}, []string{"INT4","INT4","TEXT"}
 rows := [][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"},{int64(3),int64(0),"three"}}
 for _, table := range []string{"pr_root","pr_mid","pr_hash","pr_h0","pr_h1"} {
  want := rows
  if table=="pr_h0" { want=rows[:2] }; if table=="pr_h1" { want=rows[2:] }
  for _, prepared := range []bool{false,true} {
   s:=fixture.Select("SELECT * FROM "+table+" ORDER BY 1",names,types,want); s.Prepared=prepared; run(s)
  }
 }
 run(fixture.Select("SELECT * FROM ONLY pr_root",names,types,nil))
}

func TestReferencePartitionRenameColumnHierarchy(t *testing.T) {
 referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  c,err:=b.DB.Conn(p.Ctx); if err!=nil {t.Fatal(err)}; defer c.Close()
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  partitionRenameSetup(run)
  identitySQL := "SELECT c.oid,a.attnum,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN('pr_root','pr_mid','pr_hash','pr_h0','pr_h1') AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.oid,a.attnum"
  capture:=func() [][]any {
   rows,e:=c.QueryContext(p.Ctx,identitySQL); if e!=nil {t.Fatal(e)}; defer rows.Close()
   var result [][]any
   for rows.Next(){var oid any; var n int64; var name string; if e:=rows.Scan(&oid,&n,&name);e!=nil{t.Fatal(e)};result=append(result,[]any{fixture.Normalize(oid),n,name})};if e:=rows.Err();e!=nil{t.Fatal(e)};return result
  }
  before:=capture(); partitionRenameRows(run,"k","payload")
  q:="ALTER TABLE pr_root RENAME COLUMN k TO renamed"
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
  fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,fixture.Exec(q,0))
  after:=capture();for _,r:=range before{if r[2]=="k"{r[2]="renamed"}};if !reflect.DeepEqual(before,after){t.Fatal("relation/attribute identities changed",before,after)}
  partitionRenameRows(run,"renamed","payload")
  run(fixture.Exec("ALTER TABLE pr_root * RENAME payload TO label",0))
  partitionRenameRows(run,"renamed","label")
  run(fixture.Prepared(fixture.Select("INSERT INTO pr_root VALUES(4,1,'four') RETURNING renamed,bucket,label",[]string{"renamed","bucket","label"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(4),int64(1),"four"}})))
  run(fixture.Exec("INSERT INTO pr_h1(renamed,bucket,label) VALUES(5,0,'five')",1))
  run(fixture.Select("SELECT renamed FROM pr_h1 ORDER BY renamed",[]string{"renamed"},[]string{"INT4"},[][]any{{int64(3)},{int64(4)},{int64(5)}}))
  run(referenceRangeMiss("INSERT INTO pr_root VALUES(20,0,'bad')","pr_root","renamed","20"))
  run(referenceRangeError("ALTER TABLE pr_root RENAME renamed TO bucket","42701",`column "bucket" of relation "pr_mid" already exists`,fixture.Diagnostic{}))
  run(referenceRangeError("ALTER TABLE ONLY pr_root RENAME missing TO x","42P16",`inherited column "missing" must be renamed in child tables too`,fixture.Diagnostic{}))
  run(referenceRangeError("ALTER TABLE pr_h0 RENAME renamed TO x","42P16",`cannot rename inherited column "renamed"`,fixture.Diagnostic{}))
  run(fixture.Select("SELECT * FROM pr_root ORDER BY renamed",[]string{"renamed","bucket","label"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"},{int64(3),int64(0),"three"},{int64(4),int64(1),"four"},{int64(5),int64(0),"five"}}))
  run(fixture.Exec("CREATE TABLE pr_empty(k INT) PARTITION BY RANGE(k)",0))
  run(fixture.Exec("ALTER TABLE ONLY pr_empty RENAME k TO renamed",0))
  run(fixture.Select("SELECT * FROM pr_empty",[]string{"renamed"},[]string{"INT4"},nil))
 })
}

func partitionRenameAttached(run func(fixture.Step)) {
 for _,q:=range []string{"CREATE TABLE pr_att(k INT,bucket INT,payload TEXT) PARTITION BY RANGE(k)","CREATE TABLE pr_child(obsolete INT,payload TEXT,bucket INT,k INT)","ALTER TABLE pr_child DROP obsolete","ALTER TABLE pr_att ATTACH PARTITION pr_child FOR VALUES FROM(0) TO(10)"}{run(fixture.Exec(q,0))}
 run(fixture.Exec("INSERT INTO pr_att VALUES(1,0,'one')",1))
 run(fixture.Exec(`ALTER TABLE pr_att RENAME k TO "New Key"`,0))
 run(fixture.Select("SELECT * FROM pr_att",[]string{"New Key","bucket","payload"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(0),"one"}}))
 run(fixture.Select("SELECT * FROM pr_child",[]string{"payload","bucket","New Key"},[]string{"TEXT","INT4","INT4"},[][]any{{"one",int64(0),int64(1)}}))
 run(fixture.Exec(`INSERT INTO pr_att("New Key",bucket,payload) VALUES(2,1,'two')`,1))
 run(fixture.Prepared(fixture.Exec(`INSERT INTO pr_child(payload,bucket,"New Key") VALUES('three',1,3)`,1)))
 run(fixture.Select(`SELECT * FROM pr_att ORDER BY "New Key"`,[]string{"New Key","bucket","payload"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"},{int64(3),int64(1),"three"}}))
 run(referenceRangeMiss(`INSERT INTO pr_att VALUES(20,0,'bad')`,"pr_att",`"New Key"`,"20"))
}

func TestReferencePartitionRenameColumnAttachedLayout(t *testing.T){referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){partitionRenameAttached(func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)})})}

// Keep held statements on one physical session; preparing a utility has no effect.
func partitionRenamePrepare(t *testing.T,p *fixture.Pair,c *sql.Conn,q string)*sql.Stmt{t.Helper();s,e:=c.PrepareContext(p.Ctx,q);if e!=nil{t.Fatal(e)};t.Cleanup(func(){s.Close()});return s}
