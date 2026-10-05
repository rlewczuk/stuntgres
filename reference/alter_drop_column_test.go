//go:build reference

package reference_test

import (
 "context"
 "database/sql"
 "fmt"
 "reflect"
 "testing"
)

func TestReferenceAlterDropColumnShape(t *testing.T){
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){p:=newReferencePair(t);run:=func(s referenceStep){s.prepared=prepared;p.step(t,s)}
 run(referenceExec("CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)",0));run(referenceExec("INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)",2));run(referenceExec("ALTER TABLE t DROP COLUMN b RESTRICT",0))
 run(referenceSelect("SELECT * FROM t ORDER BY a",[]string{"a","c"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(9)},{int64(2),nil}}))
 run(referenceExec("INSERT INTO t VALUES(3,30)",1));run(referenceExec("INSERT INTO t(c,a) VALUES($1,$2)",1,40,4))
 run(referenceSelect("SELECT * FROM t ORDER BY a",[]string{"a","c"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(9)},{int64(2),nil},{int64(3),int64(30)},{int64(4),int64(40)}}))
 for _,tc:=range []struct{q,code,message string}{{"ALTER TABLE t DROP missing","42703",`column "missing" of relation "t" does not exist`},{"ALTER TABLE t DROP IF EXISTS ctid","0A000",`cannot drop system column "ctid"`}}{failure:=referenceImplicitError(tc.q,referenceDiagnostic{Code:tc.code,Message:tc.message});failure.errorStage="execute";run(failure)}
 for _,form:=range []string{"t","ONLY t","ONLY(t)","t*"}{run(referenceExec("DROP TABLE t",0));run(referenceExec("CREATE TABLE t(a INT,b TEXT)",0));run(referenceExec("INSERT INTO t VALUES(7,'gone')",1));run(referenceExec("ALTER TABLE "+form+" DROP b CASCADE",0));run(referenceSelect("SELECT * FROM t",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))}
 })}
}

func TestReferenceAlterDropColumnNotices(t *testing.T){
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){p:=newReferencePair(t)
 for _,postgres:=range []bool{false,true}{t.Run(fmt.Sprint(postgres),func(t *testing.T){var notices []referenceDiagnostic;_,conn,record:=noticeReferencePool(t,p,postgres,&notices);run:=func(s referenceStep){s.prepared=prepared;referenceRunStep(t,p.ctx,conn,record,s)}
 run(referenceExec("CREATE TABLE t(a INT,b TEXT)",0));run(referenceExec("INSERT INTO t VALUES(1,'keep')",1));notices=nil
 run(referenceExec("ALTER TABLE t DROP COLUMN IF EXISTS missing",0));want:=[]referenceDiagnostic{{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:`column "missing" of relation "t" does not exist, skipping`}};if !reflect.DeepEqual(notices,want){t.Fatal(notices,want)}
 run(referenceExec("SET client_min_messages TO warning",0));notices=nil;run(referenceExec("ALTER TABLE t DROP IF EXISTS missing",0));if len(notices)!=0{t.Fatal(notices)}
 run(referenceSelect("SELECT * FROM t",[]string{"a","b"},[]string{"INT4","TEXT"},[][]any{{int64(1),"keep"}}))
 })}
 })}
}

func TestReferenceAlterDropColumnPreparedLifetime(t *testing.T){
 p:=newReferencePair(t)
 for _,db:=range []*sql.DB{p.memory,p.postgres}{
  record:=&referenceRecorder{};ctx:=context.WithValue(p.ctx,referenceRecorderKey{},record)
  referenceRunStep(t,ctx,db,record,referenceExec("CREATE TABLE held(a INT,b TEXT,c BIGINT)",0))
  referenceRunStep(t,ctx,db,record,referenceExec("INSERT INTO held VALUES(1,'old',9)",1))
  survivor,err:=db.PrepareContext(ctx,"SELECT a,c FROM held");if err!=nil{t.Fatal(err)}
  star,err:=db.PrepareContext(ctx,"SELECT * FROM held");if err!=nil{t.Fatal(err)}
  dropped,err:=db.PrepareContext(ctx,"SELECT b FROM held");if err!=nil{t.Fatal(err)}
  held,err:=db.QueryContext(ctx,"SELECT * FROM held");if err!=nil{t.Fatal(err)}
  // Consume/copy the oracle portal before DDL, rather than holding a server lock.
  labels,err:=held.Columns();if err!=nil||!reflect.DeepEqual(labels,[]string{"a","b","c"}){t.Fatal(labels,err)}
  if !held.Next(){t.Fatal(held.Err())};var a,c int64;var b string;if err=held.Scan(&a,&b,&c);err!=nil||a!=1||b!="old"||c!=9{t.Fatal(a,b,c,err)};held.Close()
  referenceRunStep(t,ctx,db,record,referenceExec("ALTER TABLE held DROP b",0))
  rows,err:=survivor.QueryContext(ctx);if err!=nil{t.Fatal(err)};if !rows.Next(){t.Fatal(rows.Err())};if err=rows.Scan(&a,&c);err!=nil||a!=1||c!=9{t.Fatal(a,c,err)};rows.Close()
  rows,err=star.QueryContext(ctx);if rows!=nil{rows.Close();t.Fatal("changed star returned rows")};referenceAssertDiagnostic(t,err,&referenceDiagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"0A000",Message:"cached plan must not change result type"})
  rows,err=dropped.QueryContext(ctx);if rows!=nil{rows.Close();t.Fatal("dropped column returned rows")};referenceAssertDiagnostic(t,err,&referenceDiagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42703",Message:`column "b" does not exist`,Position:"8"})
  survivor.Close();star.Close();dropped.Close()
 }
}
