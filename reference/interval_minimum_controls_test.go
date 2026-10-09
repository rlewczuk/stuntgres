package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalMinimumTimeStyleRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	source := p.native
	p.step(t, referenceExec("CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL '-9223372036854775808 microseconds')", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES(1,'-9223372036854775808 microseconds')", 1))
	p.step(t, referenceExec("INSERT INTO iv(id) VALUES(2)", 1))
	p.step(t, referenceExec("CREATE VIEW ivview AS SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv WHERE id=1", 0))
	p.step(t, referenceExec("CREATE TABLE ivcomponents(id INTEGER,v INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO ivcomponents VALUES(1,'-14 months +3 days -9223372036854775808 microseconds'),(2,'-2562047788:00:54.775807'),(3,'2562047788:00:54.775807'),(4,'0'),(5,NULL),(6,'infinity'),(7,'-infinity')", 7))
	p.step(t, referenceSelect("SELECT v FROM iv ORDER BY id", []string{"v"}, []string{"INTERVAL"}, [][]any{{"-2562047788:00:54.775808"}, {"-2562047788:00:54.775808"}}))
	p.step(t, referenceSelect("SELECT id,v FROM ivcomponents ORDER BY id", []string{"id", "v"}, []string{"INT4", "INTERVAL"}, [][]any{{int64(1), "-1 years -2 mons +3 days -2562047788:00:54.775808"}, {int64(2), "-2562047788:00:54.775807"}, {int64(3), "2562047788:00:54.775807"}, {int64(4), "00:00:00"}, {int64(5), nil}, {int64(6), "infinity"}, {int64(7), "-infinity"}}))
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		intervalNativeExec(t, source, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, source, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
		for _, restoreStyle := range []struct{ name, out string }{
			{"postgres", "-2562047788:00:54.775808"},
			{"postgres_verbose", "@ 2562047788 hours 54.775808 secs ago"},
			{"sql_standard", "-2562047788:00:54.775808"},
			{"iso_8601", "PT-2562047788H-54.775808S"},
		} {
			destNative, dest, destRecord := intervalReferenceDestination(t, p, t.Name()+"/"+style+"/"+restoreStyle.name)
			queryCtx := context.WithValue(ctx, referenceRecorderKey{}, destRecord)
			intervalNativeExec(t, dest, "SET IntervalStyle="+restoreStyle.name)
			if err := stuntgres.Restore(ctx, destNative, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(style, err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(ctx, destNative, &redump); err != nil || redump.String() != canonical.String() {
				t.Fatal(style, err, "changed minimum-time redump")
			}
			var gotStyle string
			if err := dest.QueryRow("SHOW IntervalStyle").Scan(&gotStyle); err != nil || gotStyle != restoreStyle.name {
				t.Fatal(gotStyle, err)
			}
			for _, q := range []string{"SELECT v FROM iv ORDER BY id", "SELECT v FROM ivview"} {
				rows, err := dest.QueryContext(queryCtx, q)
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{restoreStyle.out}}
				if q == "SELECT v FROM iv ORDER BY id" {
					want = append(want, []any{restoreStyle.out})
				}
				intervalRawRows(t, rows, want, destRecord)
			}
			intervalNativeExec(t, dest, "SET IntervalStyle=postgres")
			for _, q := range []string{"SELECT v FROM iv ORDER BY id", "SELECT v FROM ivview"} {
				rows, err := dest.QueryContext(queryCtx, q)
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{"-2562047788:00:54.775808"}}
				if q == "SELECT v FROM iv ORDER BY id" {
					want = append(want, []any{"-2562047788:00:54.775808"})
				}
				intervalRawRows(t, rows, want, destRecord)
			}
			componentRows, err := dest.QueryContext(queryCtx, "SELECT id,v FROM ivcomponents ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			intervalRawRows(t, componentRows, [][]any{{int64(1), "-1 years -2 mons +3 days -2562047788:00:54.775808"}, {int64(2), "-2562047788:00:54.775807"}, {int64(3), "2562047788:00:54.775807"}, {int64(4), "00:00:00"}, {int64(5), nil}, {int64(6), "infinity"}, {int64(7), "-infinity"}}, destRecord)
			intervalNativeExec(t, dest, "INSERT INTO iv(id) VALUES(3)")
			rows, err := dest.QueryContext(queryCtx, "SELECT v FROM iv WHERE id=3")
			if err != nil {
				t.Fatal(err)
			}
			intervalRawRows(t, rows, [][]any{{"-2562047788:00:54.775808"}}, destRecord)
			var before bytes.Buffer
			if err := stuntgres.Dump(ctx, destNative, &before); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := stuntgres.Restore(canceled, destNative, bytes.NewReader(canonical.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			intervalDumpMatches(t, ctx, destNative, before.Bytes())
			if _, err := dest.ExecContext(canceled, "INSERT INTO iv VALUES(4,'-9223372036854775808 microseconds')"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			intervalDumpMatches(t, ctx, destNative, before.Bytes())
			if err := stuntgres.Restore(ctx, destNative, bytes.NewBufferString("CREATE TABLE broken(v INTERVAL); INSERT INTO broken VALUES('1 second'),('-2562047788:00:54.775808')")); err == nil {
				t.Fatal("invalid clock restore succeeded")
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(ctx, destNative, &after); err != nil || after.String() != before.String() {
				t.Fatal(err, "failed/canceled operation changed catalog")
			}
			if err := dest.QueryRow("SHOW IntervalStyle").Scan(&gotStyle); err != nil || gotStyle != "postgres" {
				t.Fatal(gotStyle, err)
			}
		}
	}
}
