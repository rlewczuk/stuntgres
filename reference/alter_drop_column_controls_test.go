//go:build reference

package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "strings"
 "stuntgres"
 "testing"
)

func TestReferenceAlterDropColumnControls(t *testing.T){
 roleReferenceCleanup(t,[]string{"drop_table_owner","postgres"})
 p:=newReferencePair(t);schemaReferenceCleanup(t,"S")
 p.step(t,referenceExec("CREATE USER drop_table_owner",0))
 p.step(t,referenceExec(`CREATE SCHEMA "S" AUTHORIZATION drop_table_owner`,0))
 for _,db:=range []*sql.DB{p.memory,p.postgres}{
  conn,err:=db.Conn(p.ctx);if err!=nil{t.Fatal(err)}
  func(){defer conn.Close();defer conn.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
   record:=&referenceRecorder{};run:=func(s referenceStep){referenceRunStep(t,p.ctx,conn,record,s)}
   run(referenceExec("SET SESSION AUTHORIZATION drop_table_owner",0))
   run(referenceExec(`CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,s TEXT,c BIGINT,second_drop TEXT)`,0))
   run(referenceExec(`INSERT INTO "S"."T" VALUES(1,'sentinel',$1,9223372036854775807,'second sentinel'),(2,NULL,NULL,NULL,NULL)`,2,"O'Brien\n\\tail"))
   run(referenceExec(`ALTER TABLE "S"."T" DROP COLUMN b CASCADE`,0))
   run(referencePrepared(referenceExec(`ALTER TABLE "S"."T" DROP COLUMN second_drop RESTRICT`,0)))
  }()
 }
 p.step(t,referenceExec("CREATE TABLE z(v TEXT)",0));p.step(t,referenceExec("INSERT INTO z VALUES('one'),('two')",2));p.step(t,referenceExec("ALTER TABLE ONLY(z) DROP v",0))
 var dump bytes.Buffer;if err:=stuntgres.Dump(p.ctx,p.native,&dump);err!=nil{t.Fatal(err)}
 if strings.Contains(dump.String(),"sentinel")||strings.Contains(dump.String(),"VALUES ()")||strings.Count(dump.String(),`INSERT INTO "public"."z" DEFAULT VALUES;`)!=2||!strings.Contains(dump.String(),`CREATE TABLE "S"."T" ("a" INTEGER NOT NULL, "s" TEXT, "c" BIGINT);`){t.Fatal(dump.String())}
 schemaDirective:=strings.Index(dump.String(),`CREATE SCHEMA "S" AUTHORIZATION "drop_table_owner";`);ownerDirective:=strings.Index(dump.String(),`SET SESSION AUTHORIZATION 'drop_table_owner';`);tableDirective:=strings.Index(dump.String(),`CREATE TABLE "S"."T"`)
 if schemaDirective<0||ownerDirective<=schemaDirective||tableDirective<=ownerDirective{t.Fatal(dump.String())}
 oracle,err:=p.postgres.Conn(p.ctx);if err!=nil{t.Fatal(err)}
 func(){defer oracle.Close();defer oracle.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
  if _,err=oracle.ExecContext(p.ctx,`DROP SCHEMA "S" CASCADE; DROP TABLE z`);err!=nil{t.Fatal(err)}
  // Provision the native bootstrap owner as in schema_table_integration; the
  // oracle authenticates as stuntgres. Replay the canonical bytes unchanged.
  if _,err=oracle.ExecContext(p.ctx,"CREATE ROLE postgres SUPERUSER NOLOGIN");err!=nil{t.Fatal(err)}
  if _,err=oracle.ExecContext(p.ctx,dump.String());err!=nil{t.Fatal("real PostgreSQL rejected same owner-bearing live dump",err)}
 }()
 var schemaOwner,tableOwner string
 if err=referenceAdmin.QueryRowContext(p.ctx,`SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='S'`).Scan(&schemaOwner);err!=nil||schemaOwner!="drop_table_owner"{t.Fatal(schemaOwner,err)}
 if err=referenceAdmin.QueryRowContext(p.ctx,`SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='S' AND c.relname='T'`).Scan(&tableOwner);err!=nil||tableOwner!="drop_table_owner"{t.Fatal(tableOwner,err)}
 var bootstrapTableOwner string
 if err=referenceAdmin.QueryRowContext(p.ctx,`SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='public' AND c.relname='z'`).Scan(&bootstrapTableOwner);err!=nil||bootstrapTableOwner!="postgres"{t.Fatal(bootstrapTableOwner,err)}
 for range 2 {if err:=stuntgres.Reset(p.ctx,p.native);err!=nil{t.Fatal(err)};if err:=stuntgres.Restore(p.ctx,p.native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
 expected:=referenceSelect(`SELECT * FROM "S"."T" ORDER BY a`,[]string{"a","s","c"},[]string{"INT4","TEXT","INT8"},[][]any{{int64(1),"O'Brien\n\\tail",int64(9223372036854775807)},{int64(2),nil,nil}});p.step(t,expected);p.step(t,referencePrepared(expected))
 for _,db:=range []*sql.DB{p.memory,p.postgres}{
  conn,err:=db.Conn(p.ctx);if err!=nil{t.Fatal(err)}
  func(){defer conn.Close();defer conn.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION");record:=&referenceRecorder{};run:=func(s referenceStep){referenceRunStep(t,p.ctx,conn,record,s)}
   run(referenceExec("SET SESSION AUTHORIZATION drop_table_owner",0))
   run(expected);run(referencePrepared(expected))
   run(referenceExec(`ALTER TABLE "S"."T" DROP COLUMN IF EXISTS b`,0));run(referencePrepared(referenceExec(`ALTER TABLE "S"."T" DROP COLUMN IF EXISTS b`,0)))
  }()
 }
 empty:=referenceSelect("SELECT * FROM z",[]string{},[]string{},[][]any{{},{}});p.step(t,empty);p.step(t,referencePrepared(empty));var again bytes.Buffer;if err:=stuntgres.Dump(p.ctx,p.native,&again);err!=nil||again.String()!=dump.String(){t.Fatal(err,again.String())}}
 for _,script:=range []string{"CREATE TABLE bad(v INT); INSERT INTO bad DEFAULT VALUES;","CREATE TABLE bad(); INSERT INTO bad(v) DEFAULT VALUES;","CREATE TABLE bad(); ALTER TABLE bad DROP v;"}{if err:=stuntgres.Restore(p.ctx,p.native,strings.NewReader(script));err==nil{t.Fatal("invalid private control accepted",script)};p.step(t,referenceSelect("SELECT * FROM z",[]string{},[]string{},[][]any{{},{}}))}
}

func TestReferenceAlterDropColumnDistinctOwnerRestoreBoundary(t *testing.T) {
 roleReferenceCleanup(t,[]string{"drop_schema_owner","drop_table_owner"})
 p:=newReferencePair(t);schemaReferenceCleanup(t,"S")
 p.step(t,referenceExec("CREATE USER drop_schema_owner",0));p.step(t,referenceExec("CREATE USER drop_table_owner",0))
 script:=`CREATE SCHEMA "S" AUTHORIZATION drop_schema_owner; SET SESSION AUTHORIZATION drop_table_owner; CREATE TABLE "S"."T"(a INT NOT NULL,b TEXT,c BIGINT); INSERT INTO "S"."T" VALUES(1,'gone',9);`
 if err:=stuntgres.Restore(p.ctx,p.native,strings.NewReader(script));err!=nil{t.Fatal(err)}
 if _,err:=p.native.ExecContext(p.ctx,`ALTER TABLE "S"."T" DROP b`);err!=nil{t.Fatal(err)}
 var dump bytes.Buffer;if err:=stuntgres.Dump(p.ctx,p.native,&dump);err!=nil{t.Fatal(err)}
 if !strings.Contains(dump.String(),`CREATE SCHEMA "S" AUTHORIZATION "drop_schema_owner";`)||!strings.Contains(dump.String(),`SET SESSION AUTHORIZATION 'drop_table_owner';`){t.Fatal(dump.String())}
 oracle,err:=referenceAdmin.Conn(p.ctx);if err!=nil{t.Fatal(err)}
 func(){defer oracle.Close();defer oracle.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
  // Independent PG fixture privileges bridge its real destination authority;
  // native Dump still rejects explicit ACLs and has no public GRANT support.
  for _,line:=range strings.Split(dump.String(),"\n"){
   if line==""{continue}
   if strings.HasPrefix(line,"SET SESSION AUTHORIZATION"){
    if _,err=oracle.ExecContext(p.ctx,`GRANT USAGE,CREATE ON SCHEMA "S" TO drop_table_owner`);err!=nil{t.Fatal(err)}
   }
   if _,err=oracle.ExecContext(p.ctx,line);err!=nil{t.Fatal(line,err)}
  }
 }()
 var schemaOwner,tableOwner string
 if err=referenceAdmin.QueryRowContext(p.ctx,`SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='S'`).Scan(&schemaOwner);err!=nil||schemaOwner!="drop_schema_owner"{t.Fatal(schemaOwner,err)}
 if err=referenceAdmin.QueryRowContext(p.ctx,`SELECT r.rolname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner WHERE n.nspname='S' AND c.relname='T'`).Scan(&tableOwner);err!=nil||tableOwner!="drop_table_owner"{t.Fatal(tableOwner,err)}
 for range 2 {
  if err=stuntgres.Reset(p.ctx,p.native);err!=nil{t.Fatal(err)}
  if err=stuntgres.Restore(p.ctx,p.native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
  p.step(t,referenceSelect(`SELECT * FROM "S"."T"`,[]string{"a","c"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(9)}}))
  for _,db:=range []*sql.DB{p.memory,p.postgres}{
   conn,err:=db.Conn(p.ctx);if err!=nil{t.Fatal(err)}
   func(){defer conn.Close();defer conn.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
    record:=&referenceRecorder{};referenceRunStep(t,p.ctx,conn,record,referenceExec("SET SESSION AUTHORIZATION drop_schema_owner",0))
    for _,prepared:=range []bool{false,true}{failure:=referenceImplicitError(`ALTER TABLE "S"."T" DROP IF EXISTS missing`,referenceDiagnostic{Code:"42501",Message:"must be owner of table T"});failure.errorStage="execute";failure.prepared=prepared;referenceRunStep(t,p.ctx,conn,record,failure)}
   }()
  }
  var again bytes.Buffer;if err=stuntgres.Dump(p.ctx,p.native,&again);err!=nil||again.String()!=dump.String(){t.Fatal(err,again.String())}
 }
}
