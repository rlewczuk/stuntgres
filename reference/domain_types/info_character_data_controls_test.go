package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCharacterDataDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	script := dump.String()
	if !strings.Contains(script, `"information_schema"."character_data"`) {
		t.Fatalf("Dump lost qualified domain declaration: %s", script)
	}
	if strings.Contains(strings.ToUpper(script), "CREATE DOMAIN") {
		t.Fatalf("Dump recreated builtin domain: %s", script)
	}
	return script
}

func infoCharacterDataObservations(t *testing.T, p *fixture.Pair, pg bool, conn fixture.Executor, recorder *fixture.Recorder, want [][]any, inputs []string) {
	t.Helper()
	infoCharacterDataRead(t, p, pg, conn, recorder, want)
	infoCharacterDataIdentity(t, p, conn, recorder)
	infoCharacterDataRejectInputs(t, p, pg, conn, recorder, want, inputs)
}

func infoCharacterDataRestored(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, want [][]any, inputs []string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
	infoCharacterDataObservations(t, p, false, conn, recorder, want, inputs)
}

func infoCharacterDataRoundTrip(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, want [][]any, inputs []string) {
	t.Helper()
	// Every shared query and held statement has closed before native controls.
	script := infoCharacterDataDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Error("SELECT v FROM public.info_character_data_witness", "42P01"))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	infoCharacterDataRestored(t, p, conn, recorder, want, inputs)
}

func TestReferenceInfoCharacterDataNativeControls(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		want := infoCharacterDataSeed(t, p, conn, recorder, false)
		inputs := []string{"a\x00b", string([]byte{0xff})}
		infoCharacterDataObservations(t, p, backend.PG, conn, recorder, want, inputs)
		// PostgreSQL shares SQL acceptance, not a fabricated native control API.
		if backend.PG {
			return
		}
		infoCharacterDataRoundTrip(t, p, conn, recorder, want, inputs)
	})
}

func TestReferenceInfoCharacterDataNativeFreshRestore(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		want := infoCharacterDataSeed(t, p, conn, recorder, true)
		inputs := []string{"a\x00b", string([]byte{0xff})}
		infoCharacterDataObservations(t, p, backend.PG, conn, recorder, want, inputs)
		if backend.PG {
			return
		}
		script := infoCharacterDataDump(t, p)
		// NewScenario owns a separate Driver, not merely another pool on p.
		target := fixture.NewScenario(t)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoCharacterDataRestored(t, target, target.Memory, target.MemoryRecord, want, inputs)
	})
}
