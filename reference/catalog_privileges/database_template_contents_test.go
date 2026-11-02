package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func templateRun(c *databaseCreateCase, db *sql.DB, rec *fixture.Recorder, steps ...fixture.Step) {
	c.t.Helper()
	for _, step := range steps {
		fixture.RunStep(c.t, c.p.Ctx, db, rec, step)
	}
}

func templateRows(query string, rows ...[]any) fixture.Step {
	return fixture.Select(query, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}

func templateAbsent(c *databaseCreateCase,name string) {
	c.t.Helper()
	c.absent(name)
	db,_,_:=c.pool(name,"")
	d:=databaseCreateDiagnostic("3D000","database \""+name+"\" does not exist")
	d.Severity,d.SeverityUnlocalized="FATAL","FATAL"
	fixture.AssertDiagnostic(c.t,db.PingContext(c.p.Ctx),&d)
}

func TestReferenceDatabaseTemplateContents(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		c.create("dt_source", "", false)
		source, _, rec := c.pool("dt_source", "")
		templateRun(c, source, rec,
			fixture.Exec("CREATE SCHEMA dt_s", 0),
			fixture.Exec("CREATE TABLE dt_s.t(id integer PRIMARY KEY,v integer)", 0),
			fixture.Exec("INSERT INTO dt_s.t VALUES (1,7),(2,11)", 2),
			fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO dt_s.t VALUES (9,99)", 1), fixture.Exec("ROLLBACK", 0))
		var sourceOID int64
		if err := source.QueryRowContext(c.p.Ctx, "SELECT 'dt_s.t'::regclass::oid").Scan(&sourceOID); err != nil { c.t.Fatal(err) }
		if err := source.Close(); err != nil { c.t.Fatal(err) }
		for i, name := range []string{"dt_direct", "dt_prepared"} {
			c.create(name, "TEMPLATE dt_source", i == 1)
			clone, _, cloneRec := c.pool(name, "")
			want := templateRows("SELECT id,v FROM dt_s.t ORDER BY id", []any{int64(1),int64(7)}, []any{int64(2),int64(11)})
			templateRun(c, clone, cloneRec, want, fixture.Prepared(want))
			var oid int64
			if err := clone.QueryRowContext(c.p.Ctx, "SELECT 'dt_s.t'::regclass::oid").Scan(&oid); err != nil || oid != sourceOID { c.t.Fatalf("local relation OID %d want %d: %v", oid,sourceOID,err) }
			c.run(fixture.Select("SELECT a.oid<>b.oid AS distinct_database FROM pg_database a,pg_database b WHERE a.datname='dt_source' AND b.datname=$1", []string{"distinct_database"}, []string{"BOOL"}, [][]any{{true}},name))
			templateRun(c, clone, cloneRec, fixture.Exec("DELETE FROM dt_s.t WHERE id=1",1),fixture.Exec("INSERT INTO dt_s.t VALUES (4,17)",1),fixture.Exec("ALTER TABLE dt_s.t ADD COLUMN clone_only integer",0))
			if err := clone.Close(); err != nil { c.t.Fatal(err) }
		}
		source, _, rec = c.pool("dt_source", "")
		templateRun(c, source, rec,
			templateRows("SELECT id,v FROM dt_s.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)}),
			fixture.Select("SELECT column_name FROM information_schema.columns WHERE table_schema='dt_s' AND table_name='t' ORDER BY ordinal_position",[]string{"column_name"},[]string{"NAME"},[][]any{{"id"},{"v"}}),
			fixture.Exec("INSERT INTO dt_s.t VALUES (3,13)",1),
			fixture.Exec("UPDATE dt_s.t SET v=19 WHERE id=2",1),
			fixture.Exec("CREATE TABLE dt_s.source_only(v integer)",0),
			templateRows("SELECT id,v FROM dt_s.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(19)},[]any{int64(3),int64(13)}))
		clone, _, cloneRec := c.pool("dt_direct", "")
		templateRun(c,clone,cloneRec,
			templateRows("SELECT id,v FROM dt_s.t ORDER BY id",[]any{int64(2),int64(11)},[]any{int64(4),int64(17)}),
			fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='dt_s' AND c.relname='source_only'",[]string{"relname"},[]string{"NAME"},nil))
	})
}

func TestReferenceDatabaseTemplateFeatureSmoke(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		c.run(fixture.Exec("CREATE ROLE dc_actor CREATEDB",0))
		c.run(fixture.Exec("CREATE ROLE dc_denied CREATEDB",0))
		c.create("dt_smoke_source","OWNER dc_actor",false)
		source,_,rec:=c.pool("dt_smoke_source","")
		templateRun(c,source,rec,fixture.Exec("CREATE SCHEMA dt_smoke",0),fixture.Exec("CREATE TABLE dt_smoke.t(id integer PRIMARY KEY,v integer)",0),fixture.Exec("INSERT INTO dt_smoke.t VALUES(1,7),(2,11)",2))
		oid:=templateIdentity(c.t,c,source,"SELECT 'dt_smoke.t'::regclass::oid")
		if err:=source.Close();err!=nil {c.t.Fatal(err)}
		c.run(fixture.Exec("SET ROLE dc_actor",0))
		c.create("dt_smoke_clone","TEMPLATE dt_smoke_source",true)
		c.run(fixture.Exec("RESET ROLE",0))
		clone,_,cloneRec:=c.pool("dt_smoke_clone","")
		if got:=templateIdentity(c.t,c,clone,"SELECT 'dt_smoke.t'::regclass::oid");got!=oid {c.t.Fatalf("copied local identity %d want %d",got,oid)}
		c.run(fixture.Select("SELECT a.oid<>b.oid AS distinct_database FROM pg_database a,pg_database b WHERE a.datname='dt_smoke_source' AND b.datname='dt_smoke_clone'",[]string{"distinct_database"},[]string{"BOOL"},[][]any{{true}}))
		templateRun(c,clone,cloneRec,templateRows("SELECT id,v FROM dt_smoke.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)}),fixture.Exec("INSERT INTO dt_smoke.t VALUES(4,17)",1))
		source,_,rec=c.pool("dt_smoke_source","")
		templateRun(c,source,rec,fixture.Exec("INSERT INTO dt_smoke.t VALUES(3,13)",1),templateRows("SELECT id,v FROM dt_smoke.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)},[]any{int64(3),int64(13)}))
		templateRun(c,clone,cloneRec,templateRows("SELECT id,v FROM dt_smoke.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)},[]any{int64(4),int64(17)}))
		c.remember("dt_smoke_busy")
		d:=databaseCreateDiagnostic("55006",`source database "dt_smoke_source" is being accessed by other users`)
		d.Detail="There is 1 other session using the database."
		c.run(fixture.ErrorDiagnostic("CREATE DATABASE dt_smoke_busy TEMPLATE dt_smoke_source",d));templateAbsent(c,"dt_smoke_busy")
		if err:=source.Close();err!=nil {c.t.Fatal(err)}
		c.create("dt_smoke_busy","TEMPLATE dt_smoke_source",false)
		retry,_,retryRec:=c.pool("dt_smoke_busy","")
		templateRun(c,retry,retryRec,templateRows("SELECT id,v FROM dt_smoke.t ORDER BY id",[]any{int64(1),int64(7)},[]any{int64(2),int64(11)},[]any{int64(3),int64(13)}))
		c.run(fixture.Exec("SET ROLE dc_denied",0));c.remember("dt_smoke_denied")
		c.error("CREATE DATABASE dt_smoke_denied TEMPLATE dt_smoke_source","42501",`permission denied to copy database "dt_smoke_source"`)
		templateAbsent(c,"dt_smoke_denied");c.run(fixture.Exec("RESET ROLE",0))
	})
}
