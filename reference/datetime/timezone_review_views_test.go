package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneReviewViewsSessionCapture(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, s := range []fixture.Step{
		fixture.Exec("SET TIME ZONE 'UTC'", 0),
		fixture.Exec("SET DateStyle='ISO, MDY'", 0),
		fixture.Exec("CREATE TABLE tz_view_source(id INT)", 0),
		fixture.Exec("INSERT INTO tz_view_source VALUES(1)", 1),
		fixture.Exec("CREATE VIEW tz_view AS SELECT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS same,DATE '2003-01-02'::TEXT AS rendered FROM tz_view_source", 0),
		fixture.Exec("CREATE VIEW tz_nested WITH(security_invoker=true) AS SELECT same,rendered FROM tz_view", 0),
		fixture.Exec("ALTER TABLE tz_view_source RENAME TO tz_view_pinned", 0),
		fixture.Exec("CREATE TABLE tz_view_source(id INT)", 0),
		fixture.Exec("INSERT INTO tz_view_source VALUES(2),(3)", 2),
	} {
		p.Step(t, s)
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, name := range []string{"tz_view", "tz_nested"} {
				q := "SELECT same,rendered FROM " + name
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct {
					zone, style, text string
					want              bool
				}{
					{"UTC", "ISO, MDY", "2003-01-02", false},
					{"America/New_York", "SQL, DMY", "02/01/2003", true},
					{"UTC", "German, DMY", "02.01.2003", false},
				} {
					for _, setting := range []string{"SET TIME ZONE '" + tc.zone + "'", "SET DateStyle='" + tc.style + "'"} {
						if _, err := c.ExecContext(p.Ctx, setting); err != nil {
							t.Fatal(err)
						}
					}
					s := fixture.Select(q, []string{"same", "rendered"}, []string{"BOOL", "TEXT"}, [][]any{{tc.want, tc.text}})
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, backend.Recorder, s)
				}
				stmt.Close()
			}
		})
	}
}
