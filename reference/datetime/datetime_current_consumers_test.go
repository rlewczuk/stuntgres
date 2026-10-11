package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeCurrentStoredConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			datetimeCurrentSeed(t, p.Ctx, conn)
			datetimeCurrentExec(t, p.Ctx, conn, "CREATE TABLE dc_stored(n INTEGER,d DATE DEFAULT CURRENT_DATE,t TIMETZ DEFAULT CURRENT_TIME(3),z TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,l TIME DEFAULT LOCALTIME(3),s TIMESTAMP DEFAULT LOCALTIMESTAMP,CHECK(d<=CURRENT_DATE))", 0)
			datetimeCurrentExec(t, p.Ctx, conn, "CREATE VIEW dc_view AS SELECT CURRENT_DATE,CURRENT_TIME(3),CURRENT_TIMESTAMP,LOCALTIME(3),LOCALTIMESTAMP FROM dc_anchor WHERE n=1", 0)
			// Reconstruct SQL from the existing deparser and execute the actual returned
			// definition, not a guessed renderer string or a freshly copied SELECT.
			var definition string
			if err := conn.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('dc_view'::TEXT)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			upper := strings.ToUpper(definition)
			for _, name := range []string{"CURRENT_DATE", "CURRENT_TIME(3)", "CURRENT_TIMESTAMP", "LOCALTIME(3)", "LOCALTIMESTAMP"} {
				if !strings.Contains(upper, name) {
					t.Errorf("view lost symbolic %s: %q", name, definition)
				}
			}
			datetimeCurrentExec(t, p.Ctx, conn, "CREATE VIEW dc_replayed AS "+strings.TrimSuffix(strings.TrimSpace(definition), ";"), 0)
			query := "SELECT d AS current_date,t AS current_time,z AS current_timestamp,l AS localtime,s AS localtimestamp FROM dc_stored WHERE n=1"
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			for range 2 {
				datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
				stamp := datetimeCurrentWitness(t, p.Ctx, conn)
				datetimeCurrentExec(t, p.Ctx, conn, "INSERT INTO dc_stored(n) VALUES(1)", 1)
				t.Run("direct_precision_cast", func(t *testing.T) {
					datetimeCurrentTextCheck(t, p.Ctx, conn, backend.Recorder, stamp, 0, 3)
				})
				// Explicit p3 defaults and view expressions independently should round;
				// source-labelled assertions distinguish assignment from view/replay.
				for _, q := range []string{query, "SELECT * FROM dc_view", "SELECT * FROM dc_replayed"} {
					t.Run("raw/"+q, func(t *testing.T) {
						datetimeCurrentCheckPrecisions(t, p.Ctx, conn, backend.PG, q, stamp, 0, 1, [5]int{6, 3, 6, 3, 6}, true)
					})
				}
				want6 := datetimeCurrentExpected(stamp, 0, 6)
				want3 := datetimeCurrentExpected(stamp, 0, 3)
				row := []any{want6[0], want3[1], want6[2], want3[3], want6[4]}
				for _, source := range []string{"dc_stored WHERE n=1", "dc_view", "dc_replayed"} {
					t.Run(source, func(t *testing.T) {
						columns := []string{"d", "t", "z", "l", "s"}
						if source != "dc_stored WHERE n=1" {
							columns = datetimeCurrentNames
						}
						projection := make([]string, 5)
						// Quote the keyword-labelled view columns: bare current_time etc.
						// would evaluate new SQL value functions instead of reading the view.
						for i, c := range columns {
							projection[i] = `"` + c + `"::TEXT AS "` + datetimeCurrentNames[i] + `"`
						}
						q := "SELECT " + strings.Join(projection, ",") + " FROM " + source
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(q, datetimeCurrentNames, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{row}))
					})
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT CURRENT_DATE=CURRENT_DATE AS same,count(*) AS n FROM dc_anchor GROUP BY CURRENT_DATE", []string{"same", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2)}}))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT a.n,CURRENT_TIMESTAMP=CURRENT_TIMESTAMP AS same FROM dc_anchor a JOIN dc_anchor b ON a.n=b.n AND LOCALTIME=LOCALTIME WHERE CURRENT_DATE=CURRENT_DATE ORDER BY a.n", []string{"n", "same"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}}))
				datetimeCurrentExec(t, p.Ctx, conn, "UPDATE dc_stored SET z=CURRENT_TIMESTAMP WHERE n=1", 1)
				datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
				fixture.TemporalAdvanceClock(t, p.Ctx, stamp)
			}
			for _, item := range []struct{ q, message string }{
				{"CREATE TABLE dc_generated(z TIMESTAMPTZ GENERATED ALWAYS AS (CURRENT_TIMESTAMP) STORED)", "generation expression is not immutable"},
				{"CREATE INDEX dc_stable_index ON dc_anchor ((CURRENT_TIMESTAMP))", "functions in index expression must be marked IMMUTABLE"},
			} {
				t.Run(item.q, func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ErrorDiagnostic(item.q, fixture.ControlsReferenceDiagnostic("42P17", item.message)))
				})
			}
			if backend.PG {
				// Deterministic server-side cancellation, followed by SAME-owner CURRENT
				// recovery. Native precancellation is deliberately a different control.
				fixture.SessionCancel(t, p.Ctx, conn, false, true)
				datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
				datetimeCurrentCheck(t, p.Ctx, conn, backend.PG, "SELECT "+datetimeCurrentProjection, datetimeCurrentWitness(t, p.Ctx, conn), 0, 6, 1)
				datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
			} else {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				q := "INSERT INTO dc_stored(n,z) VALUES(1,CURRENT_TIMESTAMP)"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, held := range []bool{false, true} {
					if held {
						rows, err := stmt.QueryContext(canceled)
						if rows != nil || !errors.Is(err, context.Canceled) {
							if rows != nil {
								rows.Close()
							}
							t.Fatal("held cancellation", rows, err)
						}
						result, err := stmt.ExecContext(canceled)
						if result != nil || !errors.Is(err, context.Canceled) {
							t.Fatal("held Exec cancellation", result, err)
						}
					} else {
						rows, err := conn.QueryContext(canceled, q)
						if rows != nil || !errors.Is(err, context.Canceled) {
							if rows != nil {
								rows.Close()
							}
							t.Fatal("direct cancellation", rows, err)
						}
						result, err := conn.ExecContext(canceled, q)
						if result != nil || !errors.Is(err, context.Canceled) {
							t.Fatal("direct Exec cancellation", result, err)
						}
						failed, err := conn.PrepareContext(canceled, q)
						if failed != nil || !errors.Is(err, context.Canceled) {
							if failed != nil {
								failed.Close()
							}
							t.Fatal("Prepare cancellation", failed, err)
						}
					}
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT n FROM dc_stored", []string{"n"}, []string{"INT4"}, nil))
				// Native eager output and metadata remain owned through settings/end/reset.
				datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
				stamp := datetimeCurrentWitness(t, p.Ctx, conn)
				capturedSQL := "SELECT " + datetimeCurrentProjection
				rows, err := conn.QueryContext(p.Ctx, capturedSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				datetimeCurrentExec(t, p.Ctx, conn, "SET TIME ZONE 'Pacific/Kiritimati'", 0)
				datetimeCurrentExec(t, p.Ctx, conn, "COMMIT", 0)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				admitted := timezoneAdmittedRows{Executor: conn, rows: rows}
				datetimeCurrentCheck(t, p.Ctx, admitted, false, capturedSQL, stamp, 0, 6, 1)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				datetimeCurrentExec(t, p.Ctx, conn, "SET TIME ZONE 'UTC'", 0)
				datetimeCurrentExec(t, p.Ctx, conn, "BEGIN", 0)
				fresh := datetimeCurrentWitness(t, p.Ctx, conn)
				if !fresh.After(stamp) {
					t.Fatal("restored definitions retained old clock", fresh, stamp)
				}
				datetimeCurrentExec(t, p.Ctx, conn, "INSERT INTO dc_stored(n) VALUES(1)", 1)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT z=CURRENT_TIMESTAMP AS same,s=LOCALTIMESTAMP AS local_same FROM dc_stored", []string{"same", "local_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
				datetimeCurrentExec(t, p.Ctx, conn, "ROLLBACK", 0)
			}
		})
	}
}
