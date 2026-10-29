package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAutoincPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"ai_saved")
	triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		userOperatorExec(t,p,b,c,
			"CREATE SCHEMA ai_saved","CREATE EXTENSION autoinc SCHEMA ai_saved",
			`CREATE SEQUENCE ai_saved."S ' One" START 10 INCREMENT 10`,
			`CREATE SEQUENCE ai_saved."S Two" START 100`,
			`CREATE TABLE ai_saved."Saved Table"("Key ' One" integer,k integer,label text)`,
			`CREATE TRIGGER "a ' keys" BEFORE INSERT OR UPDATE ON ai_saved."Saved Table" FOR EACH ROW EXECUTE FUNCTION ai_saved.autoinc('Key '' One','ai_saved."S '' One"','k','ai_saved."S Two"')`)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(`INSERT INTO ai_saved."Saved Table" VALUES(NULL,0,'generated'),(7,8,'manual')`,2))
		userOperatorExec(t,p,b,c,
			`ALTER FUNCTION ai_saved.autoinc() RENAME TO "Real ' Helper"`,
			"CREATE FUNCTION ai_saved.autoinc() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.k:=999; RETURN NEW; END$$")
		q:=`INSERT INTO ai_saved."Saved Table" VALUES($1,$2,$3)`
		writer,err:=c.PrepareContext(p.Ctx,q);if err!=nil { t.Fatal(err) };defer writer.Close()
		selectSQL:=`SELECT "Key ' One" AS id,k,label FROM ai_saved."Saved Table" ORDER BY label`
		rowsStep:=func(query string,rows ...[]any) fixture.Step { return fixture.Select(query,[]string{"id","k","label"},[]string{"INT4","INT4","TEXT"},rows) }
		check:=func(conn *sql.Conn,backend fixture.Backend) {
			run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,backend.Recorder,s) }
			run(rowsStep(selectSQL,[]any{int64(10),int64(100),"generated"},[]any{int64(7),int64(8),"manual"}))
			run(rowsStep(selectSQL+" LIMIT 0"))
			run(autoincScalar(`SELECT last_value FROM ai_saved."S ' One"`,"last_value",10))
			run(autoincScalar(`SELECT last_value FROM ai_saved."S Two"`,"last_value",100))
			run(fixture.Select(`SELECT t.tgfoid=p.oid AS same,t.tgnargs::integer FROM pg_trigger t JOIN pg_proc p ON p.oid='ai_saved."Real '' Helper"()'::regprocedure WHERE t.tgrelid='ai_saved."Saved Table"'::regclass`,[]string{"same","tgnargs"},[]string{"BOOL","INT4"},[][]any{{true,int64(4)}}))
		}
		parametersReplay(t,p,b,c,"ai_saved",check)
		if b.PG {
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:writer,SQL:q},b.Recorder,fixture.Exec(q,1,nil,nil,"next"))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,rowsStep(`SELECT "Key ' One" AS id,k,label FROM ai_saved."Saved Table" WHERE label='next'`,[]any{int64(20),int64(101),"next"}))
			return
		}
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil { t.Fatal(err) }
		retained,err:=c.QueryContext(p.Ctx,selectSQL);if err!=nil { t.Fatal(err) };defer retained.Close()
		oldTypes,err:=retained.ColumnTypes();if err!=nil { t.Fatal(err) }
		if len(oldTypes)!=3 || oldTypes[0].DatabaseTypeName()!="INT4" || oldTypes[1].DatabaseTypeName()!="INT4" || oldTypes[2].DatabaseTypeName()!="TEXT" { t.Fatal(oldTypes) }
		// Two additional atomic replacements, the first explicitly without Reset.
		for range 2 {
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil { t.Fatal(err) }
			check(c,b)
		}
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:writer,SQL:q},b.Recorder,fixture.Exec(q,1,nil,nil,"next"))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,rowsStep(`SELECT "Key ' One" AS id,k,label FROM ai_saved."Saved Table" WHERE label='next'`,[]any{int64(20),int64(101),"next"}))
		fresh:=fixture.NewFixture(t,false,true)
		fc,err:=fresh.Memory.Conn(fresh.Ctx);if err!=nil { t.Fatal(err) };defer fc.Close()
		if err:=stuntgres.Restore(fresh.Ctx,fresh.Native,bytes.NewReader(dump.Bytes()));err!=nil { t.Fatal(err) }
		fb:=fixture.Backend{DB:fresh.Memory,Recorder:fresh.MemoryRecord}
		check(fc,fb)
		fixture.RunStep(t,fresh.Ctx,fc,fb.Recorder,fixture.Prepared(fixture.Exec(q,1,nil,nil,"next")))
		fixture.RunStep(t,fresh.Ctx,fc,fb.Recorder,rowsStep(`SELECT "Key ' One" AS id,k,label FROM ai_saved."Saved Table" WHERE label='next'`,[]any{int64(20),int64(101),"next"}))
		userOperatorExec(t,p,b,c,"CREATE TABLE ai_saved.sentinel(n integer)")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO ai_saved.sentinel VALUES(42)",1))
		preserved:=func() {
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT n FROM ai_saved.sentinel",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincScalar(`SELECT last_value FROM ai_saved."S ' One"`,"last_value",20))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincScalar(`SELECT last_value FROM ai_saved."S Two"`,"last_value",101))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,rowsStep(selectSQL,[]any{int64(10),int64(100),"generated"},[]any{int64(7),int64(8),"manual"},[]any{int64(20),int64(101),"next"}))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,autoincScalar("SELECT count(*) FROM pg_class WHERE relname='ai_staged_prefix'","count",0))
		}
		bad:=dump.String()+`;CREATE TABLE ai_saved.ai_staged_prefix(n integer);CREATE TRIGGER broken BEFORE INSERT ON ai_saved."Saved Table" FOR EACH ROW EXECUTE FUNCTION ai_saved.missing();`
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(bad));err==nil { t.Fatal("late invalid trigger accepted") };preserved()
		if err:=stuntgres.Restore(p.Ctx,p.Native,&plpgsqlFailReader{data:dump.Bytes()});!errors.Is(err,io.ErrUnexpectedEOF) { t.Fatal(err) };preserved()
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		if err:=stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump.Bytes()));!errors.Is(err,context.Canceled) { t.Fatal(err) };preserved()
		ctx,stop:=context.WithCancel(p.Ctx)
		err=stuntgres.Restore(ctx,p.Native,&fixture.EnumCancelReader{Reader:bytes.NewReader(dump.Bytes()),Cancel:stop});stop()
		if !errors.Is(err,context.Canceled) { t.Fatal(err) };preserved()
		for _,want:=range []struct{ id,k int64; label string }{{10,100,"generated"},{7,8,"manual"}} {
			if !retained.Next() { t.Fatal("old rows missing",retained.Err()) }
			var id,k int64;var label string
			if err:=retained.Scan(&id,&k,&label);err!=nil || id!=want.id || k!=want.k || label!=want.label { t.Fatal(id,k,label,err) }
		}
		if retained.Next() || retained.Err()!=nil { t.Fatal("old rows cardinality",retained.Err()) }
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:writer,SQL:q},b.Recorder,fixture.Exec(q,1,nil,nil,"after-errors"))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,rowsStep(`SELECT "Key ' One" AS id,k,label FROM ai_saved."Saved Table" WHERE label='after-errors'`,[]any{int64(30),int64(102),"after-errors"}))
	})
}
