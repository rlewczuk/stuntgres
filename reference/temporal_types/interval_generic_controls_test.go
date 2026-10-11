package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalGenericTypmodStyleRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	db := p.Native
	destNative, dest, destRecord := fixture.IntervalReferenceDestination(t, p, t.Name()+"/restore")
	queryCtx := context.WithValue(ctx, fixture.RecorderKey{}, destRecord)
	cases := []struct {
		mask int
		out  string
	}{{4, "1 year"}, {2, "1 year 1 mon"}, {8, "1 year 1 mon 2 days"}, {1024, "1 year 1 mon 2 days 03:00:00"}, {2048, "1 year 1 mon 2 days 03:04:00"}, {6, "1 year 1 mon"}, {1032, "1 year 1 mon 2 days 03:00:00"}, {3080, "1 year 1 mon 2 days 03:04:00"}, {3072, "1 year 1 mon 2 days 03:04:00"}}
	for i, tc := range cases {
		fixture.IntervalNativeExec(t, db, fmt.Sprintf(`CREATE TABLE g%d(v "interval"(%d,2))`, i, tc.mask))
		fixture.IntervalNativeExec(t, db, fmt.Sprintf("INSERT INTO g%d VALUES('13 months 2 days 3:04:05.6789')", i))
	}
	fixture.IntervalNativeExec(t, db, `CREATE TABLE ivdefault(id INTEGER,v "interval"(4,2) DEFAULT '13 months')`)
	fixture.IntervalNativeExec(t, db, "INSERT INTO ivdefault(id) VALUES(1)")
	fixture.IntervalNativeExec(t, db, `CREATE VIEW ivgenericview AS SELECT v FROM g0`)
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		fixture.IntervalNativeExec(t, db, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
		for _, restoreStyle := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
			fixture.IntervalNativeExec(t, dest, "SET IntervalStyle="+restoreStyle)
			if err := stuntgres.Restore(ctx, destNative, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(style, err, dump.String())
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(ctx, destNative, &redump); err != nil || redump.String() != canonical.String() {
				t.Fatal(style, err, "typmod/redump changed")
			}
			fixture.RunStep(t, p.Ctx, dest, destRecord, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{restoreStyle}}))
			fixture.IntervalNativeExec(t, dest, "SET IntervalStyle=postgres")
			for i, tc := range cases {
				rows, err := dest.QueryContext(queryCtx, fmt.Sprintf("SELECT v FROM g%d", i))
				if err != nil {
					t.Fatal(err)
				}
				fixture.IntervalRawRows(t, rows, [][]any{{tc.out}}, destRecord)
			}
			for _, q := range []string{"SELECT v FROM ivdefault", "SELECT v FROM ivgenericview"} {
				rows, err := dest.QueryContext(queryCtx, q)
				if err != nil {
					t.Fatal(err)
				}
				fixture.IntervalRawRows(t, rows, [][]any{{"1 year"}}, destRecord)
			}
		}
	}
}
