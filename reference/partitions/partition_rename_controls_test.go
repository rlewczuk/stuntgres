package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func partitionRenameControls(t *testing.T){
 p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
  partitionRenameSetup(run);run(fixture.Exec("ALTER TABLE pr_root RENAME k TO renamed",0));partitionRenameRows(run,"renamed","payload")
  partitionRenameAttached(run);partitionRenameExpressions(run)
  run(fixture.Exec(`COMMENT ON COLUMN pr_att."New Key" IS 'stable key'`,0))
 })}
 t.Run("native_controls_and_emitted_pg_replay",func(t *testing.T){
  var dump bytes.Buffer;if e:=stuntgres.Dump(p.Ctx,p.Native,&dump);e!=nil{t.Fatal(e)}
  for _,fragment:=range []string{`"renamed"`,`"New Key"`,`PARTITION BY RANGE`,`PARTITION BY LIST`,`PARTITION BY HASH`,`ATTACH PARTITION`,`stable key`}{if !strings.Contains(dump.String(),fragment){t.Fatal("missing dump definition",fragment,dump.String())}}
  if strings.Contains(dump.String()," RENAME "){t.Fatal("dump contains rename history",dump.String())}
  held,e:=p.Memory.QueryContext(p.Ctx,`SELECT * FROM pr_child ORDER BY "New Key"`);if e!=nil{t.Fatal(e)};defer held.Close()
  if e:=stuntgres.Reset(p.Ctx,p.Native);e!=nil{t.Fatal(e)};if e:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));e!=nil{t.Fatal(e)}
  var again bytes.Buffer;if e:=stuntgres.Dump(p.Ctx,p.Native,&again);e!=nil||again.String()!=dump.String(){t.Fatal("redump differs",e,again.String())}
  driver:=stuntgres.NewDriver();connector,e:=driver.OpenConnector("partition-rename/"+t.Name());if e!=nil{t.Fatal(e)}
  raw:=sql.OpenDB(connector);defer raw.Close();recorded,rec:=fixture.RecordedNativePool(t,connector)
  if e:=stuntgres.Restore(p.Ctx,raw,bytes.NewReader(dump.Bytes()));e!=nil{t.Fatal(e)}
  destinations:=[]fixture.Backend{{Name:"restored",DB:p.Memory,Recorder:p.MemoryRecord},{Name:"independent",DB:recorded,Recorder:rec}}
  if p.Postgres!=nil{
   if _,e:=p.Postgres.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE; CREATE SCHEMA public");e!=nil{t.Fatal(e)}
   if _,e:=p.Postgres.ExecContext(p.Ctx,dump.String());e!=nil{t.Fatal("actual emitted SQL replay",e,dump.String())}
   for _,b:=range p.Backends(){if b.PG{destinations=append(destinations,b)}}
  }
  for _,b:=range destinations{t.Run(b.Name,func(t *testing.T){
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
   partitionRenameRows(run,"renamed","payload")
   run(fixture.Select(`SELECT * FROM pr_child ORDER BY "New Key"`,[]string{"payload","bucket","New Key"},[]string{"TEXT","INT4","INT4"},[][]any{{"one",int64(0),int64(1)},{"two",int64(1),int64(2)},{"three",int64(1),int64(3)}}))
   run(fixture.Select(`SELECT * FROM pr_expr ORDER BY "New Key"`,[]string{"New Key","payload"},[]string{"INT4","TEXT"},[][]any{{int64(1),"default"},{int64(2),"default"}}))
   run(fixture.Exec(`INSERT INTO pr_att VALUES(4,0,'four')`,1));run(fixture.Exec(`INSERT INTO pr_child VALUES('five',1,5)`,1))
   run(fixture.Select(`SELECT "New Key" FROM pr_att ORDER BY "New Key"`,[]string{"New Key"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)},{int64(5)}}))
   run(referenceRangeMiss(`INSERT INTO pr_att VALUES(20,0,'bad')`,"pr_att",`"New Key"`,"20"))
   partitionRenameRestoredConsumers(run)
  })}
  columns,e:=held.Columns();if e!=nil||len(columns)!=3||columns[0]!="payload"||columns[1]!="bucket"||columns[2]!="New Key"{t.Fatal("old reader metadata",columns,e)}
  n:=0;for held.Next(){var payload string;var bucket,key int64;if e:=held.Scan(&payload,&bucket,&key);e!=nil{t.Fatal(e)};n++;if n>3||key!=int64(n)||payload!=[]string{"one","two","three"}[n-1]||bucket!=[]int64{0,1,1}[n-1]{t.Fatal("old reader changed",key,bucket,payload)}};if e:=held.Err();e!=nil||n!=3{t.Fatal("old reader",e,n)}
  fixture.RunStep(t,p.Ctx,p.Memory,p.MemoryRecord,fixture.Select(`SELECT count(*) FROM pr_att`,[]string{"count"},[]string{"INT8"},[][]any{{int64(5)}}))
 })
}

// Each destination executes the same independently expected future operations.
// Catalog identities are checked by their restored relationships, never by the
// allocator values (or physical attribute numbers) of the source catalog.
func partitionRenameRestoredConsumers(run func(fixture.Step)) {
 run(fixture.Prepared(fixture.Select("INSERT INTO pr_root(renamed,bucket,payload) VALUES(4,1,'four') RETURNING renamed,bucket,payload",
  []string{"renamed","bucket","payload"},[]string{"INT4","INT4","TEXT"},[][]any{{int64(4),int64(1),"four"}})))
 run(fixture.Exec("INSERT INTO pr_h1(renamed,bucket,payload) VALUES(5,0,'five')",1))
 rows:=[][]any{{int64(1),int64(0),"one"},{int64(2),int64(1),"two"},{int64(3),int64(0),"three"},{int64(4),int64(1),"four"},{int64(5),int64(0),"five"}}
 for _,table:=range []string{"pr_root","pr_mid","pr_hash","pr_h0","pr_h1"} {
  want:=rows
  if table=="pr_h0"{want=rows[:2]};if table=="pr_h1"{want=rows[2:]}
  run(fixture.Select("SELECT * FROM "+table+" ORDER BY renamed",[]string{"renamed","bucket","payload"},[]string{"INT4","INT4","TEXT"},want))
 }
 run(fixture.Select("SELECT * FROM ONLY pr_root",[]string{"renamed","bucket","payload"},[]string{"INT4","INT4","TEXT"},nil))
 run(referenceRangeMiss("INSERT INTO pr_root VALUES(20,0,'bad')","pr_root","renamed","20"))

 // Both duplicate key values are legal: the leaf expression index is nonunique.
 // Omitting payload exercises the restored DEFAULT as well as expression routing.
 run(fixture.Prepared(fixture.Exec(`INSERT INTO pr_expr("New Key") VALUES(1)`,1)))
 run(fixture.Select(`SELECT * FROM pr_expr_leaf ORDER BY "New Key"`,[]string{"New Key","payload"},[]string{"INT4","TEXT"},
  [][]any{{int64(1),"default"},{int64(1),"default"},{int64(2),"default"}}))
 run(referenceRangeError(`INSERT INTO pr_expr("New Key") VALUES(3)`,"23514",`new row for relation "pr_expr_leaf" violates check constraint "pr_upper"`,
  fixture.Diagnostic{Detail:"Failing row contains (3, default).",Schema:"public",Table:"pr_expr_leaf",Constraint:"pr_upper"}))
 run(referenceRangeMiss(`INSERT INTO pr_expr("New Key") VALUES(20)`,"pr_expr",`("New Key" + 1)`,"21"))
 run(fixture.Select("SELECT * FROM pr_view ORDER BY original",[]string{"original","calculated"},[]string{"INT4","INT4"},
  [][]any{{int64(1),int64(11)},{int64(1),int64(11)},{int64(2),int64(12)}}))
 run(fixture.Prepared(fixture.Select(`SELECT "New Key"+10 AS indexed FROM pr_expr_leaf WHERE "New Key"+10=11`,
  []string{"indexed"},[]string{"INT4"},[][]any{{int64(11)},{int64(11)}})))
 run(fixture.Select(`SELECT * FROM pr_expr ORDER BY "New Key"`,[]string{"New Key","payload"},[]string{"INT4","TEXT"},
  [][]any{{int64(1),"default"},{int64(1),"default"},{int64(2),"default"}}))

 // Row types must point back to their actual restored relations.
 run(fixture.Select("SELECT c.relname,t.typrelid=c.oid AS reciprocal FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.relname IN('pr_root','pr_mid','pr_hash','pr_h0','pr_h1','pr_att','pr_child','pr_expr','pr_expr_leaf','pr_view') ORDER BY c.relname",
  []string{"relname","reciprocal"},[]string{"NAME","BOOL"},
  [][]any{{"pr_att",true},{"pr_child",true},{"pr_expr",true},{"pr_expr_leaf",true},{"pr_h0",true},{"pr_h1",true},{"pr_hash",true},{"pr_mid",true},{"pr_root",true},{"pr_view",true}}))
 // Resolve names to local live attributes instead of assuming replay retained
 // the sparse attached child's original attnum.
 run(fixture.Select("SELECT c.relname,a.attname FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.relname IN('pr_root','pr_mid','pr_hash','pr_h0','pr_h1','pr_att','pr_child','pr_expr','pr_expr_leaf') AND a.attnum>0 AND NOT a.attisdropped AND a.attname IN('renamed','New Key') ORDER BY c.relname",
  []string{"relname","attname"},[]string{"NAME","NAME"},
  [][]any{{"pr_att","New Key"},{"pr_child","New Key"},{"pr_expr","New Key"},{"pr_expr_leaf","New Key"},{"pr_h0","renamed"},{"pr_h1","renamed"},{"pr_hash","renamed"},{"pr_mid","renamed"},{"pr_root","renamed"}}))
 run(fixture.Select("SELECT child.relname,parent.relname AS parent FROM pg_inherits i JOIN pg_class child ON child.oid=i.inhrelid JOIN pg_class parent ON parent.oid=i.inhparent WHERE child.relname IN('pr_mid','pr_hash','pr_h0','pr_h1','pr_child','pr_expr_leaf') ORDER BY child.relname",
  []string{"relname","parent"},[]string{"NAME","NAME"},
  [][]any{{"pr_child","pr_att"},{"pr_expr_leaf","pr_expr"},{"pr_h0","pr_hash"},{"pr_h1","pr_hash"},{"pr_hash","pr_mid"},{"pr_mid","pr_root"}}))
 run(fixture.Select("SELECT child.relname,parent.relname AS parent,d.deptype FROM pg_depend d JOIN pg_class child ON child.oid=d.objid JOIN pg_class parent ON parent.oid=d.refobjid WHERE d.classid='pg_class'::regclass::OID AND d.refclassid='pg_class'::regclass::OID AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='a' AND child.relname IN('pr_mid','pr_hash','pr_h0','pr_h1','pr_child','pr_expr_leaf') ORDER BY child.relname",
  []string{"relname","parent","deptype"},[]string{"NAME","NAME","CHAR"},
  [][]any{{"pr_child","pr_att","a"},{"pr_expr_leaf","pr_expr","a"},{"pr_h0","pr_hash","a"},{"pr_h1","pr_hash","a"},{"pr_hash","pr_mid","a"},{"pr_mid","pr_root","a"}}))
 run(fixture.Select("SELECT i.indrelid='pr_expr_leaf'::regclass::OID AS owner,i.indisvalid,i.indisready,i.indislive FROM pg_index i WHERE i.indexrelid='pr_expr_idx'::regclass::OID",
  []string{"owner","indisvalid","indisready","indislive"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true}}))
 run(fixture.Select(`SELECT a.attname,d.deptype FROM pg_depend d JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE d.classid='pg_class'::regclass::OID AND d.objid='pr_expr_idx'::regclass::OID AND d.refclassid='pg_class'::regclass::OID AND d.refobjid='pr_expr_leaf'::regclass::OID AND a.attname='New Key' AND NOT a.attisdropped`,
  []string{"attname","deptype"},[]string{"NAME","CHAR"},[][]any{{"New Key","a"}}))
 run(fixture.Select(`SELECT owner.relname,c.conname,a.attname,d.deptype FROM pg_constraint c JOIN pg_class owner ON owner.oid=c.conrelid JOIN pg_depend d ON d.classid='pg_constraint'::regclass::OID AND d.objid=c.oid JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE owner.relname IN('pr_expr','pr_expr_leaf') AND c.conname IN('pr_positive','pr_upper') AND d.refclassid='pg_class'::regclass::OID AND d.refobjid=c.conrelid AND a.attname='New Key' AND NOT a.attisdropped ORDER BY owner.relname,c.conname,d.deptype`,
  []string{"relname","conname","attname","deptype"},[]string{"NAME","NAME","NAME","CHAR"},
  [][]any{{"pr_expr","pr_positive","New Key","a"},{"pr_expr","pr_positive","New Key","n"},{"pr_expr","pr_upper","New Key","a"},{"pr_expr","pr_upper","New Key","n"},{"pr_expr_leaf","pr_positive","New Key","a"},{"pr_expr_leaf","pr_positive","New Key","n"},{"pr_expr_leaf","pr_upper","New Key","a"},{"pr_expr_leaf","pr_upper","New Key","n"}}))
 run(fixture.Select(`SELECT a.attname,d.deptype FROM pg_rewrite r JOIN pg_depend d ON d.classid='pg_rewrite'::regclass::OID AND d.objid=r.oid JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE r.ev_class='pr_view'::regclass::OID AND r.rulename='_RETURN' AND d.refclassid='pg_class'::regclass::OID AND d.refobjid='pr_expr'::regclass::OID AND a.attname='New Key' AND NOT a.attisdropped`,
  []string{"attname","deptype"},[]string{"NAME","CHAR"},[][]any{{"New Key","n"}}))
 run(fixture.Select("SELECT owner.relname,a.attname FROM pg_attrdef d JOIN pg_class owner ON owner.oid=d.adrelid JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE owner.relname IN('pr_expr','pr_expr_leaf') AND a.attname='payload' AND NOT a.attisdropped ORDER BY owner.relname",
  []string{"relname","attname"},[]string{"NAME","NAME"},[][]any{{"pr_expr","payload"},{"pr_expr_leaf","payload"}}))
}
func TestReferencePartitionRenameColumnControls(t *testing.T){partitionRenameControls(t)}

func TestReferencePartitionRenameColumnFeatureSmoke(t *testing.T){
 t.Run("hierarchy",TestReferencePartitionRenameColumnHierarchy)
 t.Run("attached",TestReferencePartitionRenameColumnAttachedLayout)
 t.Run("expressions",TestReferencePartitionRenameColumnExpressions)
 t.Run("prepared",TestReferencePartitionRenameColumnPrepared)
 t.Run("transactions",TestReferencePartitionRenameColumnTransactions)
 t.Run("controls",partitionRenameControls)
}
