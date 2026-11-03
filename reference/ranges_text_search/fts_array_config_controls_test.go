package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ftsArrayConfigControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
	ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("english"))
	// Definition must stay executable: values already stored retain the custom
	// identity, while newly evaluated defaults use the target's current setting.
	ftsArrayConfigRun(t, p, b, c, fixture.Exec("INSERT INTO public.array_cfg_saved(id,a) VALUES(99,'{z,z}')", 1))
	ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT v,cfg FROM public.array_cfg_saved WHERE id=99", []string{"v", "cfg"}, []string{"TSVECTOR", "REGCONFIG"}, [][]any{{"'a' 'b'", "english"}}))
	ftsArrayConfigRun(t, p, b, c, fixture.Exec("DELETE FROM public.array_cfg_saved WHERE id=99", 1))
}

// Native logical controls are a phase of the shared Persistence/FeatureSmoke
// scenarios, not an alternative SQL scenario or an alternate fixture. Reuse the
// shared retained-row consumer and the fixture's actual emitted-SQL oracle.
func ftsArrayConfigControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	check := ftsArrayConfigStored()
	held, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='english'", 0))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("same-engine Restore", err)
	}
	fixture.RangeConsumeHeld(t, held, check)
	rangeRetainedMetadata(t, metadata, check, nil)
	ftsArrayConfigControlCheck(t, p, b, c)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("same-engine redump", err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("Reset/Restore", err)
	}
	ftsArrayConfigControlCheck(t, p, b, c)

	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE allocator_spacer(v integer)", 0))
	tc, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	native := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	ftsArrayConfigRun(t, target, native, tc, fixture.Exec("SET default_text_search_config='english'", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("fresh-driver Restore", err)
	}
	ftsArrayConfigControlCheck(t, target, native, tc)
	redump.Reset()
	if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("fresh-driver redump", err)
	}
	for _, failure := range []rangeStagedFailure{
		{script: `CREATE TABLE staged_array_cfg(a text[],v tsvector GENERATED ALWAYS AS (array_to_tsvector(a)) STORED); INSERT INTO staged_array_cfg(a) VALUES('{good}'); INSERT INTO staged_array_cfg(a) VALUES('{bad,""}');`, diagnostic: fixture.ControlsReferenceDiagnostic("2200F", "lexeme array may not contain empty strings")},
		{script: `CREATE TABLE staged_array_cfg(a text[],v tsvector GENERATED ALWAYS AS (array_to_tsvector(a)) STORED); INSERT INTO staged_array_cfg(a) VALUES('{good}'); INSERT INTO staged_array_cfg(a) VALUES('{bad,NULL}');`, diagnostic: fixture.ControlsReferenceDiagnostic("22004", "lexeme array may not contain nulls")},
	} {
		retained, err := tc.QueryContext(target.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			retained.Close()
			t.Fatal(err)
		}
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, bytes.NewBufferString(failure.script)), &failure.diagnostic)
		fixture.RangeConsumeHeld(t, retained, check)
		retained.Close()
		rangeRetainedMetadata(t, columns, check, nil)
		ftsArrayConfigControlCheck(t, target, native, tc)
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("failed staged Restore changed prior state", err)
		}
	}
	canceled, cancel := context.WithCancel(target.Ctx)
	cancel()
	for _, call := range []func() error{
		func() error { return stuntgres.Reset(canceled, target.Native) },
		func() error { return stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())) },
		func() error { return stuntgres.Dump(canceled, target.Native, &bytes.Buffer{}) },
	} {
		err := call()
		var state interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("control cancellation crossed SQL boundary", err)
		}
	}
	ftsArrayConfigControlCheck(t, target, native, tc)
	if p.Postgres != nil {
		pc, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pc.Close()
		defer ftsConvertRestore(t, p, pc)()
		if _, err := pc.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := pc.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Error(err)
			}
		}()
		if _, err := pc.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err, dump.String())
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		ftsArrayConfigRun(t, p, pg, pc, fixture.Exec("SET default_text_search_config='english'", 0))
		ftsArrayConfigControlCheck(t, p, pg, pc)
	}
}
