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

func pgIndexControlSeed(run func(fixture.Step)) {
	for _,q := range []string{"CREATE TABLE pgi_t(a INT,b INT,c INT)","CREATE INDEX pgi_ix ON pgi_t(a DESC NULLS FIRST,b) INCLUDE(c)","CREATE INDEX pgi_simple ON pgi_t(a)","CREATE TABLE pgi_pk(n INT CONSTRAINT pgi_pk_ix PRIMARY KEY)"} { run(fixture.Exec(q,0)) }
	run(fixture.Exec("INSERT INTO pgi_pk VALUES(7)",1))
	pgIndexPartialSeed(run)
}

func pgIndexControlCheck(run func(fixture.Step)) {
	pgIndexInclude(run,false)
	pgIndexBoth(run,pgIndexLogical("pgi_simple",[][]any{pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil)}))
	primary := pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil); primary[4],primary[6] = true,true
	pgIndexBoth(run,pgIndexLogical("pgi_pk_ix",[][]any{primary}))
	pgIndexBoth(run,fixture.Select(`SELECT i.indexrelid=c.conindid AS index_link,i.indrelid=c.conrelid AS parent_link FROM pg_index i JOIN pg_constraint c ON c.conindid=i.indexrelid WHERE c.conname='pgi_pk_ix'`,[]string{"index_link","parent_link"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
	pgIndexBoth(run,fixture.ErrorDiagnostic("INSERT INTO pgi_pk VALUES(7)",fixture.UniqueReferenceDuplicate("pgi_pk","pgi_pk_ix","Key (n)=(7) already exists.")))
	pgIndexPartialCheck(run)
}

func pgIndexNativeRoundtrip(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	if b.PG { t.Fatal("native controls on PostgreSQL") }
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
	owner := fixture.ConcurrentIndexConn(t,p,b)
	private := transactionalIndexRun(t,p,b,owner)
	private(fixture.Exec("BEGIN",0)); private(fixture.Exec("CREATE INDEX pgi_uncommitted ON pgi_t(c)",0))
	private(pgIndexLogical("pgi_uncommitted",[][]any{pgIndexOrdinary(1,1,"3","0","1978","0",nil,nil)}))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
	private(fixture.Exec("ROLLBACK",0))
	connector,err := stuntgres.NewDriver().OpenConnector(t.Name()+"/pgi-clone"); if err != nil { t.Fatal(err) }
	clone := sql.OpenDB(connector); defer clone.Close()
	if err := stuntgres.Restore(p.Ctx,clone,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal("fresh replay",err) }
	recorded,recorder := fixture.RecordedNativePool(t,connector)
	cloneRun := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,recorded,recorder,s) }
	pgIndexControlCheck(cloneRun); pgIndexBoth(cloneRun,pgIndexLogical("pgi_uncommitted",nil))
	state,err := stuntgres.InspectIndex(p.Ctx,clone,"public","pgi_partial_expr")
	if err != nil || !state.Ready || !state.Valid || !state.Live { t.Fatal("restored owner state",state,err) }

	// Baseline raw tree bytes are only a native retained-artifact witness, not
	// PostgreSQL nodeToString goldens. Exact semantic deparse is asserted above.
	q := "SELECT * FROM pg_catalog.pg_index WHERE indexrelid='pgi_partial_expr'::regclass"
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	baseline,err := b.DB.QueryContext(ctx,q); if err != nil { t.Fatal(err) }
	want := fixture.AnyValueConsume(t,baseline,b.Recorder,q,pgIndexColumns,pgIndexTypes)
	if len(want) != 1 || len(want[0]) != 21 || want[0][19] == nil || want[0][20] == nil { t.Fatal("typed retained trees absent",want) }
	retained,err := b.DB.QueryContext(ctx,q); if err != nil { t.Fatal(err) }; defer retained.Close()
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	pgIndexBoth(run,fixture.Select("SELECT * FROM pg_catalog.pg_index",pgIndexColumns,pgIndexTypes,nil))
	if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	fixture.RangeConsumeHeld(t,retained,fixture.Select(q,pgIndexColumns,pgIndexTypes,want))
	pgIndexControlCheck(run)
	var savedIndex, savedParent int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT indexrelid::bigint,indrelid::bigint FROM pg_index WHERE indexrelid='pgi_partial_expr'::regclass").Scan(&savedIndex, &savedParent); err != nil { t.Fatal(err) }
	for _,phase := range []string{"late-invalid","reader","canceled"} {
		switch phase {
		case "late-invalid":
			err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump.String()+"\nALTER INDEX public.pgi_ix SET(fillfactor=9);"))
			want := fixture.ControlsReferenceDiagnostic("22023",`value 9 out of bounds for option "fillfactor"`); want.Detail = `Valid values are between "10" and "100".`; fixture.AssertDiagnostic(t,err,&want)
		case "reader":
			failure := errors.New("pg_index restore reader failure")
			err := stuntgres.Restore(p.Ctx,p.Native,fixture.CatalogControlFailingReader{Err:failure}); pgIndexOrdinaryError(t,err)
			if !errors.Is(err,failure) { t.Fatal("reader identity",err) }
		case "canceled":
			ctx,cancel := context.WithCancel(p.Ctx)
			err := stuntgres.Restore(ctx,p.Native,&fixture.CatalogControlCancelReader{Cancel:cancel}); cancel(); pgIndexOrdinaryError(t,err)
			if !errors.Is(err,context.Canceled) { t.Fatal("Restore cancellation",err) }
		}
		pgIndexControlCheck(run)
		pgIndexBoth(run, fixture.Select("SELECT indexrelid=$1::oid AS same_index,indrelid=$2::oid AS same_parent FROM pg_index WHERE indexrelid='pgi_partial_expr'::regclass", []string{"same_index","same_parent"}, []string{"BOOL","BOOL"}, [][]any{{true,true}}, savedIndex,savedParent))
	}
	run(fixture.Exec("INSERT INTO pgi_pk VALUES(8)",1)); run(fixture.Exec("DELETE FROM pgi_pk WHERE n=8",1)); pgIndexControlCheck(run)
}

func TestReferencePGIndexControls(t *testing.T) {
	fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
		pgIndexControlSeed(run); pgIndexControlCheck(run)
		if !b.PG { pgIndexNativeRoundtrip(t,p,b) }
	})
}
