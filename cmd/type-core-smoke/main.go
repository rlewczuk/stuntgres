package main

import (
 "bytes"
 "context"
 "database/sql"
 "database/sql/driver"
 "errors"
 "fmt"
 "io"
 "os"
 "reflect"
 "strconv"
 "strings"
 "unicode/utf8"
 "stuntgres"
)

func main(){if err:=run();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}}
func diagnostic(err error,want stuntgres.Error) error {want.Severity,want.SeverityUnlocalized="ERROR","ERROR";var got *stuntgres.Error;if !errors.As(err,&got)||*got!=want{return fmt.Errorf("diagnostic got %#v (%v), want %#v",got,err,want)};return nil}
func run() error {
 ctx:=context.Background();d:=stuntgres.NewDriver();connector,err:=d.OpenConnector("typed-core-smoke");if err!=nil{return err};db:=sql.OpenDB(connector);defer db.Close();if err=db.PingContext(ctx);err!=nil{return err}
 exec:=func(q string,count int64,args ...any)error{r,err:=db.ExecContext(ctx,q,args...);if err!=nil{return err};n,err:=r.RowsAffected();if err!=nil{return err};if n!=count{return fmt.Errorf("%s affected %d want %d",q,n,count)};return nil}
 check:=func(q string,names,types []string,want [][]any,args ...any)error{rows,err:=db.QueryContext(ctx,q,args...);if err!=nil{return err};defer rows.Close();columns,err:=rows.Columns();if err!=nil{return err};if !reflect.DeepEqual(columns,names){return fmt.Errorf("columns %v want %v",columns,names)};metadata,err:=rows.ColumnTypes();if err!=nil{return err};for i,n:=range types{if metadata[i].DatabaseTypeName()!=n{return fmt.Errorf("type %s want %s",metadata[i].DatabaseTypeName(),n)}};got:=make([][]any,0);for rows.Next(){row:=make([]any,len(names));dst:=make([]any,len(names));for i:=range row{dst[i]=&row[i]};if err=rows.Scan(dst...);err!=nil{return err};got=append(got,row)};if err=rows.Err();err!=nil{return err};if !reflect.DeepEqual(got,want){return fmt.Errorf("%s rows %#v want %#v",q,got,want)};return nil}
 names,types:=[]string{"i","b","t","f"},[]string{"INT4","INT8","TEXT","BOOL"}
 if err=exec("CREATE TABLE typed(i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)",0);err!=nil{return err};if err=exec("CREATE TABLE empty(i INT)",0);err!=nil{return err}
 if err=exec("INSERT INTO typed VALUES(-2147483648,-9223372036854775808,TRUE,' ye '),(2147483647,9223372036854775807,FALSE,'of')",2);err!=nil{return err}
 stmt,err:=db.PrepareContext(ctx,"INSERT INTO typed VALUES($1,$2,$3,$4)");if err!=nil{return err};defer stmt.Close();owned:=[]byte("owned");r,err:=stmt.ExecContext(ctx,"0x_FF","0b10_10",owned,"on");if err!=nil{return err};if n,err:=r.RowsAffected();err!=nil||n!=1{return fmt.Errorf("prepared count %d: %v",n,err)};owned[0]='X'
 if err=exec("INSERT INTO typed VALUES(' +0007 ','0o17',42,NULL),('1_000','0x_FF',NULL,NULL)",2);err!=nil{return err}
 want:=[][]any{{int64(-2147483648),int64(-9223372036854775808),"true",true},{int64(7),int64(15),"42",nil},{int64(255),int64(10),"owned",true},{int64(1000),int64(255),nil,nil},{int64(2147483647),int64(9223372036854775807),"false",false}}
 if err=check("SELECT * FROM typed ORDER BY i",names,types,want);err!=nil{return err}
 rows,err:=db.QueryContext(ctx,"SELECT * FROM typed");if err!=nil{return err};metadata,err:=rows.ColumnTypes();rows.Close();if err!=nil{return err};scan:=[]reflect.Type{reflect.TypeFor[int64](),reflect.TypeFor[int64](),reflect.TypeFor[string](),reflect.TypeFor[bool]()};for i:=range types{nullable,known:=metadata[i].Nullable();if metadata[i].ScanType()!=scan[i]||!known||nullable!=(i!=0){return fmt.Errorf("invalid metadata for %s",names[i])}}
 if err=exec("CREATE TABLE repeats(a INT,b TEXT,c INT)",0);err!=nil{return err};repeat,err:=db.PrepareContext(ctx,"INSERT INTO repeats VALUES($1,$2,$1)");if err!=nil{return err};defer repeat.Close();for _,n:=range []int{7,8}{r,err:=repeat.ExecContext(ctx,n,"x");if err!=nil{return err};count,err:=r.RowsAffected();if err!=nil||count!=1{return fmt.Errorf("repeat affected %d: %v",count,err)}}
 if err=check("SELECT * FROM repeats ORDER BY a",[]string{"a","b","c"},[]string{"INT4","TEXT","INT4"},[][]any{{int64(7),"x",int64(7)},{int64(8),"x",int64(8)}});err!=nil{return err}
 _,err=db.PrepareContext(ctx,"INSERT INTO repeats VALUES($1,$1,1)");if err=diagnostic(err,stuntgres.Error{Code:"0A000",Message:"parameter reused with different destination types"});err!=nil{return err}
 if err=check("SELECT i FROM typed WHERE i=2147483648",[]string{"i"},[]string{"INT4"},[][]any{});err!=nil{return err};if err=check("SELECT i FROM typed WHERE i=$1",[]string{"i"},[]string{"INT4"},[][]any{},nil);err!=nil{return err}
 // Every failing later tuple must leave the original complete bag unchanged.
 for _,tc:=range []struct{column,text,name,code string}{{"i","oops","integer","22P02"},{"i","2147483648","integer","22003"},{"i","-2147483649","integer","22003"},{"b","9223372036854775808","bigint","22003"},{"b","-9223372036854775809","bigint","22003"}}{
  message:="invalid input syntax for type "+tc.name+": \""+tc.text+"\"";if tc.code=="22003"{message="value \""+tc.text+"\" is out of range for type "+tc.name}
  q:="/*é*/ INSERT INTO typed("+tc.column+") VALUES(1),('"+tc.text+"')";_,err=db.ExecContext(ctx,q);if err=diagnostic(err,stuntgres.Error{Code:tc.code,Message:message,Position:strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,"'")])+1)});err!=nil{return err}
  bound,e:=db.PrepareContext(ctx,"INSERT INTO typed("+tc.column+") VALUES($1)");if e!=nil{return e};_,e=bound.ExecContext(ctx,tc.text);bound.Close();if err=diagnostic(e,stuntgres.Error{Code:tc.code,Message:message});err!=nil{return err}
 }
 q:="INSERT INTO typed(i) VALUES(TRUE)";_,err=db.PrepareContext(ctx,q);if err=diagnostic(err,stuntgres.Error{Code:"42804",Message:`column "i" is of type integer but expression is of type boolean`,Hint:"You will need to rewrite or cast the expression.",Position:strconv.Itoa(strings.Index(q,"TRUE")+1)});err!=nil{return err}
 narrow,err:=db.PrepareContext(ctx,"INSERT INTO typed(i) VALUES(1),(2147483648)");if err!=nil{return fmt.Errorf("deferred narrowing failed preparation: %w",err)};_,err=narrow.ExecContext(ctx);narrow.Close();if err=diagnostic(err,stuntgres.Error{Code:"22003",Message:"integer out of range"});err!=nil{return err}
 canceled,cancel:=context.WithCancel(ctx);cancel();_,err=db.ExecContext(canceled,"INSERT INTO typed(i) VALUES(1),(2)");if !errors.Is(err,context.Canceled){return fmt.Errorf("cancel got %v",err)};if err=check("SELECT * FROM typed ORDER BY i",names,types,want);err!=nil{return err}
 // Add complex text, then hold a raw cursor through a different generation.
 if err=exec("INSERT INTO typed VALUES($1,$2,$3,$4)",1,0,nil,"O'Brien\n\\tail",false);err!=nil{return err};want=append(want[:1],append([][]any{{int64(0),nil,"O'Brien\n\\tail",false}},want[1:]...)...)
 raw,err:=d.Open("typed-core-smoke");if err!=nil{return err};defer raw.Close();held,err:=raw.(driver.QueryerContext).QueryContext(ctx,"SELECT * FROM typed ORDER BY i",nil);if err!=nil{return err};defer held.Close()
 var dump bytes.Buffer;if err=stuntgres.Dump(ctx,db,&dump);err!=nil{return err};if err=stuntgres.Reset(ctx,db);err!=nil{return err};if err=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes()));err!=nil{return err};var redump bytes.Buffer;if err=stuntgres.Dump(ctx,db,&redump);err!=nil{return err};if dump.String()!=redump.String(){return fmt.Errorf("dump round trip differs")};if err=check("SELECT * FROM typed ORDER BY i",names,types,want);err!=nil{return err};if err=check("SELECT i FROM empty",[]string{"i"},[]string{"INT4"},[][]any{});err!=nil{return err}
 oldTypes:=held.(driver.RowsColumnTypeDatabaseTypeName);oldNullable:=held.(driver.RowsColumnTypeNullable);dst:=make([]driver.Value,4);for i,n:=range types{nullable,known:=oldNullable.ColumnTypeNullable(i);if oldTypes.ColumnTypeDatabaseTypeName(i)!=n||!known||nullable!=(i!=0){return fmt.Errorf("retained metadata changed")}};for _,row:=range want{if err=held.Next(dst);err!=nil{return err};for i,v:=range row{if dst[i]!=v{return fmt.Errorf("retained row %v want %v",dst,row)}}};if err=held.Next(dst);err!=io.EOF{return fmt.Errorf("retained EOF %v",err)}
 script:="CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1),('oops')"
 err=stuntgres.Restore(ctx,db,strings.NewReader(script));if err=diagnostic(err,stuntgres.Error{Code:"22P02",Message:`invalid input syntax for type integer: "oops"`,Position:strconv.Itoa(strings.Index(script,"'oops'")+1)});err!=nil{return err};if err=check("SELECT * FROM typed ORDER BY i",names,types,want);err!=nil{return err}
 fmt.Println("typed core: inference, codecs, metadata, atomicity and native round trips OK");return nil
}
