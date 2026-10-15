package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateConsumers(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		projection := hypotheticalProjection("20", "v", "")
		for _, q := range []string{
			"WITH a AS (SELECT " + projection + " FROM hs_input) SELECT * FROM a",
			"SELECT * FROM (SELECT " + projection + " FROM hs_input) AS a",
			"SELECT (SELECT rank(20) WITHIN GROUP(ORDER BY v) FROM hs_input) AS rank,(SELECT dense_rank(20) WITHIN GROUP(ORDER BY v) FROM hs_input) AS dense_rank,(SELECT percent_rank(20) WITHIN GROUP(ORDER BY v) FROM hs_input) AS percent_rank,(SELECT cume_dist(20) WITHIN GROUP(ORDER BY v) FROM hs_input) AS cume_dist",
		} {
			orderedSetBoth(t, p, c, b, hypotheticalResult(q, hypotheticalRow(2, 2, .25, .8)))
		}
		orderedSetBoth(t, p, c, b, fixture.Select("SELECT count(*) AS n,percentile_disc(.5) WITHIN GROUP(ORDER BY v) AS p FROM hs_input HAVING rank(20) WITHIN GROUP(ORDER BY v)=2 ORDER BY dense_rank(30) WITHIN GROUP(ORDER BY v)", []string{"n", "p"}, []string{"INT8", "INT4"}, [][]any{{int64(4), int64(20)}}))
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT coalesce(rank(20) WITHIN GROUP(ORDER BY v),99)+1 AS v FROM hs_input", "INT8", int64(3)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_result(r BIGINT,d BIGINT,p FLOAT8,c FLOAT8)", 0))
		for _, tail := range []string{"", " WHERE FALSE", " WHERE FALSE GROUP BY v"} {
			count := int64(1)
			if tail == " WHERE FALSE GROUP BY v" {
				count = 0
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_result SELECT "+projection+" FROM hs_input"+tail, count))
		}
		orderedSetBoth(t, p, c, b, fixture.Select("SELECT r,d,p,c FROM hs_result ORDER BY r", []string{"r", "d", "p", "c"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8"}, [][]any{hypotheticalRow(1, 1, 0, 1), hypotheticalRow(2, 2, .25, .8)}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_ctas AS SELECT "+projection+" FROM hs_input", 1))
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT * FROM hs_ctas", hypotheticalRow(2, 2, .25, .8)))
		orderedSetBoth(t, p, c, b, fixture.Select("SELECT g,rank(g) WITHIN GROUP(ORDER BY v) AS r FROM (VALUES(1,1),(1,3),(2,2)) AS a(g,v) GROUP BY GROUPING SETS((g),()) ORDER BY g NULLS LAST", []string{"g", "r"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {nil, int64(4)}}))
		// The second direct child is the only source of this late group error.
		q := "INSERT INTO hs_result(r) SELECT rank(1,1/g) WITHIN GROUP(ORDER BY v,v) FROM (VALUES(1,1),(0,2)) AS a(g,v) GROUP BY g ORDER BY g DESC"
		s := orderedSetDiagnostic(q, "22012", "division by zero", "", "")
		s.ErrorStage = "execute"
		orderedSetBoth(t, p, c, b, s)
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT count(*) AS v FROM hs_result", "INT8", int64(2)))
	})
}

func TestReferenceHypotheticalAggregateExecution(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		for _, filter := range []string{"FALSE", "NULL"} {
			orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("20,20", "1/(v-v),1/(v-v)", " FILTER(WHERE "+filter+")")+" FROM hs_input", hypotheticalRow(1, 1, 0, 1)))
		}
		// Empty keyed input has no executed direct list at all.
		orderedSetBoth(t, p, c, b, hypotheticalResult("SELECT "+hypotheticalProjection("1/(SELECT 0),1/(SELECT 0)", "v,v", "")+" FROM hs_input WHERE FALSE GROUP BY v"))
		q := "SELECT rank(1,1/(SELECT 0)) WITHIN GROUP(ORDER BY v,v) FROM hs_input WHERE FALSE"
		orderedSetRuntimeError(t, p, c, b, q, "22012", "division by zero")
		// A direct expression evaluates once per actual call/group, left to right.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE hs_effect START 1", 0))
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT rank(nextval('hs_effect'),nextval('hs_effect')) WITHIN GROUP(ORDER BY v,v) AS v FROM hs_input WHERE FALSE", "INT8", int64(1)))
		orderedSetBoth(t, p, c, b, orderedSetValue("SELECT currval('hs_effect') AS v", "INT8", int64(4)))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE hs_left START 1", 0))
		q = "SELECT rank(1/(SELECT 0),nextval('hs_left')) WITHIN GROUP(ORDER BY v,v) FROM hs_input WHERE FALSE"
		orderedSetRuntimeError(t, p, c, b, q, "22012", "division by zero")
		// These are two actual executions, so nextval advances once on each.
		for i, prepared := range []bool{false, true} {
			s := orderedSetValue("SELECT nextval('hs_left') AS v", "INT8", int64(i+1))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceHypotheticalAggregatePlainRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE hs_plain(v INT)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO hs_plain VALUES(10),(20),(20),(40)", 4))
			for _, transaction := range []bool{false, true} {
				for _, plain := range []bool{true, false} {
					var source fixture.Executor = b.DB
					var tx *sql.Tx
					if transaction {
						var err error
						if plain {
							tx, err = b.DB.Begin()
						} else {
							tx, err = b.DB.BeginTx(p.Ctx, nil)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						source = tx
					}
					for _, prepared := range []bool{false, true} {
						s := hypotheticalResult("SELECT "+hypotheticalProjection("20", "v", "")+" FROM hs_plain", hypotheticalRow(2, 2, .25, .8))
						s.Prepared, s.NonContext = prepared, plain
						fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
						if tx != nil {
							fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec("SAVEPOINT hs_plain_error", 0))
						}
						q := "SELECT rank(1) WITHIN GROUP(ORDER BY 1,2)"
						s = hypotheticalArity(q, "rank", "integer, integer, integer", 1, 2)
						s.Prepared, s.NonContext = prepared, plain
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
						if tx != nil {
							fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec("ROLLBACK TO hs_plain_error", 0))
						}
					}
					if tx != nil {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		})
	}
}
