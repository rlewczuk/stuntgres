package stuntgres_test

import (
 "bytes"
 "database/sql"
 "context"
 "strings"
 "stuntgres"
 "testing"
)

func TestAlterDropColumnQuotedExpressionDiagnostics(t *testing.T) {
 for _,tc:=range []struct{name,definition,drop,projection,filter,message string}{
  {"quote",`"q""u"`, `ALTER TABLE quoted DROP COLUMN "q""u"`, `/*é*/ SELECT "q""u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q""u"=1`, `column "q"u" does not exist`},
  {"backslash",`"q\u"`, `ALTER TABLE quoted DROP COLUMN "q\u"`, `/*é*/ SELECT "q\u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q\u"=1`, `column "q\u" does not exist`},
 } {
  t.Run(tc.name,func(t *testing.T){
   db:=openLocal(t,stuntgres.NewDriver(),t.Name());execSQL(t,db,"CREATE TABLE quoted("+tc.definition+" INT,a INT)");execSQL(t,db,"INSERT INTO quoted VALUES(1,7)")
   queries:=[]string{tc.projection,tc.filter,strings.TrimPrefix(tc.projection,"/*é*/ "),strings.TrimPrefix(tc.filter,"/*é*/ ")};retained:=make([]*sql.Stmt,len(queries))
   for i,query:=range queries{stmt,err:=db.Prepare(query);if err!=nil{t.Fatal(err)};defer stmt.Close();retained[i]=stmt}
   execSQL(t,db,tc.drop)
   for i,query:=range queries {
    position:=[]string{"14","34","8","28"}[i]
    _,err:=retained[i].Query();alterDropDiagnostic(t,err,"42703",tc.message,position)
    _,err=db.Query(query);alterDropDiagnostic(t,err,"42703",tc.message,position)
    _,err=db.Prepare(query);alterDropDiagnostic(t,err,"42703",tc.message,position)
   }
   wantTuples(t,db,"SELECT * FROM quoted",[][]any{{int64(7)}})
   var dump bytes.Buffer;if err:=stuntgres.Dump(context.Background(),db,&dump);err!=nil{t.Fatal(err)}
  })
 }
}
