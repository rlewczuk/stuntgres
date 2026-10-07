//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTimezoneReviewViewsSessionCapture(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("SET TIME ZONE 'UTC'", 0),
		referenceExec("SET DateStyle='ISO, MDY'", 0),
		referenceExec("CREATE TABLE tz_view_source(id INT)", 0),
		referenceExec("INSERT INTO tz_view_source VALUES(1)", 1),
		referenceExec("CREATE VIEW tz_view AS SELECT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS same,DATE '2003-01-02'::TEXT AS rendered FROM tz_view_source", 0),
		referenceExec("CREATE VIEW tz_nested WITH(security_invoker=true) AS SELECT same,rendered FROM tz_view", 0),
		referenceExec("ALTER TABLE tz_view_source RENAME TO tz_view_pinned", 0),
		referenceExec("CREATE TABLE tz_view_source(id INT)", 0),
		referenceExec("INSERT INTO tz_view_source VALUES(2),(3)", 2),
	} {
		p.step(t, s)
	}
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"native", p.memory}, {"postgres", p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, name := range []string{"tz_view", "tz_nested"} {
				q := "SELECT same,rendered FROM " + name
				stmt, err := c.PrepareContext(p.ctx, q)
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
						if _, err := c.ExecContext(p.ctx, setting); err != nil {
							t.Fatal(err)
						}
					}
					for _, prepared := range []bool{false, true} {
						var rows *sql.Rows
						if prepared {
							rows, err = stmt.QueryContext(p.ctx)
						} else {
							rows, err = c.QueryContext(p.ctx, q)
						}
						if err != nil {
							t.Fatal(err)
						}
						var same bool
						var rendered string
						if !rows.Next() {
							rows.Close()
							t.Fatal("missing view row", rows.Err())
						}
						if err := rows.Scan(&same, &rendered); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						if same != tc.want || rendered != tc.text || rows.Next() || rows.Err() != nil {
							rows.Close()
							t.Fatalf("%s prepared=%t zone=%s: %t %q want %t %q", name, prepared, tc.zone, same, rendered, tc.want, tc.text)
						}
						rows.Close()
					}
				}
				stmt.Close()
			}
		})
	}
}
