package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ltreeLqueryReplay(t *testing.T,p *fixture.Pair,dump []byte){
	t.Helper()
	if p.Postgres==nil{return}
	c,err:=p.Postgres.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
	clean:="DROP SCHEMA IF EXISTS "+ltreeLquerySchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _,err:=c.ExecContext(p.Ctx,clean);err!=nil{t.Fatal(err)}
	defer func(){if _,err:=c.ExecContext(p.Ctx,clean);err!=nil{t.Error(err)}}()
	if _,err:=c.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatalf("actual emitted scalar lquery SQL: %v",err)}
	ltreeLqueryControlChecks(t,p,fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord},c)
}

const ltreeLqueryBadRestoreSQL="CREATE EXTENSION ltree; CREATE TABLE staged(path ltree,matched boolean); CREATE VIEW staged_view AS SELECT path ~ 'a'::lquery AS matched FROM staged; INSERT INTO staged VALUES ('a',true); INSERT INTO staged VALUES ('a','a'::ltree ~ 'a..b'::lquery);"

func ltreeLqueryLifecycle(t *testing.T,phase string){
	fixture.SchemaReferenceCleanup(t,ltreeLquerySchema)
	fixture.SchemaReferenceCleanup(t,"lq_provider_renamed")
	ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeLqueryControlSetup(t,p,b,c);ltreeLqueryControlChecks(t,p,b,c)
		stmt,err:=c.PrepareContext(p.Ctx,ltreeLqueryHeldSQL);if err!=nil{t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:ltreeLqueryHeldSQL};ltreeLqueryHeldChecks(t,p,b,held)
		literalSQL:="SELECT 'a'::ltree ~ 'a'::lquery AS forward,'a'::lquery ~ 'a'::ltree AS reverse"
		literal,err:=c.PrepareContext(p.Ctx,literalSQL);if err!=nil{t.Fatal(err)};defer literal.Close()
		literalHeld:=fixture.HeldStatement{Executor:c,Statement:literal,SQL:literalSQL}
		ltreeLqueryRun(t,p,b,literalHeld,ltreeLqueryPair(b,literalSQL,true))
		if phase=="recreate"||b.PG{
			var before,after int64
			identity:="SELECT ('lq_provider.ltree'::regtype)::oid"
			if err:=c.QueryRowContext(p.Ctx,identity).Scan(&before);err!=nil{t.Fatal(err)}
			ltreeLqueryDrop(t,p,b,c)
			if b.PG && phase=="fresh_and_failure" {
				ltreeLqueryRun(t,p,b,c,fixture.Exec("BEGIN",0))
				want:=fixture.ControlsReferenceDiagnostic("42601","lquery syntax error at character 3");want.Position=lquerySQLPosition(ltreeLqueryBadRestoreSQL,"'a..b'")
				_,err:=c.ExecContext(p.Ctx,ltreeLqueryBadRestoreSQL);fixture.AssertDiagnostic(t,err,&want)
				ltreeLqueryRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
			}
			ltreeLqueryControlSetup(t,p,b,c)
			if err:=c.QueryRowContext(p.Ctx,identity).Scan(&after);err!=nil{t.Fatal(err)}
			if before<=0||after<=0||before==after{t.Fatalf("recreated type identity %d/%d",before,after)}
			ltreeLqueryControlChecks(t,p,b,c);ltreeLqueryHeldChecks(t,p,b,held)
			ltreeLqueryRun(t,p,b,literalHeld,ltreeLqueryPair(b,literalSQL,true));return
		}
		ltreeLqueryRun(t,p,b,c,fixture.Exec("BEGIN",0));ltreeLqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lq_paths VALUES (99,'PrivateSentinel')",1))
		dump:=ltreeHierarchyDump(t,p)
		ltreeLqueryRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		if bytes.Contains(dump,[]byte("PrivateSentinel")){t.Fatal("Dump included private row")}
		if first,consumer:=bytes.Index(dump,[]byte("CREATE EXTENSION")),bytes.Index(dump,[]byte("CREATE TABLE"));first<0||consumer<first{t.Fatal("extension must precede scalar consumers")}
		for _,forbidden:=range []string{"CREATE TYPE","CREATE FUNCTION","CREATE OPERATOR"}{if bytes.Contains(dump,[]byte(forbidden)){t.Fatalf("standalone provider member %s",forbidden)}}
		old,columns:=ltreeLqueryRetain(t,p,b)
		if phase=="same"{
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
			ltreeLqueryControlChecks(t,p,b,c);ltreeLqueryHeldChecks(t,p,b,held);ltreeLqueryRun(t,p,b,literalHeld,ltreeLqueryPair(b,literalSQL,true))
			if got:=ltreeHierarchyDump(t,p);!bytes.Equal(got,dump){
				offset:=0
				for offset<min(len(dump),len(got)) && dump[offset]==got[offset]{offset++}
				t.Fatalf("same-engine deterministic dump changed at byte %d (length %d -> %d): before %q; after %q",offset,len(dump),len(got),dump[offset:min(offset+120,len(dump))],got[offset:min(offset+120,len(got))])
			}
			ltreeLqueryConsume(t,old,columns);ltreeLqueryReplay(t,p,dump);return
		}
		target:=fixture.NewFixture(t,false,true)
		tc,err:=target.Memory.Conn(target.Ctx);if err!=nil{t.Fatal(err)};defer tc.Close()
		tb:=fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
		ltreeLqueryRun(t,target,tb,tc,fixture.Exec("CREATE TABLE incumbent(id integer)",0));ltreeLqueryRun(t,target,tb,tc,fixture.Exec("INSERT INTO incumbent VALUES (73)",1))
		incumbent,err:=target.Memory.QueryContext(target.Ctx,"SELECT id FROM incumbent");if err!=nil{t.Fatal(err)};defer incumbent.Close()
		if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
		if !incumbent.Next(){t.Fatal("retained incumbent missing")};var sentinel int
		if err:=incumbent.Scan(&sentinel);err!=nil||sentinel!=73{t.Fatalf("incumbent %d: %v",sentinel,err)}
		if incumbent.Next()||incumbent.Err()!=nil{t.Fatalf("incumbent tail: %v",incumbent.Err())};incumbent.Close()
		ltreeLqueryControlChecks(t,target,tb,tc);ltreeLqueryControlChecks(t,p,b,c)
		if got:=ltreeHierarchyDump(t,p);!bytes.Equal(got,dump){t.Fatal("destination changed source")};ltreeLqueryConsume(t,old,columns)
		beforeDump:=ltreeHierarchyDump(t,target);retained,metadata:=ltreeLqueryRetain(t,target,tb)
		var beforeOID,afterOID int64;identity:="SELECT ('lq_provider.ltree'::regtype)::oid"
		if err:=tc.QueryRowContext(target.Ctx,identity).Scan(&beforeOID);err!=nil{t.Fatal(err)}
		bad:=ltreeLqueryBadRestoreSQL
		want:=fixture.ControlsReferenceDiagnostic("42601","lquery syntax error at character 3");want.Position=lquerySQLPosition(bad,"'a..b'")
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&want)
		canceled,cancel:=context.WithCancel(target.Ctx);cancel()
		if err:=stuntgres.Restore(canceled,target.Native,bytes.NewReader(dump));!errors.Is(err,context.Canceled){t.Fatalf("canceled Restore: %v",err)}
		if err:=tc.QueryRowContext(target.Ctx,identity).Scan(&afterOID);err!=nil{t.Fatal(err)}
		if beforeOID!=afterOID{t.Fatal("failed Restore changed identity")}
		ltreeLqueryControlChecks(t,target,tb,tc)
		if got:=ltreeHierarchyDump(t,target);!bytes.Equal(got,beforeDump){t.Fatal("failed Restore changed incumbent dump")};ltreeLqueryConsume(t,retained,metadata)
	})
}

func TestReferenceLtreeLqueryControls(t *testing.T){
	for _,phase:=range []string{"same","fresh_and_failure"}{t.Run(phase,func(t *testing.T){ltreeLqueryLifecycle(t,phase)})}
}

func TestReferenceLtreeLqueryRecreation(t *testing.T){ltreeLqueryLifecycle(t,"recreate")}
