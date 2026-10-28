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
func ltreeLtxtqueryDump(t *testing.T,p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&out); err != nil { t.Fatal(err) }
	return out.Bytes()
}

func ltreeLtxtqueryRetain(t *testing.T,p *fixture.Pair,b fixture.Backend) (*sql.Rows,[]*sql.ColumnType) {
	t.Helper()
	// This reader borrows a different pooled connection from the scenario's
	// configured session, so every relocated provider reference is qualified.
	rows,err := b.DB.QueryContext(p.Ctx,"SELECT v OPERATOR(lxq_provider.@) 'Europe & Russia'::lxq_provider.ltxtquery AS forward,'Europe & Russia'::lxq_provider.ltxtquery OPERATOR(lxq_provider.@) v AS reverse FROM public.lxq_paths ORDER BY id")
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ rows.Close() })
	return rows,ltreeArrayMetadata(t,b,rows,[]string{"BOOL","BOOL"},nil)
}

func ltreeLtxtqueryConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType) {
	t.Helper()
	want := [][2]any{{true,true},{true,true},{false,false},{false,false},{false,false},{false,false},{nil,nil},{true,true}}
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

func ltreeLtxtquerySQLRecreate(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held fixture.HeldStatement) {
	t.Helper()
	var before,after int64
	q := "SELECT ('"+ltreeLtxtquerySchema+".ltree'::regtype)::oid"
	if err := c.QueryRowContext(p.Ctx,q).Scan(&before); err != nil { t.Fatal(err) }
	ltreeLtxtqueryDrop(t,p,b,c)
	ltreeLtxtqueryControlSetup(t,p,b,c)
	if err := c.QueryRowContext(p.Ctx,q).Scan(&after); err != nil { t.Fatal(err) }
	if before<=0 || after<=0 || before==after { t.Fatalf("recreated identity %d/%d",before,after) }
	ltreeLtxtqueryControlChecks(t,p,b,c)
	ltreeLtxtqueryHeldChecks(t,p,b,held)
}

func ltreeLtxtqueryReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres==nil { return }
	c,err := p.Postgres.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	clean := "DROP SCHEMA IF EXISTS "+ltreeLtxtquerySchema+" CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Fatal(err) }
	defer func(){ if _,err := c.ExecContext(p.Ctx,clean); err != nil { t.Error(err) } }()
	if _,err := c.ExecContext(p.Ctx,string(dump)); err != nil { t.Fatalf("actual emitted ltxtquery SQL: %v",err) }
	b := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	ltreeLtxtqueryControlChecks(t,p,b,c)
}

func ltreeLtxtqueryLifecycle(t *testing.T,phase string) {
	fixture.SchemaReferenceCleanup(t,ltreeLtxtquerySchema)
	ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeLtxtqueryControlSetup(t,p,b,c)
		ltreeLtxtqueryControlChecks(t,p,b,c)
		q := "SELECT $1::text::lxq_provider.ltree OPERATOR(lxq_provider.@) $2::text::lxq_provider.ltxtquery AS forward,$2::text::lxq_provider.ltxtquery OPERATOR(lxq_provider.@) $1::text::lxq_provider.ltree AS reverse"
		stmt,err := c.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		ltreeLtxtqueryHeldChecks(t,p,b,held)
		if phase=="recreate" || b.PG {
			ltreeLtxtquerySQLRecreate(t,p,b,c,held)
			return
		}
		ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("BEGIN",0))
		ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("INSERT INTO lxq_paths VALUES (99,'PrivateSentinel')",1))
		dump := ltreeLtxtqueryDump(t,p)
		ltreeLtxtqueryRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		if strings.Contains(string(dump),"PrivateSentinel") { t.Fatal("Dump included uncommitted row") }
		if first,consumer := bytes.Index(dump,[]byte("CREATE EXTENSION")),bytes.Index(dump,[]byte("CREATE TABLE")); first<0 || consumer<first { t.Fatal("extension must precede consumers") }
		for _, forbidden := range []string{"CREATE TYPE", "CREATE FUNCTION", "CREATE OPERATOR"} {
			if bytes.Contains(dump,[]byte(forbidden)) { t.Fatalf("standalone extension member %s",forbidden) }
		}
		old,columns := ltreeLtxtqueryRetain(t,p,b)
		if phase=="same" {
			if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
			ltreeLtxtqueryRun(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'",[]string{"extname"},[]string{"NAME"},[][]any{}))
			if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
			ltreeLtxtqueryControlChecks(t,p,b,c)
			ltreeLtxtqueryHeldChecks(t,p,b,held)
			if got := ltreeLtxtqueryDump(t,p); !bytes.Equal(got,dump) { t.Fatalf("stable replacement changed deterministic dump\nbefore:\n%s\nafter:\n%s",dump,got) }
			ltreeLtxtqueryConsume(t,old,columns)
			ltreeLtxtqueryReplay(t,p,dump)
			return
		}
		target := fixture.NewFixture(t,false,true)
		tc,err := target.Memory.Conn(target.Ctx)
		if err != nil { t.Fatal(err) }
		defer tc.Close()
		tb := fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
		ltreeLtxtqueryRun(t,target,tb,tc,fixture.Exec("CREATE TABLE incumbent(id integer)",0))
		ltreeLtxtqueryRun(t,target,tb,tc,fixture.Exec("INSERT INTO incumbent VALUES (73)",1))
		incumbent,err := target.Memory.QueryContext(target.Ctx,"SELECT id FROM incumbent")
		if err != nil { t.Fatal(err) }
		defer incumbent.Close()
		if err := stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
		if !incumbent.Next() { t.Fatal("retained incumbent reader lost") }
		var sentinel int
		if err := incumbent.Scan(&sentinel); err != nil || sentinel!=73 { t.Fatalf("incumbent %d: %v",sentinel,err) }
		if incumbent.Next() || incumbent.Err()!=nil { t.Fatalf("incumbent tail: %v",incumbent.Err()) }
		incumbent.Close()
		ltreeLtxtqueryControlChecks(t,target,tb,tc)
		ltreeLtxtqueryControlChecks(t,p,b,c)
		if got := ltreeLtxtqueryDump(t,p); !bytes.Equal(got,dump) { t.Fatal("destination replacement mutated source") }
		ltreeLtxtqueryConsume(t,old,columns)
		before := ltreeLtxtqueryDump(t,target)
		retained,metadata := ltreeLtxtqueryRetain(t,target,tb)
		var beforeOID,afterOID int64
		identity := "SELECT ('"+ltreeLtxtquerySchema+".ltree'::regtype)::oid"
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&beforeOID); err != nil { t.Fatal(err) }
		bad := "CREATE EXTENSION ltree; CREATE TABLE staged(v boolean); CREATE VIEW staged_view AS SELECT 'a'::ltree @ 'a'::ltxtquery AS forward; INSERT INTO staged VALUES ('a'::ltree @ 'a'::ltxtquery); INSERT INTO staged VALUES ('a'::ltree @ 'foo@bar'::ltxtquery);"
		want := fixture.ControlsReferenceDiagnostic("42601","modifiers syntax error")
		want.Position = ltreeSQLPosition(bad,"'foo@bar'")
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&want)
		canceled,cancel := context.WithCancel(target.Ctx); cancel()
		if err := stuntgres.Restore(canceled,target.Native,bytes.NewReader(dump)); !errors.Is(err,context.Canceled) { t.Fatalf("canceled Restore: %v",err) }
		if err := tc.QueryRowContext(target.Ctx,identity).Scan(&afterOID); err != nil { t.Fatal(err) }
		if beforeOID!=afterOID { t.Fatal("failed Restore replaced installed identity") }
		ltreeLtxtqueryControlChecks(t,target,tb,tc)
		if got := ltreeLtxtqueryDump(t,target); !bytes.Equal(got,before) { t.Fatal("failed Restore changed deterministic dump") }
		ltreeLtxtqueryConsume(t,retained,metadata)
	})
}

func TestReferenceLtreeLtxtqueryControls(t *testing.T) {
	for _, phase := range []string{"same","fresh_and_failure"} {
		t.Run(phase,func(t *testing.T){ ltreeLtxtqueryLifecycle(t,phase) })
	}
 t.Run("presentation",ltreeLtxtqueryPresentation)
}

func TestReferenceLtreeLtxtqueryRecreation(t *testing.T) { ltreeLtxtqueryLifecycle(t,"recreate") }
