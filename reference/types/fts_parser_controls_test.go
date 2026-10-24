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

const ftsParserSmokeDocument = "Word 123 foo@example.com http://example.com/stuff/index.html !"

func ftsParserSmokeTokens() []ftsParserToken {
	return []ftsParserToken{{1,"Word"},{12," "},{22,"123"},{12," "},{4,"foo@example.com"},{12," "},{14,"http://"},{5,"example.com/stuff/index.html"},{6,"example.com"},{18,"/stuff/index.html"},{12," !"}}
}

func ftsParserSavedStep() fixture.Step {
	rows := ftsParserTokenRows(ftsParserSmokeTokens())
	for _,row := range rows { row[0],row[1],row[2]=row[2],row[0],row[1] }
	return fixture.Select("SELECT n,tokid,token FROM fts_parser_saved ORDER BY n",[]string{"n","tokid","token"},[]string{"INT8","INT4","TEXT"},rows)
}

func ftsParserSave(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE fts_parser_original(txt text)",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO fts_parser_original VALUES($1)",1,ftsParserSmokeDocument))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE fts_parser_saved(n bigint,tokid integer,token text)",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO fts_parser_saved SELECT p.ordinality,p.tokid,p.token FROM fts_parser_original i CROSS JOIN LATERAL ts_parse('default',i.txt) WITH ORDINALITY p",11))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,ftsParserSavedStep())
}

func ftsParserRestoredStep() fixture.Step {
	return fixture.Select("SELECT p.tokid,p.token,p.ordinality FROM fts_parser_original i CROSS JOIN LATERAL ts_parse('default',i.txt) WITH ORDINALITY p ORDER BY p.ordinality",[]string{"tokid","token","ordinality"},[]string{"INT4","TEXT","INT8"},ftsParserTokenRows(ftsParserSmokeTokens()))
}

func TestReferenceFTSParserFeatureSmoke(t *testing.T) {
	ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		ftsParserSelect(t,p,b,"SELECT * FROM ts_token_type('pg_catalog.default') ORDER BY tokid",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors())
		ftsParserParse(t,p,b,ftsParserSmokeDocument,ftsParserSmokeTokens())
		for _,call := range []string{"ts_parse(3722::oid,''::text)","ts_parse(NULL::text,'Word')"} {
			ftsParserSelect(t,p,b,"SELECT * FROM "+call,[]string{"tokid","token"},[]string{"INT4","TEXT"},nil)
		}
		query := "SELECT * FROM ts_parse($1::text,$2::text)"
		stmt,err := b.DB.PrepareContext(p.Ctx,query)
		if err!=nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
		s := fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("42704",`text search parser "missing" does not exist`),"missing","Word")
		s.SelectRows,s.ErrorStage=true,"execute"
		fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(query,[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}},"default","Word"))
		ftsParserSave(t,p,b)
		if b.PG { return }
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err!=nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,ftsParserRestoredStep())
	})
}

func TestReferenceFTSParserControls(t *testing.T) {
	ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		ftsParserSave(t,p,b)
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,ftsParserRestoredStep())
		if b.PG { return }
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err!=nil { t.Fatal(err) }
		if strings.Contains(dump.String(),"CREATE TEXT SEARCH PARSER") || strings.Contains(dump.String(),"CREATE FUNCTION pg_catalog.prsd_") { t.Fatal("dump serialized builtin parser state") }
		retained,err := b.DB.QueryContext(p.Ctx,"SELECT n,tokid,token FROM fts_parser_saved ORDER BY n")
		if err!=nil { t.Fatal(err) }
		defer retained.Close()
		metadata,err := retained.ColumnTypes()
		if err!=nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx,p.Native); err!=nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,ftsParserSavedStep())
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,ftsParserRestoredStep())
		ftsParserConsumeSaved(t,retained)
		fixture.ScalarAssertDescription(t,metadata,[]string{"n","tokid","token"},[]string{"INT8","INT4","TEXT"})
		target := fixture.NewFixture(t,false,true)
		fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("CREATE TABLE fts_parser_sentinel(v integer)",0))
		fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Exec("INSERT INTO fts_parser_sentinel VALUES(42)",1))
		if err := stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes())); err!=nil { t.Fatal(err) }
		verify := func() {
			fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,ftsParserSavedStep())
			fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,ftsParserRestoredStep())
		}
		verify()
		bad := "CREATE TABLE fts_parser_rejected(v integer); INSERT INTO fts_parser_rejected VALUES(1); INSERT INTO fts_parser_rejected VALUES('bad')"
		d := fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`)
		d.Position=fixture.Position(strings.Index(bad,"'bad'")+1)
		fixture.AssertDiagnostic(t,stuntgres.Restore(target.Ctx,target.Native,strings.NewReader(bad)),&d)
		verify()
		ctx,cancel := context.WithCancel(target.Ctx)
		reader := &fixture.SequenceGatedReader{Reader:bytes.NewReader(dump.Bytes()),Started:make(chan struct{}),Release:make(chan struct{})}
		done := make(chan error,1)
		go func() { done<-stuntgres.Restore(ctx,target.Native,reader) }()
		select {
		case <-reader.Started:
		case <-target.Ctx.Done():
			cancel()
			close(reader.Release)
			<-done
			t.Fatal(target.Ctx.Err())
		}
		cancel()
		close(reader.Release)
		if err := <-done; !errors.Is(err,context.Canceled) { t.Fatal("admitted restore cancellation",err) }
		verify()
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx,target.Native,&redump); err!=nil { t.Fatal(err) }
		if !bytes.Equal(dump.Bytes(),redump.Bytes()) { t.Fatal("same/fresh restore or failed restore changed dump") }
	})
}

func ftsParserConsumeSaved(t *testing.T,rows *sql.Rows) {
	t.Helper()
	for i,token := range ftsParserSmokeTokens() {
		if !rows.Next() { t.Fatal("retained row missing",rows.Err()) }
		var n int64
		var id int32
		var text string
		if err := rows.Scan(&n,&id,&text); err!=nil { t.Fatal(err) }
		if n!=int64(i+1) || id!=int32(token.id) || text!=token.text { t.Fatalf("retained row %d: %d/%d/%q",i,n,id,text) }
	}
	if rows.Next() || rows.Err()!=nil { t.Fatal("retained EOF",rows.Err()) }
	if err := rows.Close(); err!=nil { t.Fatal(err) }
}
