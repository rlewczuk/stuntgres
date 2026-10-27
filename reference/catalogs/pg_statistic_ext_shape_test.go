package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func statisticExtShape(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	run := func(s fixture.Step) { fixture.OidReferenceRows(t,p.Ctx,b,db,s) }
	run(fixture.Select("SELECT oid,relname,relnamespace,relkind,relpersistence,relnatts,relhasindex,relowner FROM pg_catalog.pg_class WHERE oid=3381", []string{"oid","relname","relnamespace","relkind","relpersistence","relnatts","relhasindex","relowner"}, []string{"OID","NAME","OID","CHAR","CHAR","INT2","BOOL","OID"}, [][]any{{int64(3381),"pg_statistic_ext",int64(11),"r","p",int64(9),true,int64(10)}}))
	attrs := [][]any{
		{int64(1),"oid",int64(26),true,int64(0),int64(0),int64(-1),int64(4)},
		{int64(2),"stxrelid",int64(26),true,int64(0),int64(0),int64(-1),int64(4)},
		{int64(3),"stxname",int64(19),true,int64(0),int64(950),int64(-1),int64(64)},
		{int64(4),"stxnamespace",int64(26),true,int64(0),int64(0),int64(-1),int64(4)},
		{int64(5),"stxowner",int64(26),true,int64(0),int64(0),int64(-1),int64(4)},
		{int64(6),"stxkeys",int64(22),true,int64(1),int64(0),int64(-1),int64(-1)},
		{int64(7),"stxstattarget",int64(21),false,int64(0),int64(0),int64(-1),int64(2)},
		{int64(8),"stxkind",int64(1002),true,int64(1),int64(0),int64(-1),int64(-1)},
		{int64(9),"stxexprs",int64(194),false,int64(0),int64(950),int64(-1),int64(-1)},
	}
	run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,attndims,attcollation,atttypmod,attlen FROM pg_catalog.pg_attribute WHERE attrelid=3381 AND attnum>0 ORDER BY attnum", []string{"attnum","attname","atttypid","attnotnull","attndims","attcollation","atttypmod","attlen"}, []string{"INT2","NAME","OID","BOOL","INT2","OID","INT4","INT2"},attrs))
	owner := "postgres"; if b.PG { owner = "stuntgres" }
	fixture.RunStep(t,p.Ctx,db,b.Recorder,fixture.Select("SELECT schemaname,tablename,tableowner,hasindexes,hasrules,hastriggers,rowsecurity FROM pg_catalog.pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_statistic_ext'", []string{"schemaname","tablename","tableowner","hasindexes","hasrules","hastriggers","rowsecurity"}, []string{"NAME","NAME","NAME","BOOL","BOOL","BOOL","BOOL"}, [][]any{{"pg_catalog","pg_statistic_ext",owner,true,false,false,false}}))
}

func TestReferencePGStatisticExtShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		statisticExtShape(t,p,b,conn)
		row := fixture.DeparseDefinitionID(t,conn,"SELECT reltype FROM pg_catalog.pg_class WHERE oid=3381")
		array := fixture.DeparseDefinitionID(t,conn,"SELECT typarray FROM pg_catalog.pg_type WHERE oid=$1",row)
		if row <= 0 || array <= 0 || row == array { t.Fatal("invalid composite identities",row,array) }
		fixture.OidReferenceRows(t,p.Ctx,b,conn,fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typarray FROM pg_catalog.pg_type WHERE oid=$1",[]string{"typname","typtype","typrelid","typnamespace","typarray"},[]string{"NAME","CHAR","OID","OID","OID"},[][]any{{"pg_statistic_ext","c",int64(3381),int64(11),array}},row))
		fixture.OidReferenceRows(t,p.Ctx,b,conn,fixture.Select("SELECT typname,typelem,typnamespace FROM pg_catalog.pg_type WHERE oid=$1",[]string{"typname","typelem","typnamespace"},[]string{"NAME","OID","OID"},[][]any{{"_pg_statistic_ext",row,int64(11)}},array))
	}) }
}
