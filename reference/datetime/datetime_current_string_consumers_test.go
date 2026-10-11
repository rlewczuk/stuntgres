package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeCurrentStringConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.Ctx, conn)
			cases := []struct {
				query        string
				names, types []string
			}{
				{"SELECT normalize(CURRENT_DATE::TEXT) AS normalized", []string{"normalized"}, []string{"TEXT"}},
				{"SELECT CURRENT_DATE::TEXT IS NORMALIZED AS valid", []string{"valid"}, []string{"BOOL"}},
				{"SELECT CURRENT_DATE::TEXT AS d,normalize(CURRENT_DATE::TEXT) AS normalized,CURRENT_DATE::TEXT IS NORMALIZED AS valid", []string{"d", "normalized", "valid"}, []string{"TEXT", "TEXT", "BOOL"}},
			}
			statements := make([]*sql.Stmt, len(cases))
			for i, c := range cases {
				statements[i], err = conn.PrepareContext(p.Ctx, c.query)
				if err != nil {
					t.Fatal("prepare", c.query, err)
				}
				defer statements[i].Close()
			}
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
			stamp := datetimeCurrentWitness(t, p.Ctx, conn)
			var dates []string
			for _, zone := range []struct {
				sql  string
				east int
			}{
				{"SET TIME ZONE INTERVAL '+14:00' HOUR TO MINUTE", 14 * 3600},
				{"SET TIME ZONE INTERVAL '-12:00' HOUR TO MINUTE", -12 * 3600},
			} {
				datetimeCurrentExec(t, p.Ctx, conn, zone.sql, 0)
				// ISO ASCII dates are already NFC. The independent prerequisite
				// transaction witness and fixed offset derive the expected TEXT;
				// no CURRENT or normalization implementation computes this oracle.
				date := stamp.In(time.FixedZone("oracle", zone.east)).Format("2006-01-02")
				dates = append(dates, date)
				for i, c := range cases {
					values := []any{date}
					if i == 1 {
						values = []any{true}
					} else if i == 2 {
						values = []any{date, date, true}
					}
					step := fixture.Select(c.query, c.names, c.types, [][]any{values})
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
					held := fixture.HeldStatement{Executor: conn, Statement: statements[i], SQL: c.query}
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
				}
			}
			if dates[0] == dates[1] {
				t.Fatalf("independent +14/-12 boundary witness failed: stamp=%v dates=%v", stamp, dates)
			}
			datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
		})
	}
}
