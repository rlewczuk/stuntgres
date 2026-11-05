package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func intarraySliceControlVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,owner string) []int64 {
	t.Helper()
	intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
	intarrayBoth(t,p,b,c,intarraySelect(b,"SELECT id,d,s FROM ia_slice_rows ORDER BY id",[]string{"id","d","s"},[]string{"INT4","_INT4","_INT4"},[]bool{false,true,true},[][]any{{int64(1),"{-7}","{22,33,22}"},{int64(2),"{-7}","{22,33,22}"},{int64(3),"{-7}","{22,33,22}"}}))
	intarraySliceDefaults(t,p,b,c)
	return intarraySliceCatalog(t,p,b,c,owner)
}

func intarraySliceReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres == nil { return }
	b := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	c,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer c.Close()
	// Register before destructive setup, including authorization restoration
	// even when an assertion terminates the replay. Owner comes from the dump.
	defer func() {
		ctx,cancel := context.WithTimeout(context.Background(),15*time.Second); defer cancel()
		for _,q := range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP SCHEMA IF EXISTS ia_collision CASCADE","DROP SCHEMA IF EXISTS ia_provider CASCADE","DROP SCHEMA IF EXISTS public CASCADE","CREATE SCHEMA public"} {
			if _,err := c.ExecContext(ctx,q); err != nil { t.Errorf("slice replay cleanup %q: %v",q,err) }
		}
	}()
	for _,q := range []string{"DROP SCHEMA IF EXISTS ia_collision CASCADE","DROP SCHEMA IF EXISTS ia_provider CASCADE","DROP SCHEMA public CASCADE","CREATE SCHEMA public",string(dump)} {
		if _,err := c.ExecContext(p.Ctx,q); err != nil { t.Fatal("actual emitted slice SQL replay",err) }
	}
	intarraySliceControlVerify(t,p,b,c,"postgres")
}

func intarraySliceNativeRoundtrip(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	sourceIDs := intarraySliceControlVerify(t,p,b,c,"postgres")
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("BEGIN",0))
	defer c.ExecContext(p.Ctx,"ROLLBACK")
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO ia_slice_rows(id,a) VALUES (99,'{987654321}')",1))
	dump := intarrayDump(t,p)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("ROLLBACK",0))
	if bytes.Contains(dump,[]byte("987654321")) { t.Fatal("Dump leaked private slice inputs") }
	if again := intarrayDump(t,p); !bytes.Equal(dump,again) { t.Fatal("committed slice dump is not deterministic") }
	old := intarraySliceRetain(t,p,b,c); defer old.rows.Close()
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	intarrayBoth(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'",[]string{"extname"},[]string{"NAME"},[][]any{}))
	if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
	intarraySliceControlVerify(t,p,b,c,"postgres")
	intarraySliceConsume(t,b,old)

	target := fixture.NewFixture(t,false,true)
	tb := fixture.Backend{DB:target.Memory,Recorder:target.MemoryRecord}
	tc,err := tb.DB.Conn(target.Ctx); if err != nil { t.Fatal(err) }; defer tc.Close()
	fixture.RunStep(t,target.Ctx,tc,tb.Recorder,fixture.Exec("CREATE TEMP TABLE ia_slice_allocator_spacer(v integer)",0))
	if err := stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump)); err != nil { t.Fatal(err) }
	destinationIDs := intarraySliceControlVerify(t,target,tb,tc,"postgres")
	if reflect.DeepEqual(sourceIDs,destinationIDs) { t.Fatal("allocator spacer did not displace slice identities") }
	intarraySliceReplay(t,p,dump)
}

func intarraySliceNativeFailures(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	ids := intarraySliceControlVerify(t,p,b,c,"postgres")
	dump := intarrayDump(t,p)
	old := intarraySliceRetain(t,p,b,c); defer old.rows.Close()
	bad := append(append([]byte(nil),dump...),[]byte("\nINSERT INTO ia_slice_rows(id,a) VALUES (11,'{22,NULL}');\n")...)
	want := intarraySetNullError()
	fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(bad)),&want)
	ctx,cancel := context.WithCancel(p.Ctx); cancel()
	ioFailure := errors.New("slice control I/O sentinel")
	for _,item := range []struct{ got,want error }{
		{stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump)),context.Canceled},
		{stuntgres.Dump(ctx,p.Native,new(bytes.Buffer)),context.Canceled},
		{stuntgres.Restore(p.Ctx,p.Native,fixture.CatalogControlFailingReader{Err:ioFailure}),ioFailure},
		{stuntgres.Dump(p.Ctx,p.Native,fixture.CatalogControlFailingWriter{Err:ioFailure}),ioFailure},
	} {
		var state interface{ SQLState() string }
		if !errors.Is(item.got,item.want) || errors.As(item.got,&state) { t.Fatalf("ordinary control error %v want %v",item.got,item.want) }
		if after := intarrayDump(t,p); !bytes.Equal(dump,after) { t.Fatal("failed replacement mutated deterministic dump") }
	}
	if afterIDs := intarraySliceControlVerify(t,p,b,c,"postgres"); !reflect.DeepEqual(ids,afterIDs) { t.Fatalf("failed replacement changed identities %v -> %v",ids,afterIDs) }
	intarraySliceConsume(t,b,old)
	if after := intarrayDump(t,p); !bytes.Equal(dump,after) { t.Fatal("failed replacement changed logical state") }
}

func TestReferenceIntarraySliceControls(t *testing.T) {
	for _,phase := range []string{"round_trip","failed_replacement"} {
		t.Run(phase,func(t *testing.T) {
			intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				intarraySliceSeed(t,p,b,c)
				intarraySliceControlVerify(t,p,b,c,intarrayFixtureOwner(b))
				if !b.PG {
					if phase == "round_trip" { intarraySliceNativeRoundtrip(t,p,b,c) } else { intarraySliceNativeFailures(t,p,b,c) }
				} else {
					q := "INSERT INTO ia_slice_rows(id,a) VALUES (10,'{22}'),(11,'{22,NULL}')"
					for _,prepared := range []bool{false,true} { intarrayError(t,p,b,c,q,prepared,"execute",intarraySetNullError()) }
					intarraySliceControlVerify(t,p,b,c,"stuntgres")
				}
			})
		})
	}
}
