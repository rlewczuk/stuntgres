package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func statioSysTableEmpty(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,oid int64) {
	t.Helper()
	if rows:=statioTableRows(t,p,b,e,statioSysTableProbe,oid);len(rows)!=0 {t.Fatal("user identity in system view",rows)}
}

// The released left joins distinguish absent identities from inactive objects.
// Derive the optional-counter NULL shape from actual catalog relationships,
// never from the engine's observed counters or an assumed bootstrap inventory.
func statioSysTableNulls(t *testing.T,p *fixture.Pair,e fixture.Executor,r statioTableRow) {
	t.Helper()
	toast:=originStateNumber(t,p,e,"SELECT reltoastrelid FROM pg_class WHERE oid=$1::oid",r.oid)
	indexes:=originStateNumber(t,p,e,"SELECT count(*) FROM pg_index WHERE indrelid=$1::oid",r.oid)
	var tindexes int64
	if toast!=0 {tindexes=originStateNumber(t,p,e,"SELECT count(*) FROM pg_index WHERE indrelid=$1::oid",toast)}
	for i,v:=range r.blocks {valid:=true;switch i {case 2,3:valid=indexes>0;case 4,5:valid=toast!=0;case 6,7:valid=tindexes>0};if v.Valid!=valid {t.Fatal("exact optional identity NULL shape",i,r,toast,indexes,tindexes)}}
}

func statioSysTablePositive(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,oid int64) statioTableRow {
	t.Helper()
	rows:=statioTableRows(t,p,b,e,statioSysTableProbe,oid)
	if len(rows)!=1 || rows[0].oid!=oid || rows[0].schema!="pg_catalog" || rows[0].name!="pg_class" {t.Fatal("nonempty system identity witness",rows)}
	statioSysTableNulls(t,p,e,rows[0])
	return rows[0]
}

func TestReferencePGStatioSysTablesMembership(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		conn,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer conn.Close()
		statioSequenceExec(t,p,b,conn,"BEGIN");defer conn.ExecContext(context.Background(),"ROLLBACK")
		statioSequenceExec(t,p,b,conn,"SET LOCAL stats_fetch_consistency=snapshot")
		const expectedQuery="SELECT c.oid,n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname IN ('pg_class','pg_attribute') AND c.relkind IN ('r','t','m') AND (n.nspname IN ('pg_catalog','information_schema') OR n.nspname ~ '^pg_toast')"
		rows,err:=conn.QueryContext(p.Ctx,expectedQuery);if err!=nil {t.Fatal(err)}
		want:=map[int64][2]string{}
		for rows.Next() {var oid int64;var schema,name string;if err:=rows.Scan(&oid,&schema,&name);err!=nil {t.Fatal(err)};if _,ok:=want[oid];ok {t.Fatal("duplicate catalog identity")};want[oid]=[2]string{schema,name}}
		if err:=rows.Err();err!=nil {t.Fatal(err)};rows.Close()
		if len(want)!=2 || want[1259]!=[2]string{"pg_catalog","pg_class"} || want[1249]!=[2]string{"pg_catalog","pg_attribute"} {t.Fatal("independently constrained positive catalog bag",want)}
		const q="SELECT * FROM pg_statio_sys_tables WHERE relname IN ('pg_class','pg_attribute')"
		for _,prepared:=range []bool{false,true} {
			var e fixture.Executor=conn
			if prepared {stmt,err:=conn.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();e=fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q}}
			got:=map[int64][2]string{}
			for _,r:=range statioTableRows(t,p,b,e,q) {if _,ok:=got[r.oid];ok {t.Fatal("duplicate sys row",r)};got[r.oid]=[2]string{r.schema,r.name};statioSysTableNulls(t,p,conn,r);if all:=statioTableOne(t,p,b,conn,r.oid);all!=r {t.Fatal("named ALL/SYS eleven-cell snapshot equality",all,r)}}
			if !reflect.DeepEqual(got,want) {t.Fatal("complete constrained unordered bag",got,want)}
		}
		statioSequenceExec(t,p,b,conn,"ROLLBACK")
		// Full ordinary/materialized/partition/view/sequence/index/foreign and
		// TEMP fixtures remain owned by the existing ALL membership scenario,
		// whose named SYS exclusion now executes in both modes.
		statioSequenceExec(t,p,b,conn,"CREATE TABLE statio_sys_members_user(k integer)")
		user:=statioSequenceOID(t,p,conn,"statio_sys_members_user")
		statioTableOne(t,p,b,conn,user)
		statioSysTableEmpty(t,p,b,conn,user)
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Prepared(fixture.Select("SELECT count(*) FROM pg_statio_sys_tables WHERE relid=$1::oid",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}},user)))
	})}
}

// Keep a real prepared statement, not a repeated fresh-prepare wrapper.
func statioSysTableHeld(t *testing.T,p *fixture.Pair,conn *sql.Conn) (fixture.HeldStatement,*sql.Stmt) {
	t.Helper();stmt,err:=conn.PrepareContext(p.Ctx,statioSysTableProbe);if err!=nil {t.Fatal(err)}
	return fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:statioSysTableProbe},stmt
}
