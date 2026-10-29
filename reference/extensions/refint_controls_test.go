package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func refintPersistence(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
	run(fixture.Exec(`CREATE SCHEMA "Refint Q"`,0))
	run(fixture.Exec(`CREATE TABLE "Refint Q"."Parent Q"("parent key" int)`,0))
	run(fixture.Exec(`CREATE TABLE "Refint Q"."Child Q"(id int,"child key" int)`,0))
	run(fixture.Exec(`INSERT INTO "Refint Q"."Parent Q" VALUES(1)`,1))
	run(fixture.Exec(`INSERT INTO "Refint Q"."Child Q" VALUES(10,1)`,1))
	run(fixture.Exec(`CREATE TRIGGER saved_guard AFTER UPDATE ON "Refint Q"."Parent Q" FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','c','parent key','"Refint Q"."Child Q"','"child key"')`,0))
	q := `UPDATE "Refint Q"."Parent Q" SET "parent key"=$1 WHERE "parent key"=$2`
	held,err := c.PrepareContext(p.Ctx,q); if err!=nil { t.Fatal(err) }; defer held.Close()
	assert := func(want int64) {
		for _,prepared := range []bool{false,true} {
			s := fixture.Select(`SELECT id,"child key" FROM "Refint Q"."Child Q" ORDER BY id`,[]string{"id","child key"},[]string{"INT4","INT4"},[][]any{{int64(10),want}})
			s.Prepared=prepared; run(s)
		}
	}
	assert(1)
	if b.PG {
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},r,fixture.Exec(q,1,2,1))
		refintNotices(t,notices,`saved_guard: 1 tuple(s) of "Refint Q"."Child Q" are updated`)
		assert(2)
		return
	}
	old,err := c.QueryContext(p.Ctx,`SELECT id,"child key" FROM "Refint Q"."Child Q" ORDER BY id`)
	if err!=nil { t.Fatal(err) }; defer old.Close()
	for round := range 2 {
		dump := fixture.ExtensionDump(t,p.Ctx,p.Native)
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil { t.Fatal(err) }
		assert(int64(round+1)); refintNotices(t,notices)
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:held,SQL:q},r,fixture.Exec(q,1,round+2,round+1))
		refintNotices(t,notices,`saved_guard: 1 tuple(s) of "Refint Q"."Child Q" are updated`)
		assert(int64(round+2))
	}
	if !old.Next() { t.Fatal("lost eager saved row",old.Err()) }
	var id,key int64
	if err:=old.Scan(&id,&key);err!=nil || id!=10 || key!=1 { t.Fatal("retained values",id,key,err) }
	if old.Next() || old.Err()!=nil { t.Fatal("retained tail",old.Err()) }
	old.Close()
	before := fixture.ExtensionDump(t,p.Ctx,p.Native)
	if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(before+"\nINVALID SQL;"));err==nil { t.Fatal("invalid restore accepted") }
	if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before { t.Fatal("failed restore changed state") }
	ctx,cancel := context.WithCancel(p.Ctx)
	err=stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel}); cancel()
	if !errors.Is(err,context.Canceled) || fixture.ExtensionDump(t,p.Ctx,p.Native)!=before { t.Fatal("canceled restore",err) }
	assert(3)
	// Exact same DSN on independent Drivers must not share restored objects.
	var isolated *sql.DB
	for index := range 2 {
		connector, err := stuntgres.NewDriver().OpenConnector("refint-isolation/"+t.Name())
		if err != nil { t.Fatal(err) }
		raw := sql.OpenDB(connector); defer raw.Close()
		recorded, record := fixture.RecordedNativePool(t, connector); defer recorded.Close()
		if index == 0 {
			if err := stuntgres.Restore(p.Ctx,raw,strings.NewReader(before)); err != nil { t.Fatal(err) }
			isolated = recorded
			fixture.RunStep(t,p.Ctx,recorded,record,fixture.Exec(q,1,4,3))
			fixture.RunStep(t,p.Ctx,recorded,record,fixture.Select(`SELECT id,"child key" FROM "Refint Q"."Child Q" ORDER BY id`,[]string{"id","child key"},[]string{"INT4","INT4"},[][]any{{int64(10),int64(4)}}))
		} else {
			fixture.RunStep(t,p.Ctx,recorded,record,fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='refint'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
			if err := stuntgres.Restore(p.Ctx,raw,strings.NewReader(before)); err != nil { t.Fatal(err) }
			fixture.RunStep(t,p.Ctx,recorded,record,fixture.Select(`SELECT id,"child key" FROM "Refint Q"."Child Q" ORDER BY id`,[]string{"id","child key"},[]string{"INT4","INT4"},[][]any{{int64(10),int64(3)}}))
		}
	}
	var retained int64
	if err := isolated.QueryRowContext(p.Ctx,`SELECT "child key" FROM "Refint Q"."Child Q"`).Scan(&retained); err != nil || retained != 4 { t.Fatal("independent-driver retained action",retained,err) }
	assert(3)
}

func TestReferenceRefintPersistence(t *testing.T) { refintCase(t,true,refintPersistence) }

func TestReferenceRefintIdentity(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TABLE p(id int)",0)); run(fixture.Exec("INSERT INTO p VALUES(1)",1))
		run(fixture.Exec("CREATE TABLE c(pid int)",0))
		run(fixture.Exec("CREATE TRIGGER bound_guard AFTER INSERT ON c FOR EACH ROW EXECUTE FUNCTION check_primary_key('pid','p','id')",0))
		run(fixture.Exec("ALTER FUNCTION check_primary_key() RENAME TO genuine_primary",0))
		run(fixture.Exec("CREATE FUNCTION check_primary_key() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",0))
		d:=refintError("09000","tuple references non-existent key"); d.Detail=`Trigger "bound_guard" found tuple referencing non-existent key in "p".`
		assert := func() { run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(2)",d)); run(fixture.Select("SELECT count(*) FROM c",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}})) }
		assert()
		if !b.PG { dump:=fixture.ExtensionDump(t,p.Ctx,p.Native); if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }; if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil { t.Fatal(err) }; assert() }
		run(fixture.Exec("ALTER TABLE p RENAME TO renamed_p",0))
		missing := refintError("42P01", `relation "p" does not exist`)
		missing.InternalPosition, missing.InternalQuery = "15", "select 1 from p where id = $1 "
		run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(1)", missing))
		refintNotices(t,notices)
	})
}

func TestReferenceRefintFeatureSmoke(t *testing.T) {
	refintCase(t,true,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic) {
		refintCascadeUpdates(t,p,c,r,notices)
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
		run(fixture.Exec("CREATE TRIGGER smoke_primary AFTER INSERT ON c FOR EACH ROW EXECUTE FUNCTION check_primary_key('pid','p','id')",0))
		run(fixture.Exec("INSERT INTO c VALUES(30,33)",1))
		d:=refintError("09000","tuple references non-existent key"); d.Detail=`Trigger "smoke_primary" found tuple referencing non-existent key in "p".`
		run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(40,999)",d))
		run(fixture.Exec("CREATE TRIGGER smoke_restrict AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','r','id','c','pid')",0))
		run(fixture.ErrorDiagnostic("DELETE FROM p WHERE id=33",refintError("09000",`"smoke_restrict": tuple is referenced in "c"`)))
		run(fixture.Exec("DROP TRIGGER smoke_restrict ON p",0))
		run(fixture.Exec("CREATE TRIGGER smoke_clear AFTER DELETE ON p FOR EACH ROW EXECUTE FUNCTION check_foreign_key('1','s','id','c','pid')",0))
		run(fixture.Exec("DELETE FROM p WHERE id=33",1)); refintNotices(t,notices,"smoke_clear: 2 tuple(s) of c are set to null")
		refintPersistence(t,p,b,c,r,notices)
	})
}
