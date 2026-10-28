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

func ltreePathSQLRecreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held fixture.HeldStatement) {
	t.Helper()
	var before,after int64
	q := "SELECT ('"+ltreePathSchema+".ltree'::regtype)::oid"
	if err := c.QueryRowContext(p.Ctx,q).Scan(&before); err != nil { t.Fatal(err) }
	ltreePathDrop(t,p,b,c)
	ltreePathControlSetup(t,p,b,c)
	if err := c.QueryRowContext(p.Ctx,q).Scan(&after); err != nil { t.Fatal(err) }
	if before<=0 || after<=0 || before==after { t.Fatalf("recreated identity %d/%d",before,after) }
	ltreePathControlChecks(t,p,b,c)
	ltreePathHeldChecks(t,p,b,held)
}

func ltreePathReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres==nil { return }
	c,err := p.Postgres.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	clean := "DROP SCHEMA IF EXISTS "+ltreePathSchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Fatal(err) }
	defer func(){ if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Error(err) } }()
 if _,err := c.ExecContext(p.Ctx,string(dump)); err != nil { t.Fatalf("actual emitted path SQL: %v",err) }
	b := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	ltreePathControlChecks(t,p,b,c)
}

func ltreePathLifecycle(t *testing.T,phase string) {
	fixture.SchemaReferenceCleanup(t,ltreePathSchema)
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreePathControlSetup(t,p,b,c)
		ltreePathControlChecks(t,p,b,c)
		q := "SELECT ltree2text(subpath($1::text::ltree,1)) AS v"
		stmt,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		ltreePathHeldChecks(t,p,b,held)
		if phase=="recreate" || b.PG {
			ltreePathSQLRecreate(t,p,b,c,held)
			return
		}
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("BEGIN",0))
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("INSERT INTO lp_paths VALUES (99,'PrivateSentinel','x','{}')",1))
		dump := ltreeHierarchyDump(t,p)
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		if strings.Contains(string(dump),"PrivateSentinel") { t.Fatal("Dump included uncommitted row") }
		if first,consumer := bytes.Index(dump,[]byte("CREATE EXTENSION")),bytes.Index(dump,[]byte("CREATE TABLE")); first<0 || consumer<first { t.Fatal("extension must precede consumers") }
		for _, forbidden := range []string{"CREATE TYPE", "CREATE FUNCTION", "CREATE OPERATOR"} {
			if bytes.Contains(dump,[]byte(forbidden)) { t.Fatalf("standalone extension member %s",forbidden) }
		}
  if count:=bytes.Count(dump,[]byte("ALTER OPERATOR "));count!=3{t.Fatal("moved concatenation replay statements",count)}
		old,columns := ltreePathRetain(t,p,b)
  empty,err:=b.DB.QueryContext(p.Ctx,"SELECT "+ltreePathSchema+".lp_depth(p) AS depth FROM public.lp_paths WHERE false")
  if err!=nil{t.Fatal(err)}
  emptyColumns,err:=empty.ColumnTypes();if err!=nil{empty.Close();t.Fatal(err)}
  ltreePathColumnMetadata(t,b,emptyColumns,[]string{"depth"},[]string{"INT4"},nil)
  t.Cleanup(func(){
   defer empty.Close()
   if empty.Next()||empty.Err()!=nil{t.Fatalf("retained empty reader changed: %v",empty.Err())}
   ltreePathColumnMetadata(t,b,emptyColumns,[]string{"depth"},[]string{"INT4"},nil)
  })
		if phase=="same" {
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			ltreeHierarchyRun(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'",[]string{"extname"},[]string{"NAME"},[][]any{}))
			if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
			ltreePathControlChecks(t,p,b,c)
			ltreePathHeldChecks(t,p,b,held)
			if got := ltreeHierarchyDump(t,p); !bytes.Equal(got,dump) { t.Fatal("stable replacement changed deterministic dump") }
			ltreePathConsume(t,old,columns)
			ltreePathReplay(t,p,dump)
			return
		}
		target := fixture.NewFixture(t,false,true)
		tc,err := target.Memory.Conn(target.Ctx)
		if err != nil { t.Fatal(err) }
		defer tc.Close()
		tb := fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
		ltreeHierarchyRun(t,target,tb,tc,fixture.Exec("CREATE TABLE incumbent(id integer)",0))
		ltreeHierarchyRun(t,target,tb,tc,fixture.Exec("INSERT INTO incumbent VALUES (73)",1))
		incumbent,err := target.Memory.QueryContext(target.Ctx,"SELECT id FROM incumbent")
		if err != nil { t.Fatal(err) }
		defer incumbent.Close()
		if err := stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
		if !incumbent.Next() { t.Fatal("retained incumbent reader lost") }
		var sentinel int
		if err := incumbent.Scan(&sentinel); err != nil || sentinel!=73 { t.Fatalf("incumbent %d: %v",sentinel,err) }
		if incumbent.Next() || incumbent.Err()!=nil { t.Fatalf("incumbent tail: %v",incumbent.Err()) }
		incumbent.Close()
		ltreePathControlChecks(t,target,tb,tc)
		ltreePathControlChecks(t,p,b,c)
		if got := ltreeHierarchyDump(t,p); !bytes.Equal(got,dump) { t.Fatal("destination replacement mutated source") }
		ltreePathConsume(t,old,columns)
		before := ltreeHierarchyDump(t,target)
		retained,metadata := ltreePathRetain(t,target,tb)
		var beforeOID,afterOID int64
		identity := "SELECT ('"+ltreePathSchema+".ltree'::regtype)::oid"
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&beforeOID); err != nil { t.Fatal(err) }
		bad := "CREATE EXTENSION ltree; CREATE TABLE staged(v ltree); CREATE VIEW staged_view AS SELECT subpath(v,1) AS sliced FROM staged; INSERT INTO staged VALUES ('a'); INSERT INTO staged VALUES ('a..b');"
		want := fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3")
		want.Position = ltreeSQLPosition(bad,"'a..b'")
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&want)
		canceled,cancel := context.WithCancel(target.Ctx); cancel()
		if err := stuntgres.Restore(canceled,target.Native,bytes.NewReader(dump)); !errors.Is(err,context.Canceled) { t.Fatalf("canceled Restore: %v",err) }
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&afterOID); err != nil { t.Fatal(err) }
		if beforeOID!=afterOID { t.Fatal("failed Restore replaced installed identity") }
		ltreePathControlChecks(t,target,tb,tc)
		if got := ltreeHierarchyDump(t,target); !bytes.Equal(got,before) { t.Fatal("failed Restore changed deterministic dump") }
		ltreePathConsume(t,retained,metadata)
	})
}

func TestReferenceLtreePathControls(t *testing.T) {
	for _, phase := range []string{"same","fresh_and_failure"} {
		t.Run(phase,func(t *testing.T){ ltreePathLifecycle(t,phase) })
	}
}

func TestReferenceLtreePathRecreation(t *testing.T) { ltreePathLifecycle(t,"recreate") }
