package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type catalogVectorValuer struct { value driver.Value; err error }
func (v catalogVectorValuer) Value() (driver.Value,error) { return v.value,v.err }

func TestReferenceCatalogVectorsBindFrames(t *testing.T) {
	catalogVectorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
		run(fixture.Exec("CREATE TABLE vectors(id INT,s INT2VECTOR,o OIDVECTOR)",0))
		frames:=[]struct{ input any; want any }{{"1 2 3","1 2 3"},{nil,nil},{[]byte{},""},{[]byte("4 5"),"4 5"},{catalogVectorValuer{value:"6 7"},"6 7"},{[]byte(nil),nil},{catalogVectorValuer{value:nil},nil},{"",""}}
		for _, prepared:=range []bool{false,true} {
			q:="INSERT INTO vectors VALUES($1,$2,$3)"
			stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
			func(){defer stmt.Close();var executor fixture.Executor=b.DB;if prepared{executor=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}}
				for i,frame:=range frames {id:=i;if prepared{id+=100};fixture.RunStep(t,p.Ctx,executor,b.Recorder,fixture.Exec(q,1,id,frame.input,frame.input));catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vectors WHERE id=$1",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},[][]any{{frame.want,frame.want}},id))}
			}()
		}
		owned:=[]byte("8 9")
		run(fixture.Exec("INSERT INTO vectors VALUES(200,$1,$2)",1,owned,owned));owned[0]='0'
		catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s,o FROM vectors WHERE id=200",[]string{"s","o"},[]string{"INT2VECTOR","OIDVECTOR"},[][]any{{"8 9","8 9"}}))
		for _, typ:=range []string{"int2vector","oidvector"} {
			q:="SELECT $1::"+typ+" AS a,$1::"+typ+" AS b"
			stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
			func(){defer stmt.Close();held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q};for _,frame:=range frames{catalogVectorRows(t,p,b,held,fixture.Select(q,[]string{"a","b"},[]string{strings.ToUpper(typ),strings.ToUpper(typ)},[][]any{{frame.want,frame.want}},frame.input))}}()
			col:="s";if typ=="oidvector"{col="o"}
			q="SELECT "+col+" FROM vectors WHERE "+col+"=$1 ORDER BY id"
			stmt,err=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
			func(){defer stmt.Close();held:=fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q};for _,tc:=range []struct{arg any;rows [][]any}{{"1 2 3",[][]any{{"1 2 3"},{"1 2 3"}}},{nil,nil},{"",[][]any{{""},{""},{""},{""}}},{[]byte("4 5"),[][]any{{"4 5"},{"4 5"}}}}{catalogVectorRows(t,p,b,held,fixture.Select(q,[]string{col},[]string{strings.ToUpper(typ)},tc.rows,tc.arg))}}()
		}
		ctx:=context.WithValue(p.Ctx,fixture.RecorderKey{},b.Recorder)
		for _, col:=range []string{"s","o"} {
			for _, tc:=range []struct{id int;want any}{{0,"1 2 3"},{1,nil},{2,""}} {
				q:="SELECT "+col+" FROM vectors WHERE id=$1"
				var raw any;var text sql.NullString;var data []byte
				if err:=b.DB.QueryRowContext(ctx,q,tc.id).Scan(&raw);err!=nil{t.Fatal(err)}
				if !reflect.DeepEqual(fixture.Normalize(raw),tc.want){t.Fatalf("any=%#v want %#v",raw,tc.want)}
				if err:=b.DB.QueryRowContext(ctx,q,tc.id).Scan(&text);err!=nil{t.Fatal(err)}
				if text.Valid!=(tc.want!=nil)||(tc.want!=nil&&text.String!=tc.want){t.Fatal("NullString",text,tc.want)}
				if err:=b.DB.QueryRowContext(ctx,q,tc.id).Scan(&data);err!=nil{t.Fatal(err)}
				if tc.want==nil {if data!=nil{t.Fatal("NULL bytes",data)}} else if string(data)!=tc.want{t.Fatal("bytes",data,tc.want)}
				var nonnull string
				err:=b.DB.QueryRowContext(ctx,q,tc.id).Scan(&nonnull)
				if tc.want==nil {catalogVectorOrdinaryError(t,err)} else if err!=nil||nonnull!=tc.want{t.Fatal("string",nonnull,err)}
			}
		}
		bad:=errors.New("vector valuer refused")
		for _, input:=range []any{catalogVectorValuer{err:bad},struct{}{}} {
			result,err:=b.DB.ExecContext(ctx,"INSERT INTO vectors VALUES(300,$1,$2)",input,"1")
			catalogVectorOrdinaryError(t,err);if result!=nil{t.Fatal("converter failure exposed result")}
			catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s FROM vectors WHERE id=300",[]string{"s"},[]string{"INT2VECTOR"},nil))
		}
		run(fixture.Exec("INSERT INTO vectors VALUES(301,$1,$2)",1,"1","1"))
		if !b.PG {
			canceled,cancel:=context.WithCancel(p.Ctx);cancel()
			_,err:=b.DB.ExecContext(canceled,"INSERT INTO vectors VALUES(302,$1,$2)","1","1")
			if !errors.Is(err,context.Canceled){t.Fatal(err)}
			catalogVectorRows(t,p,b,b.DB,fixture.Select("SELECT s FROM vectors WHERE id=302",[]string{"s"},[]string{"INT2VECTOR"},nil))
			conn,err:=p.Native.Conn(p.Ctx);if err!=nil{t.Fatal(err)}
			defer conn.Close()
			err=conn.Raw(func(raw any)error{
				for _,q:=range []string{"SELECT s,o FROM vectors WHERE id=0","SELECT s,o FROM vectors WHERE id=1","SELECT s,o FROM vectors WHERE id<0"}{
					cursor,err:=raw.(driver.QueryerContext).QueryContext(p.Ctx,q,nil);if err!=nil{return err}
					check:=func(){for i,typ:=range []string{"INT2VECTOR","OIDVECTOR"}{if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i)!=typ||cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i)!=reflect.TypeFor[string](){t.Fatal("detached vector metadata",q,typ)}}}
					check()
					for{err:=cursor.Next(make([]driver.Value,2));if err==io.EOF{break};if err!=nil{cursor.Close();return err}}
					check();if err:=cursor.Close();err!=nil{return err};check()
				}
				return nil
			});if err!=nil{t.Fatal(err)}
		}
	})
}

func catalogVectorOrdinaryError(t *testing.T,err error){
	t.Helper();var state interface{SQLState()string};if err==nil||errors.As(err,&state){t.Fatalf("expected ordinary Go error, got %v",err)}
}
