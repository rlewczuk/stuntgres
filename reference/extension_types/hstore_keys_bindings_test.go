package reference_test

import (
 "database/sql"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceHstoreKeysBindings(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 for _,tc:=range []struct{expression string;args [][]any;want []any;array bool}{
 {`$1::hstore ? $2`,[][]any{{"a=>NULL","a"},{"","a"},{nil,"a"},{"a=>1","a"}},[]any{true,false,nil,true},false},
 {`$1::hstore ?& $2`,[][]any{{"a=>NULL","{a,NULL,a}"},{"","{}"},{nil,"{}"},{"a=>1","{missing}"}},[]any{true,true,nil,false},false},
 {`$1::hstore ?| $2`,[][]any{{"a=>NULL","{a}"},{"","{}"},{"a=>1",nil},{"a=>1","{a}"}},[]any{true,false,nil,true},false},
 {`$1::hstore @> $2`,[][]any{{"a=>NULL","a=>NULL"},{"",""},{nil,""},{"a=>NULL",`a=>"NULL"`}},[]any{true,true,nil,false},false},
 {`$1::hstore <@ $2`,[][]any{{"a=>NULL","a=>NULL,b=>2"},{"",""},{"",nil},{"a=>NULL",""}},[]any{true,true,nil,false},false},
 {`akeys($1::hstore)`,[][]any{{"a=>NULL"},{""},{nil},{"a=>1"}},[]any{"{a}","{}",nil,"{a}"},true},
 {`avals($1::hstore)`,[][]any{{"a=>NULL"},{""},{nil},{`a=>"NULL"`}},[]any{"{NULL}","{}",nil,`{"NULL"}`},true},
 } {
 q:="SELECT "+tc.expression+" AS v";stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 for i,args:=range tc.args {
 s:=fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{tc.want[i]}},args...)
 if tc.array {s=fixture.ArrayReferenceSelect(q,[]string{"v"},[]string{"_TEXT"},[]string{"_TEXT"},[][]any{{tc.want[i]}},b.PG,args...)}
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s));hstoreRun(t,p,b,held,s)
 }
 if err:=stmt.Close();err!=nil{t.Fatal(err)}
 }
 })
}

func TestReferenceHstoreKeysMetadata(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreInstall(t,p,b,c)
 checkArrays:=func(s fixture.Step) fixture.Step {
 shared:=s.CheckMetadata
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows) {
 if shared!=nil {shared(t,rows)}
 columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
 for _,column:=range columns {
 if nullable,known:=column.Nullable();nullable||known {t.Fatalf("%s computed array Nullable=%v/%v want false/false",column.Name(),nullable,known)}
 if precision,scale,known:=column.DecimalSize();precision!=0||scale!=0||known {t.Fatalf("%s array DecimalSize=%d/%d/%v want 0/0/false",column.Name(),precision,scale,known)}
 }
 }
 return s
 }
 for _,tc:=range []struct{input string;keys,values any}{{`'a=>NULL'`,"{a}","{NULL}"},{`'a=>"NULL"'`,"{a}",`{"NULL"}`},{`'""=>""'`,`{""}`,`{""}`},{`''`,"{}","{}"},{`NULL`,nil,nil}} {
 q:="SELECT akeys("+tc.input+"::hstore) AS k,avals("+tc.input+"::hstore) AS v"
 s:=checkArrays(fixture.ArrayReferenceSelect(q,[]string{"k","v"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},[][]any{{tc.keys,tc.values}},b.PG))
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
 for _,input:=range []string{`'a=>NULL'::hstore`,`''::hstore`,`NULL::hstore`} {
 for _,empty:=range []bool{false,true} {
 q:="SELECT "+input+" ? 'a' AS e,"+input+" ?& ARRAY['a'] AS a,"+input+" ?| ARRAY['a'] AS y,"+input+" @> 'a=>NULL' AS c,"+input+" <@ 'a=>NULL' AS d"
 want:=[][]any{{true,true,true,true,true}}
 if input==`''::hstore` {want=[][]any{{false,false,false,false,true}}}
 if input==`NULL::hstore` {want=[][]any{{nil,nil,nil,nil,nil}}}
 if empty {q+=" WHERE false";want=nil}
 s:=fixture.Select(q,[]string{"e","a","y","c","d"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL"},want)
 s.CheckMetadata=func(t *testing.T,rows *sql.Rows) {
 columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
 for _,column:=range columns {
 if column.ScanType()!=reflect.TypeFor[bool]()||column.DatabaseTypeName()!="BOOL" {t.Fatal("BOOL descriptor",column)}
 if n,known:=column.Nullable();n||known {t.Fatal("computed nullability",n,known)}
 if n,known:=column.Length();n!=0||known {t.Fatal("BOOL length",n,known)}
 if p,s,known:=column.DecimalSize();p!=0||s!=0||known {t.Fatal("BOOL decimal",p,s,known)}
 }
 }
 hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s))
 }
 }
 zero:=checkArrays(fixture.ArrayReferenceSelect(`SELECT akeys('a=>1'::hstore) AS k,avals('a=>1'::hstore) AS v WHERE false`,[]string{"k","v"},[]string{"_TEXT","_TEXT"},[]string{"_TEXT","_TEXT"},nil,b.PG))
 hstoreRun(t,p,b,c,zero);hstoreRun(t,p,b,c,fixture.Prepared(zero))
 hstoreRun(t,p,b,c,fixture.Select(`SELECT array_ndims(akeys('a=>1'::hstore)) AS r,array_lower(avals('a=>1'::hstore),1) AS l,cardinality(akeys(''::hstore)) AS n`,[]string{"r","l","n"},[]string{"INT4","INT4","INT4"},[][]any{{int64(1),int64(1),int64(0)}}))
 })
}
