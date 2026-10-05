//go:build reference

package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
)

func expressionReferenceSetup(t *testing.T)*referencePair {
 p:=newReferencePair(t)
 p.step(t,referenceExec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)",0))
 p.step(t,referenceExec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)",4))
 return p
}
func TestReferenceExpressionCoreScalars(t *testing.T){
 p:=expressionReferenceSetup(t)
 all:=[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}
 cases:=[]referenceStep{
 referenceSelect("SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id",[]string{"id","?column?","?column?","?column?","?column?","?column?","?column?"},[]string{"INT4","INT4","INT8","TEXT","BOOL","BOOL","TEXT"},[][]any{{int64(1),int64(1),int64(2147483648),"x",true,false,nil},{int64(2),int64(1),int64(2147483648),"x",true,false,nil},{int64(3),int64(1),int64(2147483648),"x",true,false,nil},{int64(4),int64(1),int64(2147483648),"x",true,false,nil}}),
 // Both rows are identical: ordered fixture comparison is also bag comparison
 // here, and preserves duplicate multiplicity without promising source order.
 referenceSelect("SELECT 7 FROM t WHERE v=1",[]string{"?column?"},[]string{"INT4"},[][]any{{int64(7)},{int64(7)}}),
 referenceSelect("SELECT v FROM t WHERE v=NULL",[]string{"v"},[]string{"INT4"},nil),
 referenceSelect("SELECT v FROM t WHERE v=$1",[]string{"v"},[]string{"INT4"},nil,nil),
 referenceSelect("SELECT id FROM t WHERE v=2147483648",[]string{"id"},[]string{"INT4"},nil),
 referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(4)}},1),
 referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(3)}},2),
 referenceSelect("SELECT $1,$1 FROM t ORDER BY id",[]string{"?column?","?column?"},[]string{"TEXT","TEXT"},[][]any{{"hi","hi"},{"hi","hi"},{"hi","hi"},{"hi","hi"}},"hi"),
 referenceSelect("SELECT $1,$1 FROM t ORDER BY id",[]string{"?column?","?column?"},[]string{"TEXT","TEXT"},[][]any{{nil,nil},{nil,nil},{nil,nil},{nil,nil}},nil),
 referenceSelect("SELECT $1 FROM t WHERE s=$1 ORDER BY id",[]string{"?column?"},[]string{"TEXT"},[][]any{{"x"},{"x"},{"x"}},"x"),
 referenceSelect("SELECT id FROM t WHERE TRUE ORDER BY id",[]string{"id"},[]string{"INT4"},all),
 referenceSelect("SELECT id FROM t WHERE FALSE",[]string{"id"},[]string{"INT4"},nil),
 referenceSelect("SELECT id FROM t WHERE NULL",[]string{"id"},[]string{"INT4"},nil),
 referenceSelect("SELECT id FROM t WHERE f ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(4)}}),
 referenceSelect("SELECT id FROM t WHERE 'yes' ORDER BY id",[]string{"id"},[]string{"INT4"},all),
 referenceSelect("SELECT id FROM t WHERE 'no'",[]string{"id"},[]string{"INT4"},nil),
 referenceSelect("SELECT id FROM t WHERE $1 ORDER BY id",[]string{"id"},[]string{"INT4"},all,true),
 referenceSelect("SELECT id FROM t WHERE $1",[]string{"id"},[]string{"INT4"},nil,false),
 referenceSelect("SELECT id FROM t WHERE $1",[]string{"id"},[]string{"INT4"},nil,nil),
 }
 for _,step:=range cases{p.step(t,step);p.step(t,referencePrepared(step))}
}
func TestReferenceExpressionCoreDiagnostics(t *testing.T){
 p:=expressionReferenceSetup(t)
 for _,tc:=range []struct{q string;d referenceDiagnostic}{
 {"SELECT missing FROM t",referenceDiagnostic{Code:"42703",Message:`column "missing" does not exist`,Position:"8"}},
 {"SELECT /*é*/ missing FROM t",referenceDiagnostic{Code:"42703",Message:`column "missing" does not exist`,Position:"14"}},
 {"SELECT id FROM t WHERE missing=1",referenceDiagnostic{Code:"42703",Message:`column "missing" does not exist`,Position:"24"}},
 {"SELECT id FROM t WHERE 1",referenceDiagnostic{Code:"42804",Message:"argument of WHERE must be type boolean, not type integer",Position:"24"}},
 {"SELECT id FROM t WHERE s",referenceDiagnostic{Code:"42804",Message:"argument of WHERE must be type boolean, not type text",Position:"24"}},
 {"SELECT id FROM t WHERE id=TRUE",referenceDiagnostic{Code:"42883",Message:"operator does not exist: integer = boolean",Position:"26",Hint:"No operator matches the given name and argument types. You might need to add explicit type casts."}},
 {"SELECT id FROM t WHERE 'truth'",referenceDiagnostic{Code:"22P02",Message:`invalid input syntax for type boolean: "truth"`,Position:"24"}},
 {"SELECT $0 FROM t",referenceDiagnostic{Code:"42P02",Message:"there is no parameter $0",Position:"8"}},
 }{step:=referenceImplicitError(tc.q,tc.d);step.selectRows=true;step.errorStage="execute";p.step(t,step);step.errorStage="prepare";p.step(t,referencePrepared(step))}
 bad:=referenceImplicitError("SELECT id FROM t WHERE $1",referenceDiagnostic{Code:"22P02",Message:`invalid input syntax for type boolean: "truth"`},"truth");bad.selectRows=true;bad.errorStage="execute";pg:=*bad.diagnostic;pg.Context="unnamed portal parameter $1 = '...'";bad.postgresDiagnostic=&pg;p.step(t,bad);p.step(t,referencePrepared(bad))
 for _,tc:=range []struct{q,detail string;arg any}{
 {"SELECT $1 FROM t WHERE v=$1 ORDER BY id","integer versus text",1},
 {"SELECT $1 FROM t WHERE f=$1 ORDER BY id","boolean versus text",true},
 }{
 step:=referenceImplicitError(tc.q,referenceDiagnostic{Code:"42P08",Message:"inconsistent types deduced for parameter $1",Detail:tc.detail,Position:"8"},tc.arg)
 step.selectRows=true;step.errorStage="execute";p.step(t,step)
 step.errorStage="prepare";p.step(t,referencePrepared(step))
 }
 // Zero-argument pq Query uses simple protocol, unlike native sparse analysis.
 // Assert distinct complete records without normalizing Code or Message.
 gap:=referenceImplicitError("SELECT $2 FROM t",referenceDiagnostic{Code:"42P18",Message:"could not determine data type of parameter $1"})
 gap.selectRows=true;gap.errorStage="execute"
 referenceRunStep(t,p.ctx,p.memory,p.memoryRecord,gap)
 simple:=referenceImplicitError("SELECT $2 FROM t",referenceDiagnostic{Code:"42P02",Message:"there is no parameter $2",Position:"8"})
 simple.selectRows=true;simple.errorStage="execute"
 referenceRunStep(t,p.ctx,p.postgres,p.postgresRecord,simple)
 gap.errorStage="prepare";p.step(t,referencePrepared(gap))
 p.step(t,referenceExec("CREATE TABLE atomic(i INT NOT NULL)",0));p.step(t,referenceExec("INSERT INTO atomic VALUES(99)",1))
 for _,step:=range []referenceStep{
 referenceImplicitError("INSERT INTO atomic VALUES(1),('oops')",referenceDiagnostic{Code:"22P02",Message:`invalid input syntax for type integer: "oops"`,Position:"31"}),
 referenceImplicitError("INSERT INTO atomic VALUES(1),(2147483648)",referenceDiagnostic{Code:"22003",Message:"integer out of range"}),
 referenceImplicitError("INSERT INTO atomic VALUES(1),(NULL)",referenceDiagnostic{Code:"23502",Message:`null value in column "i" of relation "atomic" violates not-null constraint`,Detail:"Failing row contains (null).",Schema:"public",Table:"atomic",Column:"i"}),
 }{step.errorStage="execute";p.step(t,step);if step.diagnostic.Code=="22P02"{step.errorStage="prepare"};p.step(t,referencePrepared(step));p.step(t,referenceSelect("SELECT i,7 FROM atomic",[]string{"i","?column?"},[]string{"INT4","INT4"},[][]any{{int64(99),int64(7)}}))}
}

func TestReferenceExpressionCoreHeldPreparedParameters(t *testing.T) {
	p:=expressionReferenceSetup(t)
	var observed [][][][]any
	for _,db:=range []*sql.DB{p.memory,p.postgres} {
		stmt,err:=db.PrepareContext(p.ctx,"SELECT id FROM t WHERE v=$1 ORDER BY id")
		if err!=nil { t.Fatal(err) }
		gotExecutions:=[][][]any{}
		for i,arg:=range []any{1,2,nil} {
			rows,err:=stmt.QueryContext(p.ctx,arg);if err!=nil { stmt.Close();t.Fatal(err) }
			names,err:=rows.Columns();if err!=nil || !reflect.DeepEqual(names,[]string{"id"}) { t.Fatal(names,err) }
			types,err:=rows.ColumnTypes();if err!=nil || types[0].DatabaseTypeName()!="INT4" { t.Fatal(types,err) }
			got:=[][]any{};for rows.Next() { var value any;if err:=rows.Scan(&value);err!=nil { t.Fatal(err) };got=append(got,[]any{value}) }
			if err:=rows.Err();err!=nil { t.Fatal(err) };rows.Close()
			want:=[][]any{{int64(1)},{int64(4)}};if i==1 { want=[][]any{{int64(3)}} };if i==2 { want=[][]any{} }
			if !reflect.DeepEqual(got,want) { t.Fatal("independent held execution",i,got,want) }
			gotExecutions=append(gotExecutions,got)
		}
		stmt.Close();observed=append(observed,gotExecutions)
	}
	if !reflect.DeepEqual(observed[0],observed[1]) { t.Fatal("held prepared backend parity",observed) }
}

func TestReferenceExpressionCoreEmptyTableAnalysis(t *testing.T) {
	p:=newReferencePair(t)
	p.step(t,referenceExec("CREATE TABLE t(id INT,s TEXT)",0))
	for _,tc:=range []struct{q string;d referenceDiagnostic}{
		{"SELECT missing FROM t",referenceDiagnostic{Code:"42703",Message:`column "missing" does not exist`,Position:"8"}},
		{"SELECT id FROM t WHERE 'truth'",referenceDiagnostic{Code:"22P02",Message:`invalid input syntax for type boolean: "truth"`,Position:"24"}},
		{"SELECT id FROM t WHERE s",referenceDiagnostic{Code:"42804",Message:"argument of WHERE must be type boolean, not type text",Position:"24"}},
	} {
		step:=referenceImplicitError(tc.q,tc.d);step.selectRows=true;step.errorStage="execute";p.step(t,step)
		step.errorStage="prepare";p.step(t,referencePrepared(step))
	}
}
