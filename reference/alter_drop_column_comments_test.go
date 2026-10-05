//go:build reference

package reference_test

import (
 "bytes"
 "database/sql"
 "fmt"
 "strings"
 "stuntgres"
 "testing"
)

func TestReferenceAlterDropColumnComments(t *testing.T){
 for _,prepared:=range []bool{false,true}{t.Run(fmt.Sprint(prepared),func(t *testing.T){
  p:=newReferencePair(t);step:=func(q string,n int64){s:=referenceExec(q,n);s.prepared=prepared;p.step(t,s)}
  step("CREATE TABLE t(a INT,b TEXT,c BIGINT)",0);step("INSERT INTO t VALUES(1,'removed',9)",1)
  for _,q:=range []string{"COMMENT ON TABLE t IS 'table keep'","COMMENT ON COLUMN t.b IS 'removed comment'","COMMENT ON COLUMN t.c IS 'survivor comment'"}{step(q,0)}
  dump:=func()string{t.Helper();var b bytes.Buffer;if err:=stuntgres.Dump(p.ctx,p.native,&b);err!=nil{t.Fatal(err)};return b.String()}
  oracle:=func(b,c string){t.Helper();for _,probe:=range []struct{q,want string}{{"SELECT obj_description('t'::regclass,'pg_class')","table keep"},{"SELECT col_description('t'::regclass,2)",b},{"SELECT col_description('t'::regclass,3)",c}}{var got sql.NullString;if err:=p.postgres.QueryRowContext(p.ctx,probe.q).Scan(&got);err!=nil||got.Valid!=(probe.want!="")||got.String!=probe.want{t.Fatal(probe.q,got,probe.want,err)}}}
  before:=dump();p.step(t,roleReferenceError("ALTER TABLE t DROP missing","42703",`column "missing" of relation "t" does not exist`,"",prepared,"execute"));if dump()!=before{t.Fatal("native failing drop changed comments")};oracle("removed comment","survivor comment")
  step("ALTER TABLE t DROP b",0);oracle("","survivor comment")
  canonical:=dump();if strings.Contains(canonical,"removed comment")||!strings.Contains(canonical,`COMMENT ON COLUMN "public"."t"."c" IS E'survivor comment';`)||!strings.Contains(canonical,`COMMENT ON TABLE "public"."t" IS E'table keep';`){t.Fatal(canonical)}
  step("COMMENT ON COLUMN t.c IS 'updated survivor'",0);oracle("","updated survivor");canonical=dump()
  if !strings.Contains(canonical,`COMMENT ON COLUMN "public"."t"."c" IS E'updated survivor';`){t.Fatal(canonical)}
  p.step(t,referenceSelect("SELECT * FROM t",[]string{"a","c"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(9)}}))
  for i:=0;i<2;i++{if err:=stuntgres.Reset(p.ctx,p.native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.ctx,p.native,strings.NewReader(canonical));err!=nil{t.Fatal(err)};if dump()!=canonical{t.Fatal("native canonical comment roundtrip changed",dump())};referenceRunStep(t,p.ctx,p.memory,p.memoryRecord,referenceSelect("SELECT * FROM t",[]string{"a","c"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(9)}}));oracle("","updated survivor")}
 })}
}
