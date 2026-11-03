package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeControls(t *testing.T) {
	stCase(t, func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		stControls(t,p,b,c)
	})
}

func stControls(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE VIEW st_zero AS SELECT id,payload FROM sr TABLESAMPLE system_time(0)",
		"CREATE VIEW st_finite AS SELECT id,payload FROM sr TABLESAMPLE system_time(0.25)",
		"CREATE VIEW st_all AS SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity')",
	} { srRun(t,p,b,c,fixture.Exec(q,0)) }
	stControlPost(t,p,b,c)
	// Native control APIs have no PostgreSQL counterpart. All SQL observations
	// above are shared; no oracle Reset/Dump/Restore is fabricated.
	if b.PG { return }
	stReplay(t,p,b,c)
}

func stControlPost(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
	t.Helper()
	stDeclaration(t,p,b,c)
	srBoth(t,p,b,c,fixture.Select("SELECT id,payload FROM st_zero",srNames,srTypes,nil))
	s := fixture.Select("SELECT id,payload FROM st_all ORDER BY id",srNames,srTypes,srRows())
	s.CheckMetadata = func(t *testing.T,rows *sql.Rows) {
		columns,err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		fixture.TableDDLColumnTypes(t,b.PG,columns,srNames,srTypes,[]bool{true,true})
	}
	srBoth(t,p,b,c,s)
	stSample(t,p.Ctx,c,"SELECT id,payload FROM st_finite",srRows())
	srBoth(t,p,b,c,fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity') ORDER BY id",srNames,srTypes,srRows()))
}

func stReplay(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	q := "SELECT id,payload FROM sr TABLESAMPLE system_time($1) ORDER BY id"
	h := srHeld(t,p,c,q)
	held := func() {
		srRun(t,p,b,h,srMetadata(fixture.Select(q,srNames,srTypes,srRows(),"Infinity"),false))
		srRun(t,p,b,h,srMetadata(fixture.Select(q,srNames,srTypes,nil,float64(0)),false))
		stSample(t,p.Ctx,h,q,srRows(),0.25)
	}
	held()
	var original, handler int64
	if err := c.QueryRowContext(p.Ctx,"SELECT 'sr'::regclass::oid,'system_time(internal)'::regprocedure::oid").Scan(&original,&handler); err != nil || original<=0 || handler<=0 { t.Fatal("source identities",original,handler,err) }
	peer,err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer peer.Close()
	tx,err := peer.BeginTx(p.Ctx,nil)
	if err != nil { t.Fatal(err) }
	defer tx.Rollback()
	srRun(t,p,b,tx,fixture.Exec("INSERT INTO sr VALUES(99,'uncommitted')",1))
	dump := fixture.ExtensionDump(t,p.Ctx,p.Native)
	if err := tx.Rollback(); err != nil { t.Fatal(err) }
	if err := peer.Close(); err != nil { t.Fatal(err) }
	retained,err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	old,err := retained.QueryContext(p.Ctx,"SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity') ORDER BY id")
	if err != nil { t.Fatal(err) }
	defer old.Close()
	metadata,err := old.ColumnTypes()
	if err != nil { t.Fatal(err) }
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	fixture.TableDDLColumnTypes(t,false,metadata,srNames,srTypes,[]bool{false,true})
	for id := int64(1); id<=10; id++ {
		if !old.Next() { t.Fatal("retained sample truncated",id,old.Err()) }
		var got int64
		var payload string
		if err := old.Scan(&got,&payload); err != nil || got!=id || payload!=fmt.Sprintf("v%d",id) { t.Fatal("retained occurrence",got,payload,err) }
	}
	if old.Next() || old.Err()!=nil { t.Fatal("retained sample tail",old.Err()) }
	if err := old.Close(); err != nil { t.Fatal(err) }
	if err := retained.Close(); err != nil { t.Fatal(err) }
	srRun(t,p,b,c,srCount("SELECT count(*) AS n FROM pg_extension WHERE extname='tsm_system_time'",0))
	if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump)); err != nil { t.Fatal(err) }
	stControlPost(t,p,b,c)
	held()
	// Native dump is symbolic SQL. Round-trip equality excludes persistent
	// invocation clocks/progress while executable views prove genuine rebinding.
	before := fixture.ExtensionDump(t,p.Ctx,p.Native)
	if before != dump { t.Fatal("symbolic replay changed deterministic dump") }
	if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump+"\nINVALID SQL;")); err==nil { t.Fatal("invalid restore accepted") }
	if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before { t.Fatal("failed restore published partial state") }
	stControlPost(t,p,b,c)
	held()
	ctx,cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel})
	cancel()
	if !errors.Is(err,context.Canceled) { t.Fatal("canceled restore",err) }
	if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before { t.Fatal("canceled restore published partial state") }
	stControlPost(t,p,b,c)
	held()
	connector,err := stuntgres.NewDriver().OpenConnector("system-time-independent-destination")
	if err != nil { t.Fatal(err) }
	raw := sql.OpenDB(connector)
	defer raw.Close()
	pool,recorder := fixture.RecordedNativePool(t,connector)
	dest,err := pool.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer dest.Close()
	db := fixture.Backend{Name:"destination",DB:pool,Recorder:recorder}
	// Bound the real identity-collision setup; original OIDs are runtime
	// identities, never another backend's allocator golden.
	occupied := false
	for n := range 4096 {
		name := fmt.Sprintf("st_destination_pad_%d",n)
		srRun(t,p,db,dest,fixture.Exec("CREATE TABLE "+name+"(v integer)",0))
		var oid int64
		if err := dest.QueryRowContext(p.Ctx,"SELECT $1::regclass::oid",name).Scan(&oid); err != nil { t.Fatal(err) }
		if oid>=max(original,handler) { occupied=true; break }
	}
	if !occupied { t.Fatal("destination identity collision setup exceeded bound") }
	if err := stuntgres.Restore(p.Ctx,raw,strings.NewReader(dump)); err != nil { t.Fatal(err) }
	var remapped, remappedHandler int64
	if err := dest.QueryRowContext(p.Ctx,"SELECT 'sr'::regclass::oid,'system_time(internal)'::regprocedure::oid").Scan(&remapped,&remappedHandler); err!=nil || remapped<=0 || remapped==original || remappedHandler<=0 || remappedHandler==handler { t.Fatal("independent destination identities",original,remapped,handler,remappedHandler,err) }
	stControlPost(t,p,db,dest)
	srRun(t,p,db,dest,fixture.Exec("DELETE FROM sr WHERE id=1",1))
	srBoth(t,p,db,dest,srCount("SELECT count(*) AS n FROM st_all",9))
	stControlPost(t,p,b,c)
	held()
}
