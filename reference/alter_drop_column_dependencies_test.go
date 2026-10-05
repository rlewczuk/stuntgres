//go:build reference

package reference_test

import (
 "reflect"
 "testing"
)

// These PostgreSQL-only catalogs are not claimed as native graph support.
func TestReferenceAlterDropColumnExternalDependencies(t *testing.T){
 p:=newReferencePair(t)
 var notices []referenceDiagnostic
 _,conn,record:=noticeReferencePool(t,p,true,&notices)
 run:=func(s referenceStep){referenceRunStep(t,p.ctx,conn,record,s)}
 run(referenceExec("CREATE TABLE t(a INT CONSTRAINT t_a_unique UNIQUE,b INT CONSTRAINT t_b_unique UNIQUE,CONSTRAINT t_b_check CHECK(b>0),CONSTRAINT t_a_check CHECK(a>0))",0))
 run(referenceExec("CREATE INDEX t_b_index ON t(b)",0));run(referenceExec("CREATE INDEX t_a_index ON t(a)",0));run(referenceExec("INSERT INTO t VALUES(1,9)",1));run(referenceExec("ALTER TABLE t DROP b RESTRICT",0))
 run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname",[]string{"conname"},[]string{"NAME"},[][]any{{"t_a_check"},{"t_a_unique"}}))
 run(referenceSelect("SELECT indexname FROM pg_indexes WHERE tablename='t' ORDER BY indexname",[]string{"indexname"},[]string{"NAME"},[][]any{{"t_a_index"},{"t_a_unique"}}))
 run(referenceSelect("SELECT attnum,attisdropped FROM pg_attribute WHERE attrelid='t'::regclass AND attnum>0 ORDER BY attnum",[]string{"attnum","attisdropped"},[]string{"INT2","BOOL"},[][]any{{int64(1),false},{int64(2),true}}))
 run(referenceExec("CREATE TABLE parent(a INT,b INT UNIQUE)",0));run(referenceExec("INSERT INTO parent VALUES(1,9)",1));run(referenceExec("CREATE VIEW v AS SELECT b FROM parent",0));run(referenceExec("CREATE VIEW independent AS SELECT a FROM parent",0))
 failure:=referenceImplicitError("ALTER TABLE parent DROP b RESTRICT",referenceDiagnostic{Code:"2BP01",Message:"cannot drop column b of table parent because other objects depend on it",Detail:"view v depends on column b of table parent",Hint:"Use DROP ... CASCADE to drop the dependent objects too."});failure.errorStage="execute";run(failure)
 run(referenceSelect("SELECT * FROM parent",[]string{"a","b"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(9)}}));run(referenceSelect("SELECT * FROM v",[]string{"b"},[]string{"INT4"},[][]any{{int64(9)}}))
 notices=nil;run(referenceExec("ALTER TABLE parent DROP b CASCADE",0));want:=[]referenceDiagnostic{{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"drop cascades to view v"}};if !reflect.DeepEqual(notices,want){t.Fatal(notices,want)}
 run(referenceSelect("SELECT * FROM independent",[]string{"a"},[]string{"INT4"},[][]any{{int64(1)}}))
 run(referenceExec("CREATE TABLE p(a INT,b INT UNIQUE)",0));run(referenceExec("CREATE TABLE child(x INT CONSTRAINT child_b_fk REFERENCES p(b))",0));run(referenceExec("INSERT INTO p VALUES(1,9)",1));run(referenceExec("INSERT INTO child VALUES(9)",1))
 failure=referenceImplicitError("ALTER TABLE p DROP b RESTRICT",referenceDiagnostic{Code:"2BP01",Message:"cannot drop column b of table p because other objects depend on it",Detail:"constraint child_b_fk on table child depends on column b of table p",Hint:"Use DROP ... CASCADE to drop the dependent objects too."});failure.errorStage="execute";run(failure)
 run(referenceSelect("SELECT * FROM p",[]string{"a","b"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(9)}}));notices=nil;run(referenceExec("ALTER TABLE p DROP b CASCADE",0));want=[]referenceDiagnostic{{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"drop cascades to constraint child_b_fk on table child"}};if !reflect.DeepEqual(notices,want){t.Fatal(notices,want)}
 run(referenceSelect("SELECT * FROM child",[]string{"x"},[]string{"INT4"},[][]any{{int64(9)}}));run(referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='child'::regclass",[]string{"conname"},[]string{"NAME"},nil))
}
