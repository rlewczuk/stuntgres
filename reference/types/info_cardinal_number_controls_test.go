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

func infoCardinalNumberDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	script := dump.String()
	if !strings.Contains(script, `"information_schema"."cardinal_number"`) {
		t.Fatalf("Dump lost qualified domain declaration: %s", script)
	}
	if strings.Contains(strings.ToUpper(script), "CREATE DOMAIN") {
		t.Fatalf("Dump recreated builtin domain: %s", script)
	}
	return script
}

func infoCardinalNumberRestored(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
	infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStored(false))
	infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
	infoCardinalNumberIdentity(t, p, conn, recorder)
	for _, input := range []string{"-1", "bad"} {
		s := fixture.ErrorDiagnostic("INSERT INTO public.info_cardinal_number_witness VALUES (90,7),(91,'"+input+"')", infoCardinalNumberDiagnostic(input))
		if input == "bad" {
			s.Diagnostic.Position = "67"
		}
		for _, prepared := range []bool{false, true} {
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared && input == "bad" {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
		}
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
	}
}

func TestReferenceInfoCardinalNumberNativeControls(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCardinalNumberSeed(t, p, conn, recorder)
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStored(backend.PG))
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
		infoCardinalNumberIdentity(t, p, conn, recorder)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO public.info_cardinal_number_witness VALUES (90,7),(91,-1)", infoCardinalNumberDiagnostic("-1"))
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, conn, recorder, s)
			infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
		}
		// Shared SQL acceptance precedes the explicit native-only API boundary.
		if backend.PG {
			return
		}
		script := infoCardinalNumberDump(t, p)
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStored(false))
		retained, err := backend.DB.QueryContext(p.Ctx, infoCardinalNumberStored(false).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberColumnTypes(t, columns, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Error("SELECT v FROM public.info_cardinal_number_witness", "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberRestored(t, p, conn, recorder)
		infoCardinalNumberColumnTypes(t, columns, false)
		var old [][]any
		for retained.Next() {
			var value any
			if err := retained.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if len(old) >= 3 {
				t.Fatal("retained cursor returned extra row")
			}
			infoCardinalNumberStored(false).CheckRaw(t, 0, len(old), value)
			old = append(old, []any{value})
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, infoCardinalNumberStored(false).Rows) {
			t.Fatalf("retained rows=%#v want %#v", old, infoCardinalNumberStored(false).Rows)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberColumnTypes(t, columns, false)

		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE info_cardinal_number_remap_spacer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
		second := infoCardinalNumberDump(t, target)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.info_cardinal_number_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.info_cardinal_number_sentinel VALUES (42)", 1))
		preserved := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.info_cardinal_number_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT v FROM public.info_cardinal_number_staged", "42P01"))
			infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
		}
		for _, input := range []string{"-1", "bad"} {
			bad := "CREATE TABLE public.info_cardinal_number_staged(v information_schema.cardinal_number); INSERT INTO public.info_cardinal_number_staged VALUES ('" + input + "');"
			err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
			d := infoCardinalNumberDiagnostic(input)
			if input == "bad" {
				d.Position = "143"
			}
			fixture.AssertDiagnostic(t, err, &d)
			preserved()
		}
		err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE public.info_cardinal_number_staged(v information_schema.cardinal_number); INSERT INTO"))
		fixture.State(t, err, "42601")
		preserved()
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(script)); !errors.Is(err, context.Canceled) {
			t.Fatalf("precanceled Restore=%v", err)
		}
		preserved()
	})
}

func TestReferenceInfoCardinalNumberFeatureSmoke(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec("INSERT INTO public.info_cardinal_number_witness VALUES (1,$1),(2,$2),(3,$3)", 3, int64(0), int64(7), nil)))
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStored(backend.PG))
		infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
		infoCardinalNumberIdentity(t, p, conn, recorder)
		for _, input := range []string{"-1", "bad"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO public.info_cardinal_number_witness VALUES (90,7),(91,'"+input+"')", infoCardinalNumberDiagnostic(input))
				s.Prepared, s.ErrorStage = prepared, "execute"
				if input == "bad" {
					s.Diagnostic.Position = "67"
					if prepared {
						s.ErrorStage = "prepare"
					}
				}
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				infoCardinalNumberBoth(t, p, conn, recorder, infoCardinalNumberStoredIDs())
			}
		}
		if backend.PG {
			return
		}
		script := infoCardinalNumberDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberRestored(t, p, conn, recorder)
	})
}
