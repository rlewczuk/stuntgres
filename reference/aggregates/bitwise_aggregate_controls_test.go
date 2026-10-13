package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateControls(t *testing.T,p *fixture.Pair,b fixture.Backend,emitted *string) {
	const q = "SELECT ia,io,ix,fa,fo,fx,va,vo,vx FROM bw_controls"
	names := []string{"ia","io","ix","fa","fo","fx","va","vo","vx"}
	types := []string{"INT4","INT4","INT4","BIT","BIT","BIT","BIT","BIT","BIT"}
	want := [][]any{{int64(0),int64(15),int64(5),"00000","11111","01010","00000","11111","01010"}}
	s := fixture.Select(q,names,types,want)
	s.CheckMetadata = func(t *testing.T,rows *sql.Rows) { bitwiseAggregateMetadata(t,rows,b.PG,names,types) }
	if b.PG && *emitted!="" {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		// Actual native emitted SQL, not a canned imitation of a control API.
		if _, err = conn.ExecContext(p.Ctx,*emitted); err != nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
		return
	}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_control_source(i INT,f BIT(5),v VARBIT(9))",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_control_source VALUES(12,B'10101',B'10101'),(10,B'11000',B'11000'),(3,B'00111',B'00111'),(NULL,NULL,NULL)",4))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE VIEW bw_controls AS SELECT bit_and(i) AS ia,bit_or(i) AS io,bit_xor(i) AS ix,bit_and(f) AS fa,bit_or(f) AS fo,bit_xor(f) AS fx,bit_and(v) AS va,bit_or(v) AS vo,bit_xor(v) AS vx FROM bw_control_source",0))
	bitwiseAggregateRoutes(t,p,b,s)
	// Publishing another catalog generation must remap both provider families.
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_control_publication(i INT)",0))
	bitwiseAggregateRoutes(t,p,b,s)
	if b.PG { return } // Native controls have no PostgreSQL API counterpart.
	stmt, err := b.DB.PrepareContext(p.Ctx,q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	retained, err := stmt.QueryContext(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	bitwiseAggregateMetadata(t,retained,false,names,types)
	before, err := retained.ColumnTypes()
	if err != nil { t.Fatal(err) }
	var dump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
	*emitted=dump.String()
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_control_source VALUES(7,B'00101',B'00101')",1))
	if err = stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	if err = stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(*emitted)); err != nil { t.Fatal(err) }
	fixture.RunStep(t,p.Ctx,fixture.HeldStatement{b.DB,stmt,q},b.Recorder,s)
	if err = stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(*emitted+"\nCREATE TABLE bw_invalid(v bw_missing_type);")); err==nil { t.Fatal("invalid replacement succeeded") }
	var after bytes.Buffer
	if err = stuntgres.Dump(p.Ctx,p.Native,&after); err != nil { t.Fatal(err) }
	if after.String()!=*emitted { t.Fatal("failed replacement changed dump") }
	fixture.RunStep(t,p.Ctx,fixture.HeldStatement{b.DB,stmt,q},b.Recorder,s)
	// Consume the original live cursor only after replacement; saved descriptors
	// remain valid after EOF, unlike calling ColumnTypes on an auto-closed Rows.
	fixture.ScalarRows(t,false,retained,names,types,want,false)
	fixture.ScalarAssertDescription(t,before,names,types)
	for _, c := range before {
		if c.DatabaseTypeName()=="BIT" { if width,ok := c.Length(); !ok || width!=-1 { t.Fatalf("retained width %d/%v",width,ok) } }
		if nullable,known := c.Nullable(); !nullable || !known { t.Fatalf("retained nullable %v/%v",nullable,known) }
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("bitwise-aggregate-fresh/"+t.Name())
	if err != nil { t.Fatal(err) }
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err = stuntgres.Restore(p.Ctx,fresh,strings.NewReader(*emitted)); err != nil { t.Fatal(err) }
	rows, err := fresh.QueryContext(p.Ctx,q)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	bitwiseAggregateMetadata(t,rows,false,names,types)
	fixture.ScalarRows(t,false,rows,names,types,want,false)
	ctx,cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, executor := range []fixture.Executor{b.DB,fixture.HeldStatement{b.DB,stmt,q}} {
		rows,err := executor.QueryContext(ctx,q)
		if rows!=nil { rows.Close() }
		if rows!=nil || !errors.Is(err,context.Canceled) { t.Fatalf("precanceled query published %v, error %v",rows,err) }
		result,err := executor.ExecContext(ctx,q)
		if result!=nil || !errors.Is(err,context.Canceled) { t.Fatalf("precanceled Exec published %v, error %v",result,err) }
	}
	fixture.RunStep(t,p.Ctx,fixture.HeldStatement{b.DB,stmt,q},b.Recorder,s)
}

func TestReferenceBitwiseAggregateControls(t *testing.T) {
	var emitted string
	bitwiseAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		bitwiseAggregateControls(t,p,b,&emitted)
	})
}
