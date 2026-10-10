package reference_test

import "testing"

func TestReferenceQueryWindowRangePipeline(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRoutes(t, p, b, referenceSelect("SELECT id,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM range_t WHERE id>1 AND id<6 ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(2)}, {int64(3), int64(2)}, {int64(4), int64(1)}, {int64(5), int64(2)}}))
		// The presentation bound must not truncate the WindowAgg input.
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN CURRENT ROW AND 10 FOLLOWING) AS n FROM range_t ORDER BY id LIMIT 1", 5))
		queryWindowRoutes(t, p, b, referenceSelect("SELECT k,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM range_t WHERE k IS NOT NULL GROUP BY k HAVING count(*)>0 ORDER BY k", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(4), int64(1)}, {int64(5), int64(2)}}))
		queryWindowRun(t, p, b, referenceExec("CREATE SEQUENCE range_eval START WITH 1", 0))
		q := "SELECT id,count(*) OVER(PARTITION BY id%2 ORDER BY k RANGE (SELECT nextval('range_eval')) PRECEDING) AS n FROM range_t ORDER BY id"
		// Start expression is evaluated once for all partitions. The first execution
		// uses 1; the second uses 2. nextval is an existing public provider, not a probe.
		queryWindowRun(t, p, b, queryWindowRangeResult(q, 1, 1, 2, 1, 1, 1, 1))
		s := queryWindowRangeResult(q, 1, 1, 2, 2, 1, 1, 1)
		s.prepared = true
		queryWindowRun(t, p, b, s)
		queryWindowRoutes(t, p, b, referenceSelect("SELECT last_value FROM range_eval", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}

func TestReferenceQueryWindowRangeOffsetDomains(t *testing.T) {
	for _, tc := range []struct{ base, key, offset string }{
		{"INTEGER", "k", "1"}, {"NUMERIC", "k::NUMERIC", "1.00"}, {"INTERVAL", "DATE '2025-01-01'+k", "'1 day'"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
				queryWindowRangeSeed(t, p, b)
				queryWindowRun(t, p, b, referenceExec("CREATE DOMAIN range_bound AS "+tc.base, 0))
				offset := tc.offset + "::range_bound"
				if tc.base == "INTEGER" {
					q := "SELECT id,count(*) OVER(ORDER BY k RANGE " + offset + " PRECEDING) AS n FROM range_t ORDER BY id"
					queryWindowRangeAnalysis(t, p, b, q, "0A000", "RANGE with offset PRECEDING/FOLLOWING has multiple interpretations for column type integer and offset type range_bound", offset+" PRECEDING", "Cast the offset value to the exact intended type.")
					offset += "::INTEGER"
				}
				queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY "+tc.key+" RANGE "+offset+" PRECEDING) AS n FROM range_t ORDER BY id", 1, 3, 3, 1, 2, 2, 2))
			})
		})
	}
}

func TestReferenceQueryWindowRangeOffsetDependency(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRangeSeed(t, p, b)
		queryWindowRun(t, p, b, referenceExec("CREATE TABLE range_offset_source(v INT)", 0))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO range_offset_source VALUES(1)", 1))
		queryWindowRun(t, p, b, referenceExec("CREATE VIEW range_dependency AS SELECT id,count(*) OVER(ORDER BY k RANGE (SELECT v FROM range_offset_source) PRECEDING) AS n FROM range_t", 0))
		expected := queryWindowRangeResult("SELECT id,n FROM range_dependency ORDER BY id", 1, 3, 3, 1, 2, 2, 2)
		queryWindowRoutes(t, p, b, expected)
		q := "ALTER TABLE range_offset_source DROP COLUMN v RESTRICT"
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "2BP01", Message: "cannot drop column v of table range_offset_source because other objects depend on it", Detail: "view range_dependency depends on column v of table range_offset_source", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.prepared, s.errorStage = prepared, "execute"
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, expected)
		}
		queryWindowRun(t, p, b, referenceExec("ALTER TABLE range_offset_source RENAME COLUMN v TO renamed", 0))
		queryWindowRoutes(t, p, b, expected)
		if !b.pg {
			queryWindowRangeControls(t, p, []referenceStep{expected})
		}
	})
}

func TestReferenceQueryWindowRangeExecutionZone(t *testing.T) {
	queryWindowRangeScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowRun(t, p, b, referenceExec("CREATE TABLE zone_keys(id INT,k TIMESTAMPTZ)", 0))
		queryWindowRun(t, p, b, referenceExec("INSERT INTO zone_keys VALUES(1,'2025-03-08 11:30-05'),(2,'2025-03-08 12:00-05'),(3,'2025-03-09 12:00-04')", 3))
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "SELECT id,count(*) OVER(ORDER BY k RANGE INTERVAL '1 day' PRECEDING) AS n FROM zone_keys ORDER BY id"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{conn, stmt, q}
		for _, tc := range []struct {
			zone   string
			counts []int64
		}{{"UTC", []int64{1, 2, 3}}, {"America/New_York", []int64{1, 2, 2}}, {"UTC", []int64{1, 2, 3}}} {
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			referenceRunStep(t, p.ctx, held, b.recorder, queryWindowRangeResult(q, tc.counts...))
		}
	})
}
