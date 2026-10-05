package smoke_test

import (
 "bytes"
 "context"
 "database/sql"
 "database/sql/driver"
 "errors"
 "io"
 "reflect"
 "strings"
 "testing"
 "stuntgres"
)

func TestExpressionCoreSmoke(t *testing.T){
 ctx:=context.Background();d:=stuntgres.NewDriver();connector,err:=d.OpenConnector("expression-smoke");if err!=nil{t.Fatal(err)};db:=sql.OpenDB(connector);defer db.Close()
 exec:=func(q string,want int64){t.Helper();r,err:=db.ExecContext(ctx,q);if err!=nil{t.Fatal(q,err)};got,err:=r.RowsAffected();if err!=nil||got!=want{t.Fatal(q,got,err)}}
 exec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)",0)
 exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)",4)
 check:=func(rows *sql.Rows,err error,names,types []string,want [][]any){t.Helper();if err!=nil{t.Fatal(err)};defer rows.Close();gotNames,err:=rows.Columns();if err!=nil||!reflect.DeepEqual(gotNames,names){t.Fatal(gotNames,err)};metadata,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};for i,typ:=range types{if metadata[i].DatabaseTypeName()!=typ{t.Fatal(metadata[i])}};got:=make([][]any,0);for rows.Next(){v:=make([]any,len(names));dst:=make([]any,len(v));for i:=range v{dst[i]=&v[i]};if err:=rows.Scan(dst...);err!=nil{t.Fatal(err)};got=append(got,v)};if err:=rows.Err();err!=nil||!reflect.DeepEqual(got,want){t.Fatal(got,want,err)}}
 all:=[][]any{{int64(1)},{int64(2)},{int64(3)},{int64(4)}}
 cases:=[]struct{q string;names,types []string;want [][]any;args []any}{
 {"SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id",[]string{"id","?column?","?column?","?column?","?column?","?column?","?column?"},[]string{"INT4","INT4","INT8","TEXT","BOOL","BOOL","TEXT"},[][]any{{int64(1),int64(1),int64(2147483648),"x",true,false,nil},{int64(2),int64(1),int64(2147483648),"x",true,false,nil},{int64(3),int64(1),int64(2147483648),"x",true,false,nil},{int64(4),int64(1),int64(2147483648),"x",true,false,nil}},nil},
 {"SELECT 7 FROM t WHERE v=1",[]string{"?column?"},[]string{"INT4"},[][]any{{int64(7)},{int64(7)}},nil},
 {"SELECT $1,$1 FROM t ORDER BY id",[]string{"?column?","?column?"},[]string{"TEXT","TEXT"},[][]any{{"hi","hi"},{"hi","hi"},{"hi","hi"},{"hi","hi"}},[]any{"hi"}},
 {"SELECT $1 FROM t WHERE s=$1 ORDER BY id",[]string{"?column?"},[]string{"TEXT"},[][]any{{"x"},{"x"},{"x"}},[]any{"x"}},
 {"SELECT id FROM t WHERE TRUE ORDER BY id",[]string{"id"},[]string{"INT4"},all,nil},
 {"SELECT id FROM t WHERE f ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(4)}},nil},
 {"SELECT id FROM t WHERE FALSE",[]string{"id"},[]string{"INT4"},[][]any{},nil},
 {"SELECT id FROM t WHERE NULL",[]string{"id"},[]string{"INT4"},[][]any{},nil},
 {"SELECT v FROM t WHERE v=NULL",[]string{"v"},[]string{"INT4"},[][]any{},nil},
 {"SELECT id FROM t WHERE 'yes' ORDER BY id",[]string{"id"},[]string{"INT4"},all,nil},
 {"SELECT id FROM t WHERE 'no'",[]string{"id"},[]string{"INT4"},[][]any{},nil},
 {"SELECT id FROM t WHERE $1 ORDER BY id",[]string{"id"},[]string{"INT4"},all,[]any{true}},
 {"SELECT id FROM t WHERE $1",[]string{"id"},[]string{"INT4"},[][]any{},[]any{false}},
 {"SELECT id FROM t WHERE $1",[]string{"id"},[]string{"INT4"},[][]any{},[]any{nil}},
 }
 for _,tc:=range cases{rows,err:=db.QueryContext(ctx,tc.q,tc.args...);check(rows,err,tc.names,tc.types,tc.want);stmt,err:=db.PrepareContext(ctx,tc.q);if err!=nil{t.Fatal(tc.q,err)};rows,err=stmt.QueryContext(ctx,tc.args...);check(rows,err,tc.names,tc.types,tc.want);stmt.Close()}
 held,err:=db.PrepareContext(ctx,"SELECT id FROM t WHERE v=$1 ORDER BY id");if err!=nil{t.Fatal(err)};defer held.Close()
 for i,arg:=range []any{1,2,nil}{want:=[][]any{{int64(1)},{int64(4)}};if i==1{want=[][]any{{int64(3)}}};if i==2{want=[][]any{}};rows,err:=held.QueryContext(ctx,arg);check(rows,err,[]string{"id"},[]string{"INT4"},want)}
 for _,tc:=range []struct{q string;want stuntgres.Error}{
 {"SELECT missing FROM t",stuntgres.Error{Code:"42703",Message:`column "missing" does not exist`,Position:"8"}},
 {"SELECT /*é*/ missing FROM t",stuntgres.Error{Code:"42703",Message:`column "missing" does not exist`,Position:"14"}},
 {"SELECT id FROM t WHERE missing=1",stuntgres.Error{Code:"42703",Message:`column "missing" does not exist`,Position:"24"}},
 {"SELECT id FROM t WHERE 1",stuntgres.Error{Code:"42804",Message:"argument of WHERE must be type boolean, not type integer",Position:"24"}},
 {"SELECT id FROM t WHERE 'truth'",stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type boolean: "truth"`,Position:"24"}},
 {"SELECT $0 FROM t",stuntgres.Error{Code:"42P02",Message:"there is no parameter $0",Position:"8"}},
 {"SELECT $2 FROM t",stuntgres.Error{Code:"42P18",Message:"could not determine data type of parameter $1"}},
 {"SELECT $1 FROM t WHERE v=$1 ORDER BY id",stuntgres.Error{Code:"42P08",Message:"inconsistent types deduced for parameter $1",Detail:"integer versus text",Position:"8"}},
 {"SELECT $1 FROM t WHERE f=$1 ORDER BY id",stuntgres.Error{Code:"42P08",Message:"inconsistent types deduced for parameter $1",Detail:"boolean versus text",Position:"8"}},
 }{rows,err:=db.QueryContext(ctx,tc.q);if rows!=nil{rows.Close();t.Fatal("failed query exposed rows")};if err:=typeCoreDiagnostic(err,tc.want);err!=nil{t.Fatal(err)};stmt,err:=db.PrepareContext(ctx,tc.q);if stmt!=nil{stmt.Close();t.Fatal("failed prepare exposed handle")};if err:=typeCoreDiagnostic(err,tc.want);err!=nil{t.Fatal(err)}}
 if _,err:=held.QueryContext(ctx);err==nil{t.Fatal("missing actual argument accepted")}else{var diagnostic *stuntgres.Error;if errors.As(err,&diagnostic){t.Fatal("adapter error became SQLSTATE",err)}}
 // Raw cursor capture precedes any fetching or control replacement.
 raw,err:=d.Open("expression-smoke");if err!=nil{t.Fatal(err)};defer raw.Close();owned:=[]byte("owned");cursor,err:=raw.(driver.QueryerContext).QueryContext(ctx,"SELECT id,$1,NULL FROM t ORDER BY id",[]driver.NamedValue{{Ordinal:1,Value:owned}});if err!=nil{t.Fatal(err)};defer cursor.Close();owned[0]='X'
 exec("INSERT INTO t VALUES(5,5,5,'later',TRUE)",1)
 var dump bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&dump);err!=nil{t.Fatal(err)};if err:=stuntgres.Reset(ctx,db);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(ctx,db,strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')"));err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(cursor.Columns(),[]string{"id","?column?","?column?"}){t.Fatal(cursor.Columns())};meta:=cursor.(driver.RowsColumnTypeDatabaseTypeName);for i,want:=range []string{"INT4","TEXT","TEXT"}{if meta.ColumnTypeDatabaseTypeName(i)!=want{t.Fatal("retained metadata changed")}}
 dst:=make([]driver.Value,3);for i:=range 4{if err:=cursor.Next(dst);err!=nil||!reflect.DeepEqual(dst,[]driver.Value{int64(i+1),"owned",nil}){t.Fatal(dst,err)}};if err:=cursor.Next(dst);err!=io.EOF{t.Fatal(err)}
 rows,err:=db.QueryContext(ctx,"SELECT replacement FROM t");check(rows,err,[]string{"replacement"},[]string{"TEXT"},[][]any{{"new"}})
 exec("CREATE TABLE atomic(i INT NOT NULL)",0);exec("INSERT INTO atomic VALUES(99)",1)
 for _,q:=range []string{"INSERT INTO atomic VALUES(1),('oops')","INSERT INTO atomic VALUES(1),(2147483648)","INSERT INTO atomic VALUES(1),(NULL)"}{if _,err:=db.ExecContext(ctx,q);err==nil{t.Fatal("later bad tuple accepted",q)};rows,err:=db.QueryContext(ctx,"SELECT i,7 FROM atomic");check(rows,err,[]string{"i","?column?"},[]string{"INT4","INT4"},[][]any{{int64(99),int64(7)}})}
 if err:=stuntgres.Restore(ctx,db,strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"));err==nil{t.Fatal("parameter restore accepted")};rows,err=db.QueryContext(ctx,"SELECT i FROM atomic");check(rows,err,[]string{"i"},[]string{"INT4"},[][]any{{int64(99)}})
}
