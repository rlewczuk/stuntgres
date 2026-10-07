package smoke_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestQueryJoinsSmoke(t *testing.T) {
	ctx:=context.Background()
	d:=stuntgres.NewDriver()
	connector,err:=d.OpenConnector(t.Name())
	if err!=nil {t.Fatal(err)}
	db:=sql.OpenDB(connector)
	defer db.Close()
	exec:=func(q string,want int64){t.Helper();result,err:=db.ExecContext(ctx,q);if err!=nil {t.Fatal(q,err)};n,err:=result.RowsAffected();if err!=nil || n!=want {t.Fatal(q,n,err,want)}}
	for _,q:=range []string{"CREATE TABLE a(l TEXT,k INT,q INT)","CREATE TABLE b(r TEXT,q INT,k INT)","CREATE TABLE c(k INT,c TEXT)","CREATE TABLE empty(r TEXT,q INT,k INT)","CREATE TABLE wide(k BIGINT)"} {exec(q,0)}
	exec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)",4)
	exec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)",4)
	exec("INSERT INTO c VALUES(1,'c'),(1,'c')",2)
	exec("INSERT INTO wide VALUES(1)",1)
	product:=[][]any{
		{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"y",int64(8),int64(3)},{"a",int64(1),int64(9),"n",int64(9),nil},
		{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"y",int64(8),int64(3)},{"a",int64(1),int64(9),"n",int64(9),nil},
		{"n",nil,int64(9),"x",int64(9),int64(1)},{"n",nil,int64(9),"x",int64(9),int64(1)},{"n",nil,int64(9),"y",int64(8),int64(3)},{"n",nil,int64(9),"n",int64(9),nil},
		{"z",int64(2),int64(8),"x",int64(9),int64(1)},{"z",int64(2),int64(8),"x",int64(9),int64(1)},{"z",int64(2),int64(8),"y",int64(8),int64(3)},{"z",int64(2),int64(8),"n",int64(9),nil},
	}
	matching:=[][]any{{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)},{"a",int64(1),int64(9),"x",int64(9),int64(1)}}
	nested:=[][]any{{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"},{int64(1),"a",int64(9),"x",int64(9),"c"}}
	for _,tc:=range []struct{q string;names,types []string;want [][]any;args []any}{
		{"SELECT * FROM a,b",[]string{"l","k","q","r","q","k"},[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},product,nil},
		{"SELECT * FROM a CROSS JOIN b",[]string{"l","k","q","r","q","k"},[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},product,nil},
		{"SELECT * FROM a JOIN b ON a.k=b.k",[]string{"l","k","q","r","q","k"},[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},matching,nil},
		{"SELECT * FROM a JOIN b USING(q,k)",[]string{"q","k","l","r"},[]string{"INT4","INT4","TEXT","TEXT"},[][]any{{int64(9),int64(1),"a","x"},{int64(9),int64(1),"a","x"},{int64(9),int64(1),"a","x"},{int64(9),int64(1),"a","x"}},nil},
		{"SELECT * FROM a NATURAL INNER JOIN b",[]string{"k","q","l","r"},[]string{"INT4","INT4","TEXT","TEXT"},[][]any{{int64(1),int64(9),"a","x"},{int64(1),int64(9),"a","x"},{int64(1),int64(9),"a","x"},{int64(1),int64(9),"a","x"}},nil},
		{"SELECT u.*,a.k,b.k FROM a JOIN b USING(q,k) AS u",[]string{"q","k","k","k"},[]string{"INT4","INT4","INT4","INT4"},[][]any{{int64(9),int64(1),int64(1),int64(1)},{int64(9),int64(1),int64(1),int64(1)},{int64(9),int64(1),int64(1),int64(1)},{int64(9),int64(1),int64(1),int64(1)}},nil},
		{"SELECT * FROM (a JOIN b USING(k)) j(kk,ll)",[]string{"kk","ll","q","r","q"},[]string{"INT4","TEXT","INT4","TEXT","INT4"},[][]any{{int64(1),"a",int64(9),"x",int64(9)},{int64(1),"a",int64(9),"x",int64(9)},{int64(1),"a",int64(9),"x",int64(9)},{int64(1),"a",int64(9),"x",int64(9)}},nil},
		{"SELECT x.* FROM a x(ll,kk) WHERE x.kk=1",[]string{"ll","kk","q"},[]string{"TEXT","INT4","INT4"},[][]any{{"a",int64(1),int64(9)},{"a",int64(1),int64(9)}},nil},
		{"SELECT * FROM (a JOIN b USING(k)) JOIN c USING(k)",[]string{"k","l","q","r","q","c"},[]string{"INT4","TEXT","INT4","TEXT","INT4","TEXT"},nested,nil},
		{"SELECT * FROM a JOIN (b JOIN c USING(k)) USING(k)",[]string{"k","l","q","r","q","c"},[]string{"INT4","TEXT","INT4","TEXT","INT4","TEXT"},nested,nil},
		{"SELECT * FROM a CROSS JOIN empty",[]string{"l","k","q","r","q","k"},[]string{"TEXT","INT4","INT4","TEXT","INT4","INT4"},nil,nil},
		{"SELECT a.l,b.r FROM a JOIN b ON a.k=$1 AND a.k=b.k",[]string{"l","r"},[]string{"TEXT","TEXT"},nil,[]any{nil}},
		{"SELECT count(*) FROM a JOIN b USING(k)",[]string{"count"},[]string{"INT8"},[][]any{{int64(4)}},nil},
	} {
		rows,err:=db.QueryContext(ctx,tc.q,tc.args...);aggregateSmokeRows(t,rows,err,tc.names,tc.types,tc.want)
		stmt,err:=db.PrepareContext(ctx,tc.q);if err!=nil {t.Fatal(err)}
		rows,err=stmt.QueryContext(ctx,tc.args...);aggregateSmokeRows(t,rows,err,tc.names,tc.types,tc.want);stmt.Close()
	}
	for _,tc:=range []struct{q string;want stuntgres.Error}{
		{"SELECT k FROM a CROSS JOIN b",stuntgres.Error{Code:"42702",Message:`column reference "k" is ambiguous`,Position:"8"}},
		{"SELECT a.k FROM a AS x CROSS JOIN b",stuntgres.Error{Code:"42P01",Message:`invalid reference to FROM-clause entry for table "a"`,Hint:`Perhaps you meant to reference the table alias "x".`,Position:"8"}},
		{"SELECT a.k FROM (a JOIN b USING(k)) j",stuntgres.Error{Code:"42P01",Message:`invalid reference to FROM-clause entry for table "a"`,Detail:`There is an entry for table "a", but it cannot be referenced from this part of the query.`,Position:"8"}},
	} {
		rows,err:=db.QueryContext(ctx,tc.q);if rows!=nil {rows.Close();t.Fatal("failed query exposed rows")};if err:=typeCoreDiagnostic(err,tc.want);err!=nil {t.Fatal(err)}
		stmt,err:=db.PrepareContext(ctx,tc.q);if stmt!=nil {stmt.Close();t.Fatal("failed prepare exposed handle")};if err:=typeCoreDiagnostic(err,tc.want);err!=nil {t.Fatal(err)}
	}
	exec("CREATE TABLE dst(k INT)",0)
	exec("INSERT INTO dst SELECT k FROM a JOIN b USING(k)",4)
	rows,err:=db.QueryContext(ctx,"SELECT k FROM dst")
	aggregateSmokeRows(t,rows,err,[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(1)},{int64(1)}})
	for _,replacement:=range []string{"reset","restore"} {
		direct,err:=db.QueryContext(ctx,"SELECT dst.k,wide.k FROM dst JOIN wide USING(k)");if err!=nil {t.Fatal(err)}
		computed,err:=db.QueryContext(ctx,"SELECT dst.k+1 FROM dst JOIN wide USING(k)");if err!=nil {t.Fatal(err)}
		if replacement=="reset" {err=stuntgres.Reset(ctx,db)} else {err=stuntgres.Restore(ctx,db,strings.NewReader("CREATE TABLE dst(k INT);INSERT INTO dst VALUES(9);CREATE TABLE wide(k BIGINT);INSERT INTO wide VALUES(9)"))}
		if err!=nil {t.Fatal(err)}
		aggregateSmokeRows(t,direct,nil,[]string{"k","k"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(1)},{int64(1),int64(1)},{int64(1),int64(1)},{int64(1),int64(1)}})
		aggregateSmokeRows(t,computed,nil,[]string{"?column?"},[]string{"INT4"},[][]any{{int64(2)},{int64(2)},{int64(2)},{int64(2)}})
		if replacement=="reset" {exec("CREATE TABLE dst(k INT)",0);exec("INSERT INTO dst VALUES(1),(1),(1),(1)",4);exec("CREATE TABLE wide(k BIGINT)",0);exec("INSERT INTO wide VALUES(1)",1)} else {rows,err:=db.QueryContext(ctx,"SELECT k FROM dst JOIN wide USING(k)");aggregateSmokeRows(t,rows,err,[]string{"k"},[]string{"INT8"},[][]any{{int64(9)}})}
	}
}
