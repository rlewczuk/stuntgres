package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINTemporalSearch(t *testing.T) {
	for _, tc := range temporalCases {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("SET TimeZone='UTC'", 0))
				run(fixture.Exec("SET IntervalStyle='postgres'", 0))
				for _, table := range []string{"temporal_search", "temporal_scan", "temporal_delta"} {
					run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
				}
				temporalSeed(run, "temporal_search", tc)
				temporalSeed(run, "temporal_scan", tc)
				ops := []string{"<", "<=", "=", ">=", ">", "<>"}
				reverse := []string{">", ">=", "=", "<=", "<", "<>"}
				held := make([]fixture.HeldStatement, len(ops))
				queries := make([]string, len(ops))
				for i, op := range ops {
					queries[i] = "SELECT id FROM temporal_search WHERE v" + op + "$1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				valueSQL := "SELECT id,v FROM temporal_search WHERE id >= $1 ORDER BY id"
				valueHeld := classHeld(t, p, conn, valueSQL)
				check := func(tables []string) {
					t.Helper()
					for _, table := range tables {
						for i, op := range ops {
							q := "SELECT id FROM " + table + " WHERE v" + op + "$1::" + tc.typ + " ORDER BY id"
							classRoutes(t, p, b, conn, ginIDs(q, temporalTruth[i], tc.boundary))
							classRoutes(t, p, b, conn, ginIDs(q, nil, nil))
							classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE $1::"+tc.typ+reverse[i]+"v ORDER BY id", temporalTruth[i], tc.boundary))
							classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v"+op+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", temporalTruth[i]))
							classRoutes(t, p, b, conn, fixture.Select("SELECT v"+op+"NULL::"+tc.typ+" AS unknown FROM "+table+" WHERE id=3", []string{"unknown"}, []string{"BOOL"}, [][]any{{nil}}))
						}
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v IS NULL ORDER BY id", []int64{7}))
						temporalValueRoutes(t, p, b, conn, temporalValueStep(b, "SELECT id,v FROM "+table+" ORDER BY id", tc, temporalRows(b, tc)))
					}
					for i := range ops {
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], temporalTruth[i], tc.boundary))
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil))
					}
					for _, edge := range []struct {
						value string
						truth [][]int64
					}{
						{tc.lower, [][]int64{nil, {1}, {1}, {1, 2, 3, 4, 5, 6}, {2, 3, 4, 5, 6}}},
						{tc.upper, [][]int64{{1, 2, 3, 4, 5}, {1, 2, 3, 4, 5, 6}, {6}, {6}, nil}},
					} {
						for i, ids := range edge.truth {
							s := ginIDs(queries[i], ids, edge.value)
							classRoutes(t, p, b, conn, s)
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						}
					}
					for _, value := range []struct {
						arg  any
						rows [][]any
					}{{1, temporalRows(b, tc)}, {100, nil}, {nil, nil}, {7, temporalRows(b, tc)[6:]}} {
						s := temporalValueStep(b, valueSQL, tc, value.rows, value.arg)
						temporalValueRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, valueHeld, b.Recorder, s)
					}
					classRoutes(t, p, b, conn, ginIDs(queries[2], nil, tc.absent))
				}
				check([]string{"temporal_search", "temporal_scan"})
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE INDEX temporal_search_i ON temporal_search USING gin(v public."+tc.typ+"_ops)", 0))
				run(fixture.Exec("CREATE INDEX temporal_delta_i ON temporal_delta USING gin(v public."+tc.typ+"_ops)", 0))
				temporalSeed(run, "temporal_delta", tc)
				check([]string{"temporal_search", "temporal_scan", "temporal_delta"})
				insert := "INSERT INTO temporal_search VALUES(90,$1::" + tc.typ + ")"
				ins := classHeld(t, p, conn, insert)
				fixture.RunStep(t, p.Ctx, ins, b.Recorder, fixture.Exec(insert, 1, tc.boundary))
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], []int64{3, 4, 90}, tc.boundary))
				run(fixture.Prepared(fixture.Exec("UPDATE temporal_search SET v=$1::"+tc.typ+" WHERE id=90", 1, tc.upper)))
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], []int64{3, 4}, tc.boundary))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM temporal_search WHERE id=90 AND v=$1::"+tc.typ+" ORDER BY id", []int64{90}, tc.upper))
				run(fixture.Exec("DELETE FROM temporal_search WHERE id=90", 1))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT temporal_before", 0))
				fixture.RunStep(t, p.Ctx, ins, b.Recorder, fixture.Exec(insert, 1, tc.boundary))
				run(fixture.Exec("ROLLBACK TO temporal_before", 0))
				check([]string{"temporal_search", "temporal_scan", "temporal_delta"})
				run(fixture.Exec("COMMIT", 0))
				fixture.RunStep(t, p.Ctx, ins, b.Recorder, fixture.Exec(insert, 1, tc.boundary))
				run(fixture.Exec("DELETE FROM temporal_search WHERE id=90", 1))
				if tc.typ == "timestamptz" {
					run(fixture.Exec("SET TimeZone='America/New_York'", 0))
					for i := range ops {
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], temporalTruth[i], tc.boundary))
					}
					run(fixture.Exec("SET TimeZone='UTC'", 0))
				}
			})
		})
	}
	t.Run("finite_and_equivalence", temporalBoundarySearch)
}
