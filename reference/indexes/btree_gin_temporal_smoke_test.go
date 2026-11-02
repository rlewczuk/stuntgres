package reference_test

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINTemporalFeatureSmoke(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		run(fixture.Exec("SET TimeZone='UTC'", 0))
		run(fixture.Exec("SET IntervalStyle='postgres'", 0))
		run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
		var columns, keys, params []string
		columns = append(columns, "id integer NOT NULL")
		params = append(params, "$1")
		for i, tc := range temporalCases {
			columns = append(columns, tc.typ+" "+tc.typ)
			keys = append(keys, tc.typ+" public."+tc.typ+"_ops")
			params = append(params, "$"+strconv.Itoa(i+2)+"::"+tc.typ)
		}
		run(fixture.Exec("CREATE TABLE temporal_smoke("+strings.Join(columns, ",")+")", 0))
		run(fixture.Exec("CREATE INDEX temporal_smoke_i ON temporal_smoke USING gin("+strings.Join(keys, ",")+")", 0))
		insert := "INSERT INTO temporal_smoke VALUES(" + strings.Join(params, ",") + ")"
		ins := classHeld(t, p, conn, insert)
		for row := range 7 {
			args := []any{row + 1}
			for _, tc := range temporalCases {
				var value any
				if text := temporalTexts(tc)[row]; text != "" {
					value = text
				}
				args = append(args, value)
			}
			s := fixture.Exec(insert, 1, args...)
			if row < 3 {
				run(s)
			} else {
				fixture.RunStep(t, p.Ctx, ins, b.Recorder, s)
			}
		}
		queries := make([]string, len(temporalCases))
		held := make([]fixture.HeldStatement, len(temporalCases))
		valueQueries := make([]string, len(temporalCases))
		valueHeld := make([]fixture.HeldStatement, len(temporalCases))
		for i, tc := range temporalCases {
			queries[i] = "SELECT id FROM temporal_smoke WHERE " + tc.typ + "=$1::" + tc.typ + " ORDER BY id"
			held[i] = classHeld(t, p, conn, queries[i])
			valueQueries[i] = "SELECT id," + tc.typ + " AS v FROM temporal_smoke WHERE id >= $1 ORDER BY id"
			valueHeld[i] = classHeld(t, p, conn, valueQueries[i])
		}
		check := func() {
			for i, tc := range temporalCases {
				classRoutes(t, p, b, conn, ginIDs(queries[i], []int64{3, 4}, tc.boundary))
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], []int64{3, 4}, tc.boundary))
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_smoke WHERE "+tc.typ+">$1::"+tc.typ+" ORDER BY id", []int64{5, 6}, tc.boundary))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_smoke WHERE "+tc.typ+" IS NULL ORDER BY id", []int64{7}))
				for _, v := range []struct {
					arg  any
					rows [][]any
				}{{1, temporalRows(b, tc)}, {99, nil}, {7, temporalRows(b, tc)[6:]}} {
					s := temporalValueStep(b, valueQueries[i], tc, v.rows, v.arg)
					classRoutes(t, p, b, conn, s)
					fixture.RunStep(t, p.Ctx, valueHeld[i], b.Recorder, s)
				}
			}
			classRoutes(t, p, b, conn, fixture.Select("SELECT '09:00+01'::timetz<'08:00+00'::timetz AS tie_lt,'09:00+01'::timetz='08:00+00'::timetz AS tie_eq,'1 mon'::interval='30 days'::interval AS span_eq", []string{"tie_lt", "tie_eq", "span_eq"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true}}))
		}
		check()
		ginUtilityError(t, p, b, conn, "CREATE INDEX temporal_smoke_bad ON temporal_smoke USING gin(date date_ops,timestamp time_ops)", fixture.ControlsReferenceDiagnostic("42804", `operator class "time_ops" does not accept data type timestamp without time zone`))
		compoundIndexAbsent(run, "temporal_smoke", "temporal_smoke_bad", 1)
		if !b.PG {
			var retained []networkRetained
			for i, tc := range temporalCases {
				s := temporalValueStep(b, valueQueries[i], tc, temporalRows(b, tc), 1)
				rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query, s.Args...)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { rows.Close() })
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				retained = append(retained, networkRetained{rows, meta, s})
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			temporalCheckRetained(t, b, retained)
		} else {
			if emitted == "" {
				t.Fatal("native temporal smoke Dump was not produced for PostgreSQL replay")
			}
			run(fixture.Exec("DROP TABLE temporal_smoke", 0))
			run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
			if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
				t.Fatalf("actual temporal smoke Dump replay: %v", err)
			}
		}
		check()
		temporalGraph(t, p, b, conn, "public")
		args := []any{90}
		for _, tc := range temporalCases {
			args = append(args, tc.boundary)
		}
		fixture.RunStep(t, p.Ctx, ins, b.Recorder, fixture.Exec(insert, 1, args...))
		for i, tc := range temporalCases {
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], []int64{3, 4, 90}, tc.boundary))
		}
		run(fixture.Prepared(fixture.Exec("UPDATE temporal_smoke SET timetz=$1::timetz,interval=$2::interval WHERE id=90", 1, "08:00+00", "720 hours")))
		fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], []int64{3, 4}, temporalCases[2].boundary))
		fixture.RunStep(t, p.Ctx, held[5], b.Recorder, ginIDs(queries[5], []int64{3, 4, 90}, "1 mon"))
		run(fixture.Exec("DELETE FROM temporal_smoke WHERE id=90", 1))
		check()
	})
}
