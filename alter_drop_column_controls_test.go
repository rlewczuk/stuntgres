package stuntgres_test

import (
 "bytes"
 "context"
 "errors"
 "strings"
 "stuntgres"
 "testing"
)

func TestAlterDropColumnControls(t *testing.T) {
 ctx:=context.Background();db:=openLocal(t,stuntgres.NewDriver(),t.Name())
 db.SetMaxOpenConns(1)
 execSQL(t,db,"CREATE USER drop_table_owner")
 execSQL(t,db,`CREATE SCHEMA "S" AUTHORIZATION drop_table_owner`)
 execSQL(t,db,"SET SESSION AUTHORIZATION drop_table_owner")
 execSQL(t,db,`CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,s TEXT,c BIGINT,second_drop TEXT)`)
 execSQL(t,db,`INSERT INTO "S"."T" VALUES(1,'dropped sentinel',$1,9223372036854775807,'second sentinel'),(2,NULL,NULL,NULL,NULL)`,"O'Brien\n\\tail")
 tableCoreExec(t,db,false,`ALTER TABLE "S"."T" DROP COLUMN b CASCADE`,0)
 tableCoreExec(t,db,true,`ALTER TABLE "S"."T" DROP COLUMN second_drop RESTRICT`,0)
 execSQL(t,db,"RESET SESSION AUTHORIZATION")
 var dump bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&dump);err!=nil{t.Fatal(err)}
 if !strings.Contains(dump.String(),`CREATE TABLE "S"."T" ("a" INTEGER NOT NULL, "s" TEXT, "c" BIGINT);`)||strings.Contains(dump.String(),"dropped sentinel")||strings.Contains(dump.String(),"ALTER TABLE"){t.Fatal(dump.String())}
 schemaDirective:=strings.Index(dump.String(),`CREATE SCHEMA "S" AUTHORIZATION "drop_table_owner";`)
 ownerDirective:=strings.Index(dump.String(),`SET SESSION AUTHORIZATION 'drop_table_owner';`)
 tableDirective:=strings.Index(dump.String(),`CREATE TABLE "S"."T"`)
 if schemaDirective<0||ownerDirective<=schemaDirective||tableDirective<=ownerDirective||strings.Contains(dump.String(),"second sentinel"){t.Fatal(dump.String())}
 want:=[][]any{{int64(1),"O'Brien\n\\tail",int64(9223372036854775807)},{int64(2),nil,nil}}
 for range 2 {
  if err:=stuntgres.Reset(ctx,db);err!=nil{t.Fatal(err)}
  if err:=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  execSQL(t,db,"SET SESSION AUTHORIZATION drop_table_owner")
  for _,prepared:=range []bool{false,true}{
   alterDropMetadata(t,db,prepared,`SELECT * FROM "S"."T"`,[]string{"a","s","c"},[]string{"INT4","TEXT","INT8"},[]bool{false,true,true},2)
   tableCoreExec(t,db,prepared,`ALTER TABLE "S"."T" DROP COLUMN IF EXISTS b`,0)
  }
  wantTuples(t,db,`SELECT * FROM "S"."T" ORDER BY a`,want)
  execSQL(t,db,"RESET SESSION AUTHORIZATION")
  var again bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&again);err!=nil||again.String()!=dump.String(){t.Fatal(err,again.String())}
 }
}

func TestAlterDropColumnZeroColumnControls(t *testing.T) {
 ctx:=context.Background();db:=openLocal(t,stuntgres.NewDriver(),t.Name());execSQL(t,db,"CREATE TABLE z(v TEXT)");execSQL(t,db,"INSERT INTO z VALUES('one'),('two')");execSQL(t,db,"ALTER TABLE ONLY z DROP COLUMN v")
 assertRows:=func(){t.Helper();for _,prepared:=range []bool{false,true}{alterDropMetadata(t,db,prepared,"SELECT * FROM z",[]string{},[]string{},[]bool{},2)}}
 assertRows();var dump bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&dump);err!=nil{t.Fatal(err)}
 if !strings.Contains(dump.String(),`CREATE TABLE "public"."z" ();`)||strings.Count(dump.String(),`INSERT INTO "public"."z" DEFAULT VALUES;`)!=2||strings.Contains(dump.String(),"VALUES ()"){t.Fatal(dump.String())}
 if err:=stuntgres.Reset(ctx,db);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)};assertRows()
 for _,q:=range []string{"INSERT INTO z DEFAULT VALUES","INSERT INTO z VALUES ()"}{_,err:=db.Exec(q);if q=="INSERT INTO z DEFAULT VALUES"{alterDropDiagnostic(t,err,"0A000","INSERT defaults are unsupported","15")}else{alterDropDiagnostic(t,err,"42601",`syntax error at or near ")"`,"23")};assertRows()}
 for _,script:=range []string{"CREATE TABLE x(v INT); INSERT INTO x DEFAULT VALUES;","CREATE TABLE x(); INSERT INTO x(v) DEFAULT VALUES;","CREATE TABLE x(); ALTER TABLE x DROP v;"}{if err:=stuntgres.Restore(ctx,db,strings.NewReader(script));err==nil{t.Fatal("invalid restore accepted",script)};assertRows()}
 canceled,cancel:=context.WithCancel(ctx);cancel();if err:=stuntgres.Restore(canceled,db,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled){t.Fatal(err)}
 var again bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&again);err!=nil||again.String()!=dump.String(){t.Fatal(err,again.String())}
}

func TestAlterDropColumnDistinctOwnerRestoreBoundary(t *testing.T) {
 ctx:=context.Background();db:=openLocal(t,stuntgres.NewDriver(),t.Name());db.SetMaxOpenConns(1)
 execSQL(t,db,"CREATE USER drop_schema_owner");execSQL(t,db,"CREATE USER drop_table_owner")
 script:=`CREATE SCHEMA "S" AUTHORIZATION drop_schema_owner; SET SESSION AUTHORIZATION drop_table_owner; CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,c BIGINT); INSERT INTO "S"."T" VALUES(1,'gone',9);`
 if err:=stuntgres.Restore(ctx,db,strings.NewReader(script));err!=nil{t.Fatal(err)}
 execSQL(t,db,`ALTER TABLE "S"."T" DROP b`)
 var dump bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&dump);err!=nil{t.Fatal(err)}
 if !strings.Contains(dump.String(),`CREATE SCHEMA "S" AUTHORIZATION "drop_schema_owner";`)||!strings.Contains(dump.String(),`SET SESSION AUTHORIZATION 'drop_table_owner';`){t.Fatal(dump.String())}
 for range 2 {
  if err:=stuntgres.Reset(ctx,db);err!=nil{t.Fatal(err)}
  if err:=stuntgres.Restore(ctx,db,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  execSQL(t,db,"SET SESSION AUTHORIZATION drop_schema_owner")
  for _,prepared:=range []bool{false,true}{
   q:=`ALTER TABLE "S"."T" DROP IF EXISTS missing`
   var err error
   if prepared{s,e:=db.Prepare(q);if e!=nil{t.Fatal(e)};_,err=s.Exec();s.Close()}else{_,err=db.Exec(q)}
   alterDropDiagnostic(t,err,"42501","must be owner of table T","")
  }
  execSQL(t,db,"RESET SESSION AUTHORIZATION")
  wantTuples(t,db,`SELECT * FROM "S"."T"`,[][]any{{int64(1),int64(9)}})
  var again bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&again);err!=nil||again.String()!=dump.String(){t.Fatal(err,again.String())}
 }
}
