package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func statisticExtControls(t *testing.T,p *fixture.Pair,b fixture.Backend,owner int64) {
	t.Helper()
	if b.PG { return }
	held,err := p.Native.QueryContext(p.Ctx,statisticExtQuery,owner); if err != nil { t.Fatal(err) }; defer held.Close()
	saved,err := held.ColumnTypes(); if err != nil { t.Fatal(err) }; statisticExtMetadata(t,false,saved)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
	if dump.Len() == 0 { t.Fatal("empty dump") }
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_control_sentinel'",[]string{"oid"},[]string{"OID"},nil))
	statisticExtRead(t,p,b,b.DB,statisticExtQuery,owner)
	if held.Next() || held.Err() != nil { t.Fatal("retained empty cursor",held.Err()) }
	if err := held.Close(); err != nil { t.Fatal(err) }; statisticExtMetadata(t,false,saved)
	if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	unchanged := func() {
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT * FROM stx_control_sentinel ORDER BY a",[]string{"a","b"},[]string{"INT4","INT4"},[][]any{{int64(1),int64(2)},{int64(3),int64(4)}}))
		statisticExtRead(t,p,b,b.DB,statisticExtQuery,owner)
	}
	unchanged()
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&again); err != nil { t.Fatal(err) }
	if !bytes.Equal(dump.Bytes(),again.Bytes()) { t.Fatal("redump changed") }
	err = stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE TABLE stx_staged(v INT); INSERT INTO stx_staged VALUES(2147483648);"))
	want := fixture.ControlsReferenceDiagnostic("22003","integer out of range"); fixture.AssertDiagnostic(t,err,&want)
	unchanged()
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_staged'",[]string{"oid"},[]string{"OID"},nil))
	ctx,cancel := context.WithCancel(p.Ctx); cancel()
	if err := stuntgres.Restore(ctx,p.Native,bytes.NewReader(dump.Bytes())); !errors.Is(err,context.Canceled) { t.Fatal(err) }
	if rows,err := p.Native.QueryContext(ctx,statisticExtQuery,owner); rows != nil || !errors.Is(err,context.Canceled) { t.Fatal(rows,err) }
	unchanged()
}

func TestReferencePGStatisticExtControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _,b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE stx_control_sentinel(a INT,b INT)",0))
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO stx_control_sentinel VALUES(1,2),(3,4)",2))
		conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }
		defer conn.Close()
		owner := fixture.DeparseDefinitionID(t,conn,"SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_control_sentinel'")
		if err := conn.Close(); err != nil { t.Fatal(err) }
		statisticExtRead(t,p,b,b.DB,statisticExtQuery,owner)
		statisticExtControls(t,p,b,owner)
		statisticExtRead(t,p,b,b.DB,statisticExtQuery,owner)
	}) }
}

func TestReferencePGStatisticExtFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"stx_smoke_reader"})
	p := fixture.NewScenario(t)
	for _,b := range p.Backends() { t.Run(b.Name,func(t *testing.T) {
		conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE stx_control_sentinel(a INT,b INT)",0))
		run(fixture.Exec("INSERT INTO stx_control_sentinel VALUES(1,2),(3,4)",2))
		owner := fixture.DeparseDefinitionID(t,conn,"SELECT oid FROM pg_catalog.pg_class WHERE relname='stx_control_sentinel'")
		statisticExtRead(t,p,b,conn,statisticExtQuery,owner)
		stmt,err := conn.PrepareContext(p.Ctx,statisticExtQuery); if err != nil { t.Fatal(err) }
		held := fixture.HeldStatement{conn,stmt,statisticExtQuery}
		for range 2 { statisticExtRead(t,p,b,held,statisticExtQuery,owner) }
		if err := stmt.Close(); err != nil { t.Fatal(err) }
		statisticExtShape(t,p,b,conn)
		want := fixture.ControlsReferenceDiagnostic("42703",`column "missing" does not exist`); want.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_statistic_ext",want); s.SelectRows,s.ErrorStage = true,"execute"; run(s)
		run(fixture.Exec("CREATE ROLE stx_smoke_reader",0))
		run(fixture.Exec("SET ROLE stx_smoke_reader",0))
		func() {
			defer conn.ExecContext(context.Background(),"RESET ROLE")
			statisticExtRead(t,p,b,conn,statisticExtQuery,owner)
			statisticExtError(t,p,b,conn,statisticExtMutations[2],"42501","permission denied for table pg_statistic_ext",false,false)
		}()
		if err := conn.Close(); err != nil { t.Fatal(err) }
		statisticExtControls(t,p,b,owner)
	}) }
}
