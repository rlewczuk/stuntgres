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

func infoTimeStampDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	script := dump.String()
	if !strings.Contains(script, `"information_schema"."time_stamp"`) {
		t.Fatalf("Dump lost qualified domain declaration: %s", script)
	}
	if strings.Contains(strings.ToUpper(script), "CREATE DOMAIN") {
		t.Fatalf("Dump recreated a builtin domain: %s", script)
	}
	return script
}

func TestReferenceInfoTimeStampNativeControls(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoTimeStampSeed(t, p, conn, recorder)
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampIdentityStep())
		// Shared SQL acceptance above precedes the native API boundary.
		if backend.PG {
			return
		}
		script := infoTimeStampDump(t, p)
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(false))
		retained, err := backend.DB.QueryContext(p.Ctx, infoTimeStampStored(false).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		infoTimeStampMetadata(t, retained, false)
		retainedColumns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Error("SELECT v FROM public.info_time_stamp_witness", "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle='ISO, MDY'", "SET search_path TO public"} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
		}
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(false))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampIdentityStep())
		infoTimeStampMetadata(t, retained, false)
		var old [][]any
		for retained.Next() {
			var value any
			if err := retained.Scan(&value); err != nil {
				t.Fatal(err)
			}
			step := infoTimeStampStored(false)
			step.CheckRaw(t, 0, len(old), value)
			old = append(old, []any{fixture.Normalize(value)})
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(old, infoTimeStampStored(false).Rows) {
			t.Fatalf("retained values=%#v", old)
		}
		infoTimeStampColumnTypes(t, retainedColumns, false)
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		infoTimeStampColumnTypes(t, retainedColumns, false)

		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE info_time_stamp_remap_spacer", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		check := func() {
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle='ISO, MDY'", "SET search_path TO public"} {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
			}
			infoTimeStampBoth(t, target, target.Memory, target.MemoryRecord, infoTimeStampStored(false))
			infoTimeStampBoth(t, target, target.Memory, target.MemoryRecord, infoTimeStampIdentityStep())
		}
		check()
		second := infoTimeStampDump(t, target)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		check()
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.info_time_stamp_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.info_time_stamp_sentinel VALUES (42)", 1))
		bad := "CREATE TABLE public.info_time_stamp_staged(v information_schema.time_stamp); INSERT INTO public.info_time_stamp_staged VALUES ('nonsense');"
		err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
		fixture.State(t, err, "22007")
		var diagnostic *stuntgres.Error
		if !errors.As(err, &diagnostic) || diagnostic.Message != `invalid input syntax for type timestamp with time zone: "nonsense"` {
			t.Fatalf("failed Restore diagnostic=%v", err)
		}
		checkSentinel := func() {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.info_time_stamp_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT v FROM public.info_time_stamp_staged", "42P01"))
			check()
		}
		checkSentinel()
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(script)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		checkSentinel()
	})
}

func TestReferenceInfoTimeStampFeatureSmoke(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (1,$1),(2,$2)", 2, "2004-10-19 10:23:54.126+02", nil)))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampIdentityStep())
		s := fixture.ImplicitError("INSERT INTO public.info_time_stamp_witness VALUES (3,$1)", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`, Context: "unnamed portal parameter $1 = '...'"}, "nonsense")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
		if backend.PG {
			return
		}
		script := infoTimeStampDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO public", 0))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(false))
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampIdentityStep())
	})
}
