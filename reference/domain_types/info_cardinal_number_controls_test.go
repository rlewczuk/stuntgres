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

func infoCardinalNumberControlBaseline(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
	t.Helper()
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
}

func TestReferenceInfoCardinalNumberNativeControls(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCardinalNumberControlBaseline(t, p, backend, conn, recorder)
		// Shared SQL acceptance precedes the explicit native-only API boundary.
		if backend.PG {
			return
		}
		// Retained rows and metadata must cross Reset/Restore on this SAME owner.
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
	})
}

func infoCardinalNumberRestoreTarget(t *testing.T, script string) *fixture.Pair {
	t.Helper()
	target := fixture.NewScenario(t)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE info_cardinal_number_remap_spacer", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
	return target
}

func TestReferenceInfoCardinalNumberNativeFreshRestore(t *testing.T) {
	infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoCardinalNumberControlBaseline(t, p, backend, conn, recorder)
		if backend.PG {
			return
		}
		target := infoCardinalNumberRestoreTarget(t, infoCardinalNumberDump(t, p))
		// The second roundtrip stays on the same destination and logical domain.
		second := infoCardinalNumberDump(t, target)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second)); err != nil {
			t.Fatal(err)
		}
		infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
	})
}

func TestReferenceInfoCardinalNumberNativeRestoreAtomicity(t *testing.T) {
	for _, failure := range []string{"negative", "malformed", "syntax", "precancel"} {
		t.Run(failure, func(t *testing.T) {
			// Independent failed replacements get fresh bounded scenarios, not a
			// renewed clock on the retained-cursor or repeated-roundtrip owner.
			infoCardinalNumberScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
				infoCardinalNumberControlBaseline(t, p, backend, conn, recorder)
				if backend.PG {
					return
				}
				script := infoCardinalNumberDump(t, p)
				target := infoCardinalNumberRestoreTarget(t, script)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.info_cardinal_number_sentinel(v integer)", 0))
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.info_cardinal_number_sentinel VALUES (42)", 1))
				switch failure {
				case "negative", "malformed":
					input := "-1"
					if failure == "malformed" {
						input = "bad"
					}
					bad := "CREATE TABLE public.info_cardinal_number_staged(v information_schema.cardinal_number); INSERT INTO public.info_cardinal_number_staged VALUES ('" + input + "');"
					err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
					d := infoCardinalNumberDiagnostic(input)
					if input == "bad" {
						d.Position = "143"
					}
					fixture.AssertDiagnostic(t, err, &d)
				case "syntax":
					err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE public.info_cardinal_number_staged(v information_schema.cardinal_number); INSERT INTO"))
					fixture.State(t, err, "42601")
				case "precancel":
					ctx, cancel := context.WithCancel(target.Ctx)
					cancel()
					if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(script)); !errors.Is(err, context.Canceled) {
						t.Fatalf("precanceled Restore=%v", err)
					}
				}
				// Every failure preserves the SAME destination, including complete
				// direct/prepared enforcement and domain identity relationships.
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.info_cardinal_number_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT v FROM public.info_cardinal_number_staged", "42P01"))
				infoCardinalNumberRestored(t, target, target.Memory, target.MemoryRecord)
			})
		})
	}
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
