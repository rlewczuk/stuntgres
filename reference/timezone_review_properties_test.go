//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTimezoneReviewSessionConditionalBehavior(t *testing.T) {
	p := timezoneReferencePair(t)
	const query = "SELECT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00') IS NULL AS n,CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS a,CASE TIMESTAMPTZ '2004-10-19 04:00+00' WHEN DATE '2004-10-19' THEN TRUE ELSE FALSE END AS b FROM tz_witness WHERE id=1"
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"native", p.memory}, {"postgres", p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			if _, err := backend.db.ExecContext(p.ctx, "SET TIME ZONE 'UTC'"); err != nil {
				t.Fatal(err)
			}
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				zone string
				want bool
			}{{"UTC", false}, {"America/New_York", true}, {"UTC", false}} {
				if _, err := backend.db.ExecContext(p.ctx, "SET TIME ZONE '"+tc.zone+"'"); err != nil {
					t.Fatal(err)
				}
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(p.ctx)
					} else {
						rows, err = backend.db.QueryContext(p.ctx, query)
					}
					if err != nil {
						t.Fatal(err)
					}
					var n, a, b bool
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing conditional row")
					}
					if err := rows.Scan(&n, &a, &b); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if n != tc.want || a != tc.want || b != tc.want || rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatalf("zone=%s prepared=%t: %t %t %t want %t", tc.zone, prepared, n, a, b, tc.want)
					}
					rows.Close()
				}
			}
		})
	}
}

func TestReferenceTimezoneReviewVolatileATBehavior(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("INSERT INTO tz_witness(id) VALUES(2),(3)", 2))
	for _, prepared := range []bool{false, true} {
		for _, suffix := range []string{"AT LOCAL", "AT TIME ZONE 'UTC'"} {
			p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
			p.step(t, referenceExec("CREATE SEQUENCE tz_review_seq", 0))
			query := "SELECT ((CASE WHEN nextval('tz_review_seq')=2 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END) " + suffix + ")::TEXT AS v FROM tz_witness ORDER BY id"
			for run := range 2 {
				want := [][]any{{"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}}
				if run == 0 {
					want[1][0] = "2004-10-19 00:00:00+00"
				}
				s := referenceSelect(query, []string{"v"}, []string{"TEXT"}, want)
				s.prepared = prepared
				p.step(t, s)
				p.step(t, referenceSelect("SELECT currval('tz_review_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3 * (run + 1))}}))
			}
			p.step(t, referenceExec("DROP SEQUENCE tz_review_seq", 0))
		}
	}
}
