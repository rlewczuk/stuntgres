package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each phase owns its fixture deadline, including each failed replacement. No
// live context, cursor, expected identity or dump crosses independent phases.
type isnControlPhases struct {
	name, valid, invalid, message, private string
	query string
	rows [][]any
	setup, verify func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)
	failures []string
}

func runISNControlPhases(t *testing.T, spec isnControlPhases) {
	t.Helper()
	prefix := "CREATE EXTENSION isn; CREATE TABLE " + spec.name + "_staged(e ean13,v " + spec.name + "); INSERT INTO " + spec.name + "_staged VALUES('9780123456786'," + ean13Literal(spec.valid) + "); INSERT INTO " + spec.name + "_staged VALUES('9780123456786',"
	bad := prefix + ean13Literal(spec.invalid) + ");"
	want := ean13Diagnostic("22P02", spec.message)
	want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
	t.Run("statement_atomicity_and_recreation", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			if b.PG {
				s := fixture.ErrorDiagnostic(bad, want)
				s.ErrorStage = "execute"
				run(s)
			} else {
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname=" + ean13Literal(spec.name + "_staged"), []string{"relname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			spec.setup(t,p,b,c)
			spec.verify(t,p,b,c)
			run(fixture.Exec("DROP TABLE " + spec.name + "_control," + spec.name + "_required",0))
			run(fixture.Exec("DROP EXTENSION isn",0))
			spec.setup(t,p,b,c)
			spec.verify(t,p,b,c)
		})
	})
	t.Run("same_target",func(t *testing.T){
		ean13Scenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			if b.PG { return } // Public native controls have no PostgreSQL counterpart.
			spec.setup(t,p,b,c)
			spec.verify(t,p,b,c)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("BEGIN",0))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO " + spec.name + "_control VALUES(99,'1400000000007'," + ean13Literal(spec.private) + ")",1))
			var dump bytes.Buffer
			err:=stuntgres.Dump(p.Ctx,p.Native,&dump)
			_,rollbackErr:=c.ExecContext(p.Ctx,"ROLLBACK")
			if err!=nil || rollbackErr!=nil {t.Fatalf("Dump = %v; rollback = %v",err,rollbackErr)}
			if strings.Contains(dump.String(),"140000000000") {t.Fatal("Dump leaked private values")}
			isnControlDumpOrder(t,dump.String())
			old,metadata:=isbnRetain(t,p,c,spec.query)
			defer old.Close()
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'",[]string{"extname"},[]string{"NAME"},[][]any{}))
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn')",[]string{"typname"},[]string{"NAME"},[][]any{}))
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
			spec.verify(t,p,b,c)
			isbnConsumeRetained(t,old,metadata,spec.rows,"INT4","EAN13",strings.ToUpper(spec.name))
			isnControlRedump(t,p,dump.Bytes())
			if p.Postgres!=nil {isbnReplay(t,p,dump.String(),nil,func(pg *sql.Conn,pb fixture.Backend){spec.verify(t,p,pb,pg)})}
		})
	})
	t.Run("fresh_driver",func(t *testing.T){
		ean13Scenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			if b.PG {return}
			spec.setup(t,p,b,c)
			var dump bytes.Buffer
			if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
			isnControlDumpOrder(t,dump.String())
			target,conn,tb:=isnControlDestination(t)
			// Prove all seven scalar, seven companion and fourteen codec roles,
			// not just the manifest's first member, have displaced identities.
			for _,typ:=range isnControlScalarNames(){
				var source,dest [4]int64
				q:="SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid="+ean13Literal(typ)+"::regtype::oid"
				if err:=c.QueryRowContext(p.Ctx,q).Scan(&source[0],&source[1],&source[2],&source[3]);err!=nil{t.Fatal(err)}
				if err:=conn.QueryRowContext(target.Ctx,q).Scan(&dest[0],&dest[1],&dest[2],&dest[3]);err!=nil{t.Fatal(err)}
				for i:=range source {if source[i]==dest[i]{t.Fatalf("destination %s role %d was not displaced",typ,i)}}
			}
			if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
			spec.verify(t,target,tb,conn)
			isnControlRedump(t,target,dump.Bytes())
		})
	})
	for _,kind:=range spec.failures{
		t.Run("failed_replacement/"+kind,func(t *testing.T){
			ean13Scenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
				if b.PG {return}
				spec.setup(t,p,b,c)
				var dump bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil{t.Fatal(err)}
				target,conn,tb:=isnControlDestination(t)
				// Every failed replacement also proves that all scalar, companion
				// and codec roles were displaced before restoring its actual dump.
				for _,typ:=range isnControlScalarNames(){
					source:=isbnIdentityStamp(t,p,c,typ)
					dest:=isbnIdentityStamp(t,target,conn,typ)
					for i:=range source{
						if source[i]==dest[i]{t.Fatalf("destination %s role %d was not displaced",typ,i)}
					}
				}
				if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
				spec.verify(t,target,tb,conn)
				identities:=map[string][4]int64{}
				for _,typ:=range isnControlScalarNames(){identities[typ]=isbnIdentityStamp(t,target,conn,typ)}
				retained,columns:=isbnRetain(t,target,conn,spec.query)
				defer retained.Close()
				ctx:=target.Ctx
				var reader io.Reader
				switch kind{
				case "invalid_literal":reader=strings.NewReader(bad)
				case "canceled":
					var cancel context.CancelFunc
					ctx,cancel=context.WithCancel(ctx)
					cancel()
					reader=bytes.NewReader(dump.Bytes())
				case "reader":reader=io.MultiReader(bytes.NewReader(dump.Bytes()),fixture.TypeLifecycleErrorReader{io.ErrUnexpectedEOF})
				default:reader=strings.NewReader(isbnInvalidOwnerDump(t,dump.String(),kind))
				}
				err:=stuntgres.Restore(ctx,target.Native,reader)
				switch kind{
				case "invalid_literal":fixture.AssertDiagnostic(t,err,&want)
				case "canceled":if !errors.Is(err,context.Canceled){t.Fatalf("canceled Restore = %v",err)}
				case "reader":if !errors.Is(err,io.ErrUnexpectedEOF){t.Fatalf("reader Restore = %v",err)}
				default:
					var state interface{SQLState() string}
					if err==nil || !strings.Contains(err.Error(),"invalid extension owner metadata") || errors.As(err,&state){t.Fatalf("invalid owner Restore must return ordinary control error: %v",err)}
				}
				spec.verify(t,target,tb,conn)
				for typ,identity:=range identities{if got:=isbnIdentityStamp(t,target,conn,typ);got!=identity{t.Fatalf("failed Restore replaced %s identity: %v, want %v",typ,got,identity)}}
				isbnConsumeRetained(t,retained,columns,spec.rows,"INT4","EAN13",strings.ToUpper(spec.name))
				isnControlRedump(t,target,dump.Bytes())
				isnControlRedump(t,p,dump.Bytes())
			})
		})
	}
}

func isnControlScalarNames() []string {return []string{"ean13","isbn","isbn13","ismn","ismn13","issn","issn13"}}

func isnControlDumpOrder(t *testing.T,dump string){
	t.Helper()
	ext,consumer:=strings.Index(dump,"CREATE EXTENSION"),strings.Index(dump,"CREATE TABLE")
	if ext<0 || consumer<ext || strings.Contains(dump,"CREATE TYPE") || strings.Contains(dump,"CREATE FUNCTION"){t.Fatal("Dump must install isn before consumers without standalone members",dump)}
}

func isnControlDestination(t *testing.T)(*fixture.Pair,*sql.Conn,fixture.Backend){
	t.Helper()
	target:=fixture.NewFixture(t,false,true)
	conn,err:=target.Memory.Conn(target.Ctx)
	if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){conn.Close()})
	b:=fixture.Backend{Recorder:target.MemoryRecord,DB:target.Memory}
	fixture.RunStep(t,target.Ctx,conn,b.Recorder,fixture.Exec("CREATE TABLE isbn_allocation_spacer(v integer)",0))
	fixture.RunStep(t,target.Ctx,conn,b.Recorder,fixture.Exec("CREATE EXTENSION isn",0))
	return target,conn,b
}

func isnControlRedump(t *testing.T,p *fixture.Pair,want []byte){
	t.Helper()
	var after bytes.Buffer
	if err:=stuntgres.Dump(p.Ctx,p.Native,&after);err!=nil{t.Fatal(err)}
	if !bytes.Equal(want,after.Bytes()){t.Fatal("Restore changed deterministic dump")}
}
