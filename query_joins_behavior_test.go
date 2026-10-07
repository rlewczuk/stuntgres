package stuntgres_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres"
)

func TestQueryJoinsProductsAndConsumers(t *testing.T) {
	db:=queryJoinsPublicFixture(t)
	product:=[][]any{
		{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},
		{"a",int64(1),int64(9),"y",int64(8),int64(3)},{"a",int64(1),int64(9),"n",int64(9),nil},
		{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},
		{"a",int64(1),int64(9),"y",int64(8),int64(3)},{"a",int64(1),int64(9),"n",int64(9),nil},
		{"n",nil,int64(9),"x",int64(9),int64(1)},{"n",nil,int64(9),"x",int64(9),int64(1)},
		{"n",nil,int64(9),"y",int64(8),int64(3)},{"n",nil,int64(9),"n",int64(9),nil},
		{"z",int64(2),int64(8),"x",int64(9),int64(1)},{"z",int64(2),int64(8),"x",int64(9),int64(1)},
		{"z",int64(2),int64(8),"y",int64(8),int64(3)},{"z",int64(2),int64(8),"n",int64(9),nil},
	}
	for _,q:=range []string{"SELECT * FROM a,b","SELECT * FROM a CROSS JOIN b","SELECT * FROM a INNER JOIN b ON TRUE","SELECT * FROM a NATURAL JOIN b AS renamed(rr,qq,kk)"} {
		names:=[]string{"l","k","q","r","q","k"}
		if q=="SELECT * FROM a NATURAL JOIN b AS renamed(rr,qq,kk)" {names=[]string{"l","k","q","rr","qq","kk"}}
		for _,prepared:=range []bool{false,true} {
			if prepared {stmt,err:=db.Prepare(q);if err!=nil {t.Fatal(err)};rows,err:=stmt.Query();queryCoreRows(t,rows,err,names,[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},product,true);stmt.Close()} else {rows,err:=db.Query(q);queryCoreRows(t,rows,err,names,[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},product,true)}
		}
	}
	for _,tc:=range []struct{q string;names,types []string;want [][]any}{
		{"SELECT * FROM a JOIN b ON a.k=b.k",[]string{"l","k","q","r","q","k"},[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},[][]any{{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)}}},
		{"SELECT x.l,y.l FROM a x CROSS JOIN a y",[]string{"l","l"},[]string{"TEXT","TEXT"},[][]any{{"a","a"},{"a","a"},{"a","n"},{"a","z"},{"a","a"},{"a","a"},{"a","n"},{"a","z"},{"n","a"},{"n","a"},{"n","n"},{"n","z"},{"z","a"},{"z","a"},{"z","n"},{"z","z"}}},
		{"SELECT a.l,b.r FROM a JOIN b ON CAST(a.k AS BIGINT)=b.k AND COALESCE(a.q,0)=b.q AND CASE WHEN b.r='x' THEN TRUE ELSE FALSE END",[]string{"l","r"},[]string{"TEXT","TEXT"},[][]any{{"a","x"},{"a","x"},{"a","x"},{"a","x"}}},
		{"SELECT k,count(*) FROM a JOIN b USING(k) GROUP BY k HAVING count(*)=4",[]string{"k","count"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(4)}}},
		{"SELECT CASE WHEN a.k=b.k THEN a.l ELSE 'bad' END,CAST(b.k AS BIGINT) FROM a JOIN b USING(k) WHERE COALESCE(a.q,0)=b.q",[]string{"case","k"},[]string{"TEXT","INT8"},[][]any{{"a",int64(1)},{"a",int64(1)},{"a",int64(1)},{"a",int64(1)}}},
		{`SELECT /*é*/ "X"."K",b.k FROM a AS "X"("L","K") JOIN b ON "X"."K"=b.k`,[]string{"K","k"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(1)},{int64(1),int64(1)},{int64(1),int64(1)},{int64(1),int64(1)}}},
		{"SELECT a.l FROM a CROSS JOIN b JOIN c ON a.k=c.k AND b.k=c.k",[]string{"l"},[]string{"TEXT"},[][]any{{"a"},{"a"},{"a"},{"a"},{"a"},{"a"},{"a"},{"a"}}},
	} {
		for _,prepared:=range []bool{false,true} {
			if prepared {stmt,err:=db.Prepare(tc.q);if err!=nil {t.Fatal(err)};rows,err:=stmt.Query();queryCoreRows(t,rows,err,tc.names,tc.types,tc.want,true);stmt.Close()} else {rows,err:=db.Query(tc.q);queryCoreRows(t,rows,err,tc.names,tc.types,tc.want,true)}
		}
	}
	execSQL(t,db,"INSERT INTO b VALUES('d',7,1)")
	rows,err:=db.Query("SELECT r FROM a JOIN b USING(k)")
	queryCoreRows(t,rows,err,[]string{"r"},[]string{"TEXT"},[][]any{{"x"},{"x"},{"x"},{"x"},{"d"},{"d"}},true)
	rows,err=db.Query("SELECT r FROM a NATURAL JOIN b")
	queryCoreRows(t,rows,err,[]string{"r"},[]string{"TEXT"},[][]any{{"x"},{"x"},{"x"},{"x"}},true)
	execSQL(t,db,"CREATE TABLE ordered(k INT,l TEXT)")
	execSQL(t,db,"INSERT INTO ordered VALUES(2,'two'),(1,'one')")
	rows,err=db.Query("SELECT ordered.l FROM ordered CROSS JOIN wide w(wk) ORDER BY k")
	queryCoreRows(t,rows,err,[]string{"l"},[]string{"TEXT"},[][]any{{"one"},{"two"}},false)
}

func TestQueryJoinsInsertAtomicityAndDefaults(t *testing.T) {
	db:=queryJoinsPublicFixture(t)
	execSQL(t,db,"CREATE TABLE dst(k INT NOT NULL,stamp INT DEFAULT 7)")
	for _,prepared:=range []bool{false,true} {
		q:="INSERT INTO dst(k) SELECT k FROM a JOIN b USING(k)"
		var affected int64
		if prepared {stmt,err:=db.Prepare(q);if err!=nil {t.Fatal(err)};result,err:=stmt.Exec();if err!=nil {t.Fatal(err)};affected,err=result.RowsAffected();if err!=nil {t.Fatal(err)};stmt.Close()} else {result,err:=db.Exec(q);if err!=nil {t.Fatal(err)};affected,err=result.RowsAffected();if err!=nil {t.Fatal(err)}}
		if affected!=4 {t.Fatal(affected)}
		for _,failure:=range []struct{q string;want stuntgres.Error}{
			{"INSERT INTO dst(k) SELECT 1/(b.k-3) FROM a CROSS JOIN b",stuntgres.Error{Code:"22012",Message:"division by zero"}},
			{"INSERT INTO dst(k) SELECT a.k FROM a CROSS JOIN b",stuntgres.Error{Code:"23502",Message:`null value in column "k" of relation "dst" violates not-null constraint`,Detail:"Failing row contains (null, 7).",Schema:"public",Table:"dst",Column:"k"}},
		} {
			if prepared {stmt,err:=db.Prepare(failure.q);if err!=nil {t.Fatal(err)};result,err:=stmt.Exec();if result!=nil {t.Fatal("failed insert published result",result)};notNullDiagnostic(t,err,failure.want);stmt.Close()} else {result,err:=db.Exec(failure.q);if result!=nil {t.Fatal("failed insert published result",result)};notNullDiagnostic(t,err,failure.want)}
		}
		ctx,cancel:=context.WithCancel(context.Background());cancel()
		if _,err:=db.ExecContext(ctx,q);!errors.Is(err,context.Canceled) {t.Fatal(err)}
		rows,err:=db.Query("SELECT count(*) FROM dst WHERE k=1 AND stamp=7")
		want:=int64(4);if prepared {want=8}
		queryCoreRows(t,rows,err,[]string{"count"},[]string{"INT8"},[][]any{{want}},false)
	}
}

func TestQueryJoinsPreparedFreshSchema(t *testing.T) {
	db:=queryJoinsPublicFixture(t)
	for _,q:=range []string{"CREATE TABLE shape_a(k INT)","CREATE TABLE shape_b(k INT)","INSERT INTO shape_a VALUES(1)","INSERT INTO shape_b VALUES(1)"} {execSQL(t,db,q)}
	stable,err:=db.Prepare("SELECT k FROM shape_a NATURAL JOIN shape_b");if err!=nil {t.Fatal(err)};defer stable.Close()
	changed,err:=db.Prepare("SELECT * FROM shape_a NATURAL JOIN shape_b");if err!=nil {t.Fatal(err)};defer changed.Close()
	for _,q:=range []string{"ALTER TABLE shape_a ADD COLUMN q INT DEFAULT 7","ALTER TABLE shape_b ADD COLUMN q INT DEFAULT 8"} {execSQL(t,db,q)}
	rows,err:=stable.Query();queryCoreRows(t,rows,err,[]string{"k"},[]string{"INT4"},nil,true)
	rows,err=changed.Query();if rows!=nil {rows.Close();t.Fatal("changed shape published rows")}
	notNullDiagnostic(t,err,stuntgres.Error{Code:"0A000",Message:"cached plan must not change result type"})
	qualified,err:=db.Prepare("SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.k");if err!=nil {t.Fatal(err)};defer qualified.Close()
	execSQL(t,db,"ALTER TABLE shape_b RENAME k TO gone")
	rows,err=qualified.Query();if rows!=nil {rows.Close();t.Fatal("stale prepared binding exposed rows")}
	notNullDiagnostic(t,err,stuntgres.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42703",Message:"column shape_b.k does not exist",Hint:`Perhaps you meant to reference the column "shape_a.k".`,Position:"57"})
	dropped,err:=db.Prepare("SELECT shape_a.k FROM shape_a JOIN shape_b ON shape_a.k=shape_b.gone");if err!=nil {t.Fatal(err)};defer dropped.Close()
	execSQL(t,db,"ALTER TABLE shape_b DROP COLUMN gone")
	rows,err=dropped.Query();if rows!=nil {rows.Close();t.Fatal("dropped prepared column exposed stale rows")}
	notNullDiagnostic(t,err,stuntgres.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"42703",Message:"column shape_b.gone does not exist",Position:"57"})
}

func TestQueryJoinsExportAndSchemaOwnership(t *testing.T) {
	db:=queryJoinsPublicFixture(t)
	for _,q:=range []string{"CREATE TABLE bytes(k INT,v BYTEA)","INSERT INTO bytes VALUES(1,'\\x0102')"} {execSQL(t,db,q)}
	rows,err:=db.Query("SELECT bytes.v FROM bytes JOIN c USING(k)")
	if err!=nil {t.Fatal(err)}
	defer rows.Close()
	names,err:=rows.Columns();if err!=nil || len(names)!=1 || names[0]!="v" {t.Fatal(names,err)}
	names[0]="changed"
	names,err=rows.Columns();if err!=nil || names[0]!="v" {t.Fatal("schema export was not detached",names,err)}
	metadata,err:=rows.ColumnTypes();if err!=nil || len(metadata)!=1 || metadata[0].Name()!="v" || metadata[0].DatabaseTypeName()!="BYTEA" {t.Fatal(metadata,err)}
	var first,second []byte
	if !rows.Next() {t.Fatal(rows.Err())}
	if err=rows.Scan(&first);err!=nil {t.Fatal(err)}
	if err=stuntgres.Reset(context.Background(),db);err!=nil {t.Fatal(err)}
	if !rows.Next() {t.Fatal(rows.Err())}
	if err=rows.Scan(&second);err!=nil {t.Fatal(err)}
	if len(first)!=2 || first[0]!=1 || first[1]!=2 || len(second)!=2 || second[0]!=1 || second[1]!=2 {t.Fatal(first,second)}
	first[0]=99
	if second[0]!=1 {t.Fatal("exports shared mutable bytes",first,second)}
	rows.Close()
	if first[0]!=99 || first[1]!=2 || second[0]!=1 || second[1]!=2 || metadata[0].Name()!="v" {t.Fatal("close invalidated saved values/metadata",first,second,metadata)}
}
