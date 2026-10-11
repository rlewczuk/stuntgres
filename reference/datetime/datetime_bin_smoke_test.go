package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeBinFeatureSmoke(t *testing.T) {
	t.Run("typed-boundaries", func(t *testing.T) {
		datetimeBinScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE db_smoke(id integer,s timestamp,z timestamptz,o timestamp)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO db_smoke VALUES(1,TIMESTAMP '2000-01-01 00:02:29.999999',TIMESTAMPTZ '2000-01-01 00:02:29.999999+00',TIMESTAMP '2000-01-01 00:02:30'),(2,TIMESTAMP '2000-01-01 00:02:30',TIMESTAMPTZ '2000-01-01 00:02:30+00',TIMESTAMP '2000-01-01 00:02:30'),(3,TIMESTAMP '2000-01-01 00:17:29.999999',TIMESTAMPTZ '2000-01-01 00:17:29.999999+00',TIMESTAMP '2000-01-01 00:02:30'),(4,TIMESTAMP '2000-01-01 00:17:30',TIMESTAMPTZ '2000-01-01 00:17:30+00',TIMESTAMP '2000-01-01 00:02:30')", 4))
			want := []string{"1999-12-31T23:47:30Z", "2000-01-01T00:02:30Z", "2000-01-01T00:02:30Z", "2000-01-01T00:17:30Z"}
			var expected [][]any
			for _, value := range want {
				instant := datetimeTruncInstant(value)
				expected = append(expected, []any{instant, instant})
			}
			s := fixture.Select("SELECT date_bin('15 minutes',s,o) AS stamp,date_bin('15 minutes',z,o::timestamptz) AS instant FROM db_smoke ORDER BY id", []string{"stamp", "instant"}, []string{"TIMESTAMP", "TIMESTAMPTZ"}, expected)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				datetimeTruncMetadata(t, b.PG, columns, []string{"stamp", "instant"}, []string{"TIMESTAMP", "TIMESTAMPTZ"})
			}
			datetimeExtractBoth(t, p, b, c, s)
			datetimeTruncValue(t, p, b, c, datetimeBinCall("30 minutes", "timestamp", "2024-02-01 15:00", "2024-02-01 17:00"), "TIMESTAMP", "2024-02-01T15:00:00Z")
			datetimeTruncValue(t, p, b, c, "date_bin('1 day',DATE '2000-01-02',DATE '2000-01-01')", "TIMESTAMPTZ", "2000-01-02T00:00:00Z")
			datetimeTruncValue(t, p, b, c, "date_bin('1 day',TIMESTAMP '2000-01-02',TIMESTAMPTZ '2000-01-01+00')", "TIMESTAMPTZ", "2000-01-02T00:00:00Z")
			datetimeExtractFailure(t, p, b, c, "SELECT date_bin('0',TIMESTAMP '2000-01-02',TIMESTAMP '2000-01-01')", fixture.ControlsReferenceDiagnostic("22008", "stride must be greater than zero"), "execute")
		})
	})
	t.Run("held-recovery-null-import", func(t *testing.T) { datetimeBinScenario(t, datetimeBinHeld) })
	t.Run("failed-later-write", func(t *testing.T) { datetimeBinScenario(t, datetimeBinPublication) })
	t.Run("stored-controls-replay", func(t *testing.T) { datetimeBinScenario(t, datetimeBinStored) })
}
