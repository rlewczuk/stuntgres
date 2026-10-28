package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func bitStringFunctionsControlSelect() fixture.Step {
	return fixture.Select("SELECT id,v,c,g FROM bsf_symbol ORDER BY id", []string{"id", "v", "c", "g"}, []string{"INT4", "VARBIT", "INT8", "BIT"}, [][]any{{int64(1), "011", int64(2), "11"}})
}
func bitStringFunctionsControlView() fixture.Step {
	return fixture.Select("SELECT * FROM bsf_view ORDER BY id", []string{"id", "s", "o", "p", "q"}, []string{"INT4", "BIT", "BIT", "INT4", "INT4"}, [][]any{{int64(1), "11", "001", int64(2), int64(2)}})
}
func bitStringFunctionsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE bsf_symbol(id INTEGER,v VARBIT DEFAULT substring(B'10110' FROM 2 FOR 3),c BIGINT DEFAULT bit_count(B'101'),g "bit" GENERATED ALWAYS AS (substring(v FROM 2)) STORED,CONSTRAINT bsf_valid CHECK(length(v)>0 AND bit_count(v)>0))`, 0))
	run(fixture.Exec("INSERT INTO bsf_symbol(id) VALUES(1)", 1))
	run(fixture.Exec("CREATE VIEW bsf_view AS SELECT id,substring(v FROM 2) AS s,overlay(v PLACING B'00' FROM 1 FOR 2) AS o,position(B'11' IN v) AS p,pg_catalog.position(v,B'11') AS q FROM bsf_symbol", 0))
	bitStringFunctionsBoth(t, p, b, c, bitStringFunctionsControlSelect())
	bitStringFunctionsBoth(t, p, b, c, bitStringFunctionsControlView())
	if b.PG {
		return
	} // Only the real native API has Dump/Reset/Restore.
	retained, err := c.QueryContext(p.Ctx, "SELECT substring(v FROM 2) AS s FROM bsf_symbol")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	descriptors, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	// This is symbolic SQL preservation, not a native-produced value golden.
	for _, call := range []string{"substring", "bit_count", "position", "overlay"} {
		if !strings.Contains(strings.ToLower(dump.String()), call) {
			t.Fatalf("Dump lost symbolic %s: %s", call, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	bitStringFunctionsBoth(t, p, b, c, bitStringFunctionsControlSelect())
	bitStringFunctionsBoth(t, p, b, c, bitStringFunctionsControlView())
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable symbolic redump", err, redump.String())
	}
	run(fixture.Exec("INSERT INTO bsf_symbol(id) VALUES(2)", 1))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT v,c,g FROM bsf_symbol WHERE id=2", []string{"v", "c", "g"}, []string{"VARBIT", "INT8", "BIT"}, [][]any{{"011", int64(2), "11"}}))
	if !retained.Next() {
		t.Fatal(retained.Err())
	}
	var value any
	if err := retained.Scan(&value); err != nil {
		t.Fatal(err)
	}
	fixture.AssertRawValue(t, false, "BIT", value)
	if !reflect.DeepEqual(value, "11") {
		t.Fatal(value)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal("retained snapshot changed", retained.Err())
	}
	if len(descriptors) != 1 || descriptors[0].DatabaseTypeName() != "BIT" {
		t.Fatal(descriptors)
	}
	if n, ok := descriptors[0].Length(); !ok || n != -1 {
		t.Fatal(n, ok)
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bsf_throwaway(v VARBIT); INSERT INTO bsf_throwaway VALUES (substring(B'1' FROM 1 FOR -1));")); err == nil {
		t.Fatal("malformed Restore succeeded")
	} else {
		d := fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed")
		fixture.AssertDiagnostic(t, err, &d)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed Restore changed catalog", err)
	}
	if p.Postgres != nil {
		// Close native retained results before replay and clean conflicting PG
		// objects. The fixture owns this isolated public schema and all pools.
		for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public", dump.String()} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal("actual native SQL replay", err, q)
			}
		}
		pg := b
		for _, candidate := range p.Backends() {
			if candidate.PG {
				pg = candidate
			}
		}
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitStringFunctionsControlSelect())
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitStringFunctionsControlView())
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Exec("INSERT INTO bsf_symbol(id) VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Select("SELECT v,c,g FROM bsf_symbol WHERE id=2", []string{"v", "c", "g"}, []string{"VARBIT", "INT8", "BIT"}, [][]any{{"011", int64(2), "11"}}))
		for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
}
