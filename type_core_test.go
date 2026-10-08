package stuntgres_test

import (
 "bytes"
 "context"
 "database/sql/driver"
 "errors"
 "io"
 "reflect"
 "strconv"
 "strings"
 "sync"
 "testing"
 "unicode/utf8"
 "stuntgres"
)

func typeCoreDiagnostic(t *testing.T,err error,want stuntgres.Error){t.Helper();want.Severity,want.SeverityUnlocalized="ERROR","ERROR";var got *stuntgres.Error;if !errors.As(err,&got)||*got!=want{t.Fatalf("got %#v (%v), want %#v",got,err,want)}}
func TestTypeCoreInputsMetadataAndFrames(t *testing.T){
 db:=openLocal(t,stuntgres.NewDriver(),"type-core-inputs");execSQL(t,db,"CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)")
 execSQL(t,db,"INSERT INTO typed VALUES(-2147483648,-9223372036854775808,TRUE,' ye '),(2147483647,9223372036854775807,FALSE,'of')")
 stmt,err:=db.Prepare("INSERT INTO typed VALUES($1,$2,$3,$4)");if err!=nil{t.Fatal(err)};defer stmt.Close()
 owned:=[]byte("owned");if _,err=stmt.Exec("0x_FF","0b10_10",owned,"on");err!=nil{t.Fatal(err)};owned[0]='X'
 for _,tc:=range []struct{text string;n int64}{{" +0007 ",7},{"0o17",15},{"1_000",1000}}{if _,err=stmt.Exec(tc.text,tc.text,"x",nil);err!=nil{t.Fatal(err)}}
 wantTuples(t,db,"SELECT * FROM typed ORDER BY i",[][]any{{int64(-2147483648),int64(-9223372036854775808),"true",true},{int64(7),int64(7),"x",nil},{int64(15),int64(15),"x",nil},{int64(255),int64(10),"owned",true},{int64(1000),int64(1000),"x",nil},{int64(2147483647),int64(9223372036854775807),"false",false}})
 rows,err:=db.Query("SELECT * FROM typed");if err!=nil{t.Fatal(err)};types,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};rows.Close();scans:=[]reflect.Type{reflect.TypeFor[int64](),reflect.TypeFor[int64](),reflect.TypeFor[string](),reflect.TypeFor[bool]()};for i,n:=range []string{"INT4","INT8","TEXT","BOOL"}{nullable,known:=types[i].Nullable();if types[i].DatabaseTypeName()!=n||types[i].ScanType()!=scans[i]||!known||nullable!=(i!=0){t.Fatal(types[i])}}
 wantTuples(t,db,"SELECT i FROM typed WHERE i=2147483648",[][]any{});wantTuples(t,db,"SELECT i FROM typed WHERE i=$1",[][]any{},nil)
 execSQL(t,db,"CREATE TABLE repeats(a INT,b TEXT,c INT)");repeat,err:=db.Prepare("INSERT INTO repeats VALUES($1,$2,$1)");if err!=nil{t.Fatal(err)};defer repeat.Close()
 for _,n:=range []int{7,8}{r,err:=repeat.Exec(n,"x");if err!=nil{t.Fatal(err)};count,err:=r.RowsAffected();if err!=nil||count!=1{t.Fatal(count,err)}}
 var wg sync.WaitGroup;for n:=range 16{wg.Go(func(){if _,err:=repeat.Exec(n+20,"parallel");err!=nil{t.Error(err)}})};wg.Wait()
 wantTuples(t,db,"SELECT a,c FROM repeats WHERE b='x' ORDER BY a",[][]any{{int64(7),int64(7)},{int64(8),int64(8)}})
 for _,q:=range []string{"INSERT INTO repeats VALUES($1,$1,1)","INSERT INTO typed VALUES($1,$1,NULL,NULL)"}{_,err=db.Prepare(q);sqlState(t,err,"0A000")}
}
func TestTypeCoreDiagnosticsPhasesAndAtomicity(t *testing.T){
 db:=openLocal(t,stuntgres.NewDriver(),"type-core-errors");execSQL(t,db,"CREATE TABLE failures(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)");execSQL(t,db,"INSERT INTO failures VALUES(99,99,'sentinel',TRUE)")
 for _,tc:=range []struct{column,text,name,code string}{{"i","oops","integer","22P02"},{"i","2147483648","integer","22003"},{"i","-2147483649","integer","22003"},{"b","9223372036854775808","bigint","22003"},{"b","-9223372036854775809","bigint","22003"},{"i","2147483648x","integer","22P02"},{"b","9223372036854775808x","bigint","22P02"},{"i","999999999999999999999999x","integer","22003"},{"i","\u00a01","integer","22P02"},{"f","truth","boolean","22P02"}}{
  message:="invalid input syntax for type "+tc.name+": \""+tc.text+"\"";if tc.code=="22003"{message="value \""+tc.text+"\" is out of range for type "+tc.name}
  q:="/*é*/ INSERT INTO failures("+tc.column+") VALUES('"+tc.text+"')";position:=strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,"'")])+1)
  for _,prepare:=range []bool{false,true}{var err error;if prepare{stmt,e:=db.Prepare(q);err=e;if stmt!=nil{stmt.Close()}}else{_,err=db.Exec(q)};typeCoreDiagnostic(t,err,stuntgres.Error{Code:tc.code,Message:message,Position:position})}
  stmt,err:=db.Prepare("INSERT INTO failures("+tc.column+") VALUES($1)");if err!=nil{t.Fatal(err)};_,err=stmt.Exec(tc.text);stmt.Close();typeCoreDiagnostic(t,err,stuntgres.Error{Code:tc.code,Message:message,Context:"unnamed portal parameter $1 = '...'"})
 }
 for _,tc:=range []struct{value int64;message string}{{2147483648,`value "2147483648" is out of range for type integer`},{-2147483649,`value "-2147483649" is out of range for type integer`}}{
  for _,prepared:=range []bool{false,true}{
   var err error
   if prepared{stmt,e:=db.Prepare("INSERT INTO failures(i) VALUES($1)");if e!=nil{t.Fatal(e)};_,err=stmt.Exec(tc.value);stmt.Close()}else{_,err=db.Exec("INSERT INTO failures(i) VALUES($1)",tc.value)}
   typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22003",Message:tc.message,Context:"unnamed portal parameter $1 = '...'"})
   wantTuples(t,db,"SELECT * FROM failures",[][]any{{int64(99),int64(99),"sentinel",true}})
  }
 }
 q:="INSERT INTO failures(i) VALUES(TRUE)";_,err:=db.Prepare(q);typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42804",Message:`column "i" is of type integer but expression is of type boolean`,Hint:"You will need to rewrite or cast the expression.",Position:strconv.Itoa(strings.Index(q,"TRUE")+1)})
 q="SELECT i FROM failures WHERE i=TRUE";_,err=db.Prepare(q);typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42883",Message:"operator does not exist: integer = boolean",Hint:"No operator matches the given name and argument types. You might need to add explicit type casts.",Position:strconv.Itoa(strings.Index(q,"=")+1)})
 for _,q:=range []string{"INSERT INTO failures(i) VALUES(1),(2147483648)","INSERT INTO failures(i) VALUES(1),(-2147483649)"}{stmt,err:=db.Prepare(q);if err!=nil{t.Fatal("narrowing failed at prepare",err)};_,err=stmt.Exec();stmt.Close();typeCoreDiagnostic(t,err,stuntgres.Error{Code:"22003",Message:"integer out of range"})}
 for _,q:=range []string{"INSERT INTO failures(i) VALUES(1),('oops')","INSERT INTO failures(i) VALUES(1),(NULL)"}{_,err=db.Exec(q);if err==nil{t.Fatal("bad later tuple accepted")}}
 ctx,cancel:=context.WithCancel(context.Background());cancel();_,err=db.ExecContext(ctx,"INSERT INTO failures(i) VALUES(1),(2)");if !errors.Is(err,context.Canceled){t.Fatal(err)}
 wantTuples(t,db,"SELECT * FROM failures",[][]any{{int64(99),int64(99),"sentinel",true}})
}

func TestTypeCoreQuotedWhereDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "type-core-quoted-where")
	execSQL(t, db, "CREATE TABLE failures(i INTEGER,b BIGINT,f BOOLEAN)")
	for _,tc := range []struct{column, text, code, message string}{
		{"i", "oops", "22P02", `invalid input syntax for type integer: "oops"`},
		{"f", "truth", "22P02", `invalid input syntax for type boolean: "truth"`},
		{"i", "2147483648", "22003", `value "2147483648" is out of range for type integer`},
	} {
		t.Run(tc.column+"_"+tc.text, func(t *testing.T) {
			// The empty table proves conversion happens at binding, not while
			// visiting rows. The UTF8 prefix makes byte and character offsets differ.
			q := "/*é*/ SELECT "+tc.column+" FROM failures WHERE "+tc.column+"='"+tc.text+"'"
			want := stuntgres.Error{Code:tc.code, Message:tc.message, Position:"38"}
			rows, err := db.Query(q)
			if rows != nil { rows.Close(); t.Fatal("failed Query exposed a cursor") }
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if stmt != nil { stmt.Close(); t.Fatal("quoted input did not fail at Prepare") }
			typeCoreDiagnostic(t, err, want)

			bound := "/*é*/ SELECT "+tc.column+" FROM failures WHERE "+tc.column+"=$1"
			want.Position = ""
			want.Context="unnamed portal parameter $1 = '...'"
			rows, err = db.Query(bound, tc.text)
			if rows != nil { rows.Close(); t.Fatal("failed parameter Query exposed a cursor") }
			typeCoreDiagnostic(t, err, want)
			stmt, err = db.Prepare(bound)
			if err != nil { t.Fatal("parameter failed at Prepare", err) }
			defer stmt.Close()
			rows, err = stmt.Query(tc.text)
			if rows != nil { rows.Close(); t.Fatal("failed prepared Query exposed a cursor") }
			typeCoreDiagnostic(t, err, want)
		})
	}
	wantTuples(t, db, "SELECT * FROM failures", [][]any{})
}
func TestTypeCoreControlsAndRetainedMetadata(t *testing.T){
 d:=stuntgres.NewDriver();db:=openLocal(t,d,"type-core-controls");execSQL(t,db,"CREATE TABLE typed(i INT NOT NULL,b BIGINT,t TEXT,f BOOL)");execSQL(t,db,"CREATE TABLE empty(i INT)")
 text:="O'Brien\n\\tail";execSQL(t,db,"INSERT INTO typed VALUES($1,$2,$3,$4)",-2147483648,int64(-9223372036854775808),text,true);execSQL(t,db,"INSERT INTO typed VALUES(2147483647,9223372036854775807,'',FALSE),(0,NULL,NULL,NULL)")
 raw,err:=d.Open("type-core-controls");if err!=nil{t.Fatal(err)};defer raw.Close();held,err:=raw.(driver.QueryerContext).QueryContext(context.Background(),"SELECT * FROM typed ORDER BY i",nil);if err!=nil{t.Fatal(err)};defer held.Close()
 var dump bytes.Buffer;if err=stuntgres.Dump(context.Background(),db,&dump);err!=nil{t.Fatal(err)};if err=stuntgres.Reset(context.Background(),db);err!=nil{t.Fatal(err)};if err=stuntgres.Restore(context.Background(),db,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
 var redump bytes.Buffer;if err=stuntgres.Dump(context.Background(),db,&redump);err!=nil||redump.String()!=dump.String(){t.Fatal(redump.String(),err)}
 want:=[][]any{{int64(-2147483648),int64(-9223372036854775808),text,true},{int64(0),nil,nil,nil},{int64(2147483647),int64(9223372036854775807),"",false}};wantTuples(t,db,"SELECT * FROM typed ORDER BY i",want)
 metadata:=held.(driver.RowsColumnTypeDatabaseTypeName);nullable:=held.(driver.RowsColumnTypeNullable);for i,n:=range []string{"INT4","INT8","TEXT","BOOL"}{if metadata.ColumnTypeDatabaseTypeName(i)!=n{t.Fatal("old metadata changed")};v,known:=nullable.ColumnTypeNullable(i);if !known||v!=(i!=0){t.Fatal(v,known)}}
 dst:=make([]driver.Value,4);for _,row:=range want{if err=held.Next(dst);err!=nil{t.Fatal(err)};for i,v:=range row{if dst[i]!=v{t.Fatal(dst,row)}}};if err=held.Next(dst);err!=io.EOF{t.Fatal(err)}
 for _,script:=range []string{"CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1),('oops')","CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES(1),(2147483648)","CREATE TABLE replacement(i INT NOT NULL); INSERT INTO replacement VALUES(1),(NULL)","CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"}{if err=stuntgres.Restore(context.Background(),db,strings.NewReader(script));err==nil{t.Fatal("bad restore accepted")};wantTuples(t,db,"SELECT * FROM typed ORDER BY i",want)}
}
