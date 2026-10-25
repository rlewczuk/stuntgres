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

func infoYesOrNoDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	script := dump.String()
	if !strings.Contains(script, `"information_schema"."yes_or_no"`) {
		t.Fatalf("Dump lost qualified domain declaration: %s", script)
	}
	if strings.Contains(strings.ToUpper(script), "CREATE DOMAIN") {
		t.Fatalf("Dump recreated builtin domain: %s", script)
	}
	return script
}

func infoYesOrNoRestored(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
	infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStored(false))
	infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
	infoYesOrNoIdentity(t, p, conn, recorder)
	for _, input := range []string{"abc", "YESx"} {
		s := fixture.ErrorDiagnostic("INSERT INTO public.info_yes_or_no_witness VALUES (90,'YES'),(91,'"+input+"')", infoYesOrNoDiagnostic(input))
		s.ErrorStage = "execute"
		infoYesOrNoBoth(t, p, conn, recorder, s)
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
	}
}

func TestReferenceInfoYesOrNoNativeControls(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoYesOrNoSeed(t, p, conn, recorder)
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStored(backend.PG))
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
		infoYesOrNoIdentity(t, p, conn, recorder)
		// Shared SQL acceptance precedes the explicit native-only API boundary.
		if backend.PG {
			return
		}
		script := infoYesOrNoDump(t, p)
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStored(false))
		retained, err := backend.DB.QueryContext(p.Ctx, infoYesOrNoStored(false).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		infoYesOrNoColumnTypes(t, columns, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Error("SELECT v FROM public.info_yes_or_no_witness", "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoYesOrNoRestored(t, p, conn, recorder)
		infoYesOrNoColumnTypes(t, columns, false)
		var old [][]any
		for retained.Next() {
			var value any
			if err := retained.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if len(old) >= 3 {
				t.Fatal("retained cursor returned extra row")
			}
			infoYesOrNoStored(false).CheckRaw(t, 0, len(old), value)
			old = append(old, []any{value})
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, infoYesOrNoStored(false).Rows) {
			t.Fatalf("retained rows=%#v want %#v", old, infoYesOrNoStored(false).Rows)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		infoYesOrNoColumnTypes(t, columns, false)

		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE info_yes_or_no_remap_spacer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoYesOrNoRestored(t, target, target.Memory, target.MemoryRecord)
		second := infoYesOrNoDump(t, target)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		infoYesOrNoRestored(t, target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.info_yes_or_no_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.info_yes_or_no_sentinel VALUES (42)", 1))
		preserved := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.info_yes_or_no_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT v FROM public.info_yes_or_no_staged", "42P01"))
			infoYesOrNoRestored(t, target, target.Memory, target.MemoryRecord)
		}
		for _, input := range []string{"abc", "YESx"} {
			bad := "CREATE TABLE public.info_yes_or_no_staged(v information_schema.yes_or_no); INSERT INTO public.info_yes_or_no_staged VALUES ('" + input + "');"
			err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
			d := infoYesOrNoDiagnostic(input)
			fixture.AssertDiagnostic(t, err, &d)
			preserved()
		}
		err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE public.info_yes_or_no_staged(v information_schema.yes_or_no); INSERT INTO"))
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

func TestReferenceInfoYesOrNoFeatureSmoke(t *testing.T) {
	infoYesOrNoScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec("INSERT INTO public.info_yes_or_no_witness VALUES (1,$1),(2,$2),(3,$3)", 3, "YES", "NO", nil)))
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStored(backend.PG))
		infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
		infoYesOrNoIdentity(t, p, conn, recorder)
		for _, input := range []string{"abc", "YESx"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO public.info_yes_or_no_witness VALUES (90,'YES'),(91,'"+input+"')", infoYesOrNoDiagnostic(input))
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				infoYesOrNoBoth(t, p, conn, recorder, infoYesOrNoStoredIDs())
			}
		}
		if backend.PG {
			return
		}
		script := infoYesOrNoDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		infoYesOrNoRestored(t, p, conn, recorder)
	})
}
