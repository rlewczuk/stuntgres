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
func ltreeHierarchyDump(t *testing.T,p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&out); err != nil { t.Fatal(err) }
	return out.Bytes()
}

func ltreeHierarchyRetain(t *testing.T,p *fixture.Pair,b fixture.Backend) (*sql.Rows,[]*sql.ColumnType) {
	t.Helper()
	// This reader borrows a different pooled connection from the scenario's
	// configured session, so every relocated provider reference is qualified.
	rows,err := b.DB.QueryContext(p.Ctx,"SELECT v OPERATOR(lh_provider.@>) 'Top.Science'::lh_provider.ltree AS ancestor,v OPERATOR(lh_provider.<@) 'Top.Science'::lh_provider.ltree AS descendant FROM public.lh_paths ORDER BY id")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ rows.Close() })
	return rows,ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)
}

func ltreeHierarchyConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType) {
	t.Helper()
	want := [][2]any{{true,false},{true,false},{true,true},{false,true},{false,false},{false,false},{false,false},{nil,nil}}
	index := 0
	for rows.Next() {
		var a,d any
		if err := rows.Scan(&a,&d); err != nil { t.Fatal(err) }
		if index >= len(want) || a != want[index][0] || d != want[index][1] { t.Fatalf("retained Boolean row %d: %v/%v",index,a,d) }
		index++
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	if index != len(want) { t.Fatalf("retained Boolean count %d",index) }
	ltreeArrayColumnMetadata(t,fixture.Backend{},columns,[]string{"BOOL","BOOL"},nil)
}

func ltreeHierarchySQLRecreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held fixture.HeldStatement) {
	t.Helper()
	var before,after int64
	q := "SELECT ('"+ltreeHierarchySchema+".ltree'::regtype)::oid"
	if err := c.QueryRowContext(p.Ctx,q).Scan(&before); err != nil { t.Fatal(err) }
	ltreeHierarchyDrop(t,p,b,c)
	ltreeHierarchyControlSetup(t,p,b,c)
	if err := c.QueryRowContext(p.Ctx,q).Scan(&after); err != nil { t.Fatal(err) }
	if before<=0 || after<=0 || before==after { t.Fatalf("recreated identity %d/%d",before,after) }
	ltreeHierarchyControlChecks(t,p,b,c)
	ltreeHierarchyHeldChecks(t,p,b,held)
}

func ltreeHierarchyReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres==nil { return }
	c,err := p.Postgres.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	clean := "DROP SCHEMA IF EXISTS "+ltreeHierarchySchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Fatal(err) }
	defer func(){ if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Error(err) } }()
	if _,err := c.ExecContext(p.Ctx,string(dump)); err != nil { t.Fatalf("actual emitted hierarchy SQL: %v",err) }
	b := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	ltreeHierarchyControlChecks(t,p,b,c)
}

func ltreeHierarchyLifecycle(t *testing.T,phase string) {
	fixture.SchemaReferenceCleanup(t,ltreeHierarchySchema)
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeHierarchyControlSetup(t,p,b,c)
		ltreeHierarchyControlChecks(t,p,b,c)
		q := "SELECT $1::text::ltree @> $2::text::ltree AS ancestor,$1::text::ltree <@ $2::text::ltree AS descendant"
		stmt,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		ltreeHierarchyHeldChecks(t,p,b,held)
		if phase=="recreate" || b.PG {
			ltreeHierarchySQLRecreate(t,p,b,c,held)
			return
		}
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("BEGIN",0))
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("INSERT INTO lh_paths VALUES (99,'PrivateSentinel')",1))
		dump := ltreeHierarchyDump(t,p)
		ltreeHierarchyRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		if strings.Contains(string(dump),"PrivateSentinel") { t.Fatal("Dump included uncommitted row") }
		if first,consumer := bytes.Index(dump,[]byte("CREATE EXTENSION")),bytes.Index(dump,[]byte("CREATE TABLE")); first<0 || consumer<first { t.Fatal("extension must precede consumers") }
		for _, forbidden := range []string{"CREATE TYPE", "CREATE FUNCTION", "CREATE OPERATOR"} {
			if bytes.Contains(dump,[]byte(forbidden)) { t.Fatalf("standalone extension member %s",forbidden) }
		}
		old,columns := ltreeHierarchyRetain(t,p,b)
		if phase=="same" {
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			ltreeHierarchyRun(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'",[]string{"extname"},[]string{"NAME"},[][]any{}))
			if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
			ltreeHierarchyControlChecks(t,p,b,c)
			ltreeHierarchyHeldChecks(t,p,b,held)
			if got := ltreeHierarchyDump(t,p); !bytes.Equal(got,dump) { t.Fatal("stable replacement changed deterministic dump") }
			ltreeHierarchyConsume(t,old,columns)
			ltreeHierarchyReplay(t,p,dump)
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
		ltreeHierarchyControlChecks(t,target,tb,tc)
		ltreeHierarchyControlChecks(t,p,b,c)
		if got := ltreeHierarchyDump(t,p); !bytes.Equal(got,dump) { t.Fatal("destination replacement mutated source") }
		ltreeHierarchyConsume(t,old,columns)
		before := ltreeHierarchyDump(t,target)
		retained,metadata := ltreeHierarchyRetain(t,target,tb)
		var beforeOID,afterOID int64
		identity := "SELECT ('"+ltreeHierarchySchema+".ltree'::regtype)::oid"
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&beforeOID); err != nil { t.Fatal(err) }
		bad := "CREATE EXTENSION ltree; CREATE TABLE staged(v ltree); CREATE VIEW staged_view AS SELECT v @> 'a.b'::ltree AS ancestor FROM staged; INSERT INTO staged VALUES ('a'); INSERT INTO staged VALUES ('a..b');"
		want := fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3")
		want.Position = ltreeSQLPosition(bad,"'a..b'")
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&want)
		canceled,cancel := context.WithCancel(target.Ctx); cancel()
		if err := stuntgres.Restore(canceled,target.Native,bytes.NewReader(dump)); !errors.Is(err,context.Canceled) { t.Fatalf("canceled Restore: %v",err) }
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&afterOID); err != nil { t.Fatal(err) }
		if beforeOID!=afterOID { t.Fatal("failed Restore replaced installed identity") }
		ltreeHierarchyControlChecks(t,target,tb,tc)
		if got := ltreeHierarchyDump(t,target); !bytes.Equal(got,before) { t.Fatal("failed Restore changed deterministic dump") }
		ltreeHierarchyConsume(t,retained,metadata)
	})
}

func TestReferenceLtreeHierarchyControls(t *testing.T) {
	for _, phase := range []string{"same","fresh_and_failure"} {
		t.Run(phase,func(t *testing.T){ ltreeHierarchyLifecycle(t,phase) })
	}
}

func TestReferenceLtreeHierarchyRecreation(t *testing.T) { ltreeHierarchyLifecycle(t,"recreate") }
