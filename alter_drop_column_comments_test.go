package stuntgres_test

import (
 "bytes"
 "context"
 "strings"
 "stuntgres"
 "testing"
)

func TestAlterDropColumnComments(t *testing.T) {
 for _,prepared:=range []bool{false,true}{t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T){
  db:=openLocal(t,stuntgres.NewDriver(),t.Name());ctx:=context.Background()
  for _,q:=range []string{"CREATE TABLE t(a INT,b TEXT,c BIGINT)","INSERT INTO t VALUES(1,'removed',9)","COMMENT ON TABLE t IS 'table keep'","COMMENT ON COLUMN t.b IS 'removed comment'","COMMENT ON COLUMN t.c IS 'survivor comment'"}{execSQL(t,db,q)}
  dump:=func()string{t.Helper();var b bytes.Buffer;if err:=stuntgres.Dump(ctx,db,&b);err!=nil{t.Fatal(err)};return b.String()}
  before:=dump();var err error
  if prepared{s,e:=db.Prepare("ALTER TABLE t DROP missing");if e!=nil{t.Fatal(e)};_,err=s.Exec();s.Close()}else{_,err=db.Exec("ALTER TABLE t DROP missing")}
  alterDropDiagnostic(t,err,"42703",`column "missing" of relation "t" does not exist`,"");if dump()!=before{t.Fatal("failed drop changed comments or values")}
  tableCoreExec(t,db,prepared,"ALTER TABLE t DROP b",0)
  got:=dump();for _,line:=range []string{`COMMENT ON TABLE "public"."t" IS E'table keep';`,`COMMENT ON COLUMN "public"."t"."c" IS E'survivor comment';`}{if !strings.Contains(got,line){t.Fatal("survivor metadata missing",got)}}
  if strings.Contains(got,"removed comment")||strings.Contains(got,`COMMENT ON COLUMN "public"."t"."b"`){t.Fatal("dropped comment survived",got)}
  tableCoreExec(t,db,prepared,"COMMENT ON COLUMN t.c IS 'updated survivor'",0);canonical:=dump()
  if !strings.Contains(canonical,`COMMENT ON COLUMN "public"."t"."c" IS E'updated survivor';`){t.Fatal(canonical)}
  for i:=0;i<2;i++{if err=stuntgres.Reset(ctx,db);err!=nil{t.Fatal(err)};if err=stuntgres.Restore(ctx,db,strings.NewReader(canonical));err!=nil{t.Fatal(err)};if dump()!=canonical{t.Fatal("comment roundtrip differs",dump())};wantTuples(t,db,"SELECT * FROM t",[][]any{{int64(1),int64(9)}})}
 })}
}
