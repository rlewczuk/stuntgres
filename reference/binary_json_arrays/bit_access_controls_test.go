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

func bitAccessControlTable() fixture.Step {
	return fixture.Select("SELECT id,v,i,g FROM ba_symbol ORDER BY id", []string{"id", "v", "i", "g"}, []string{"INT4", "VARBIT", "INT4", "BIT"}, [][]any{{int64(1), "100", int64(1), "101"}})
}
func bitAccessControlView() fixture.Step {
	return fixture.Select("SELECT * FROM ba_view ORDER BY id", []string{"id", "b", "s"}, []string{"INT4", "INT4", "BIT"}, [][]any{{int64(1), int64(0), "110"}})
}
func bitAccessControlDefault() fixture.Step {
	return fixture.Select("SELECT v,i,g FROM ba_symbol WHERE id=2", []string{"v", "i", "g"}, []string{"VARBIT", "INT4", "BIT"}, [][]any{{"100", int64(1), "101"}})
}
func TestReferenceBitAccessConsumersControls(t *testing.T) { bitAccessScenario(t, bitAccessControls) }
func bitAccessControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE ba_symbol(id INTEGER,v VARBIT DEFAULT set_bit(B'000',0,1),i INTEGER DEFAULT get_bit(B'101',2),g "bit" GENERATED ALWAYS AS (set_bit(v,2,1)) STORED,CONSTRAINT ba_valid CHECK(get_bit(v,0)=1))`, 0))
	run(fixture.Exec("INSERT INTO ba_symbol(id) VALUES(1)", 1))
	run(fixture.Exec("CREATE VIEW ba_view AS SELECT id,get_bit(v,2) AS b,set_bit(v,1,1) AS s FROM ba_symbol", 0))
	bitStringFunctionsBoth(t, p, b, c, bitAccessControlTable())
	bitStringFunctionsBoth(t, p, b, c, bitAccessControlView())
	// A quoted unknown literal must select the BIT family before DEFAULT admission.
	run(fixture.Exec("CREATE TABLE ba_unknown(v VARBIT DEFAULT set_bit('000',0,1),i INTEGER DEFAULT get_bit('101',2))", 0))
	run(fixture.Exec("INSERT INTO ba_unknown DEFAULT VALUES", 1))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT v,i FROM ba_unknown", []string{"v", "i"}, []string{"VARBIT", "INT4"}, [][]any{{"100", int64(1)}}))
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "ba_symbol" violates check constraint "ba_valid"`)
	d.Detail = "Failing row contains (9, 000, 1, 001)."
	d.Schema, d.Table, d.Constraint = "public", "ba_symbol", "ba_valid"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO ba_symbol(id,v) VALUES(9,B'000')", d)
		s.Prepared = prepared
		s.ErrorStage = "execute"
		run(s)
	}
	run(bitAccessControlTable())
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	retained, err := c.QueryContext(p.Ctx, "SELECT get_bit(v,2) AS g,set_bit(v,1,1) AS s FROM ba_symbol")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	cols, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	bitAccessMetadata(t, cols, false)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"get_bit", "set_bit"} {
		if !strings.Contains(strings.ToLower(dump.String()), fn) {
			t.Fatal("lost symbolic call", fn, dump.String())
		}
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	bitStringFunctionsBoth(t, p, b, c, bitAccessControlTable())
	bitStringFunctionsBoth(t, p, b, c, bitAccessControlView())
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable redump", err, redump.String())
	}
	run(fixture.Exec("INSERT INTO ba_symbol(id) VALUES(2)", 1))
	run(bitAccessControlDefault())
	if !retained.Next() {
		t.Fatal(retained.Err())
	}
	var getter, setter any
	if err := retained.Scan(&getter, &setter); err != nil {
		t.Fatal(err)
	}
	fixture.AssertRawValue(t, false, "INT4", getter)
	fixture.AssertRawValue(t, false, "BIT", setter)
	if getter != int64(0) || setter != "110" {
		t.Fatal("retained result changed", getter, setter)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal(retained.Err())
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	bitAccessMetadata(t, cols, false)
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE ba_throwaway(v VARBIT); INSERT INTO ba_throwaway VALUES(set_bit(B'1',1,2));")); err == nil {
		t.Fatal("invalid Restore succeeded")
	} else {
		d := fixture.ControlsReferenceDiagnostic("2202E", "bit index 1 out of valid range (0..0)")
		fixture.AssertDiagnostic(t, err, &d)
	}
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed Restore replaced catalog", err)
	}
	if p.Postgres != nil {
		// Replay the actual native Dump, never an independently manufactured script.
		for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public", dump.String()} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal("actual native SQL replay", err, q)
			}
		}
		var pg fixture.Backend
		for _, candidate := range p.Backends() {
			if candidate.PG {
				pg = candidate
			}
		}
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitAccessControlTable())
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitAccessControlView())
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Exec("INSERT INTO ba_symbol(id) VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitAccessControlDefault())
		for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
	}
}
