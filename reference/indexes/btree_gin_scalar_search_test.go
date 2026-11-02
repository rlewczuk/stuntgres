package reference_test

import (
	"math"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINScalarSearch(t *testing.T) {
	for _, tc := range scalarCases {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE TABLE scalar_search(id integer NOT NULL,v "+tc.typ+")", 0))
				scalarSeed(run, "scalar_search", tc)
				ops := []string{"<", "<=", "=", ">=", ">", "<>"}
				held := make([]fixture.HeldStatement, len(ops))
				queries := make([]string, len(ops))
				for i, op := range ops {
					queries[i] = "SELECT id FROM scalar_search WHERE v" + op + "$1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				valueSQL := "SELECT id,v FROM scalar_search WHERE id >= $1 ORDER BY id"
				valueHeld := classHeld(t, p, conn, valueSQL)
				check := func() {
					t.Helper()
					for i, op := range ops {
						s := ginIDs(queries[i], scalarTruth(tc)[i], tc.boundary)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						classRoutes(t, p, b, conn, ginIDs(queries[i], nil, nil))
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], nil, nil))
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_search WHERE v"+op+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", scalarTruth(tc)[i]))
					}
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_search WHERE v IS NULL ORDER BY id", []int64{int64(len(scalarTexts(tc)))}))
					scalarValueRoutes(t, p, b, conn, scalarValueStep(b, "SELECT id,v FROM scalar_search ORDER BY id", tc, scalarRows(b, tc)))
					scalarValueRoutes(t, p, b, conn, scalarValueStep(b, "SELECT id,v FROM scalar_search WHERE false", tc, nil))
					valueStep := scalarValueStep(b, valueSQL, tc, scalarRows(b, tc), 1)
					scalarValueRoutes(t, p, b, conn, valueStep)
					scalarValueRun(t, p, b, valueHeld, valueStep)
					for _, arg := range []any{100, nil} {
						empty := scalarValueStep(b, valueSQL, tc, nil, arg)
						scalarValueRoutes(t, p, b, conn, empty)
						scalarValueRun(t, p, b, valueHeld, empty)
					}
					nullRow := scalarRows(b, tc)[len(scalarTexts(tc))-1:]
					scalarValueRun(t, p, b, valueHeld, scalarValueStep(b, valueSQL, tc, nullRow, len(scalarTexts(tc))))
					if tc.absent != "" {
						classRoutes(t, p, b, conn, ginIDs(queries[2], nil, tc.absent))
					}
					var edges []struct {
						value string
						truth [][]int64
					}
					switch {
					case tc.typ == "bool":
						edges = append(edges, struct {
							value string
							truth [][]int64
						}{"true", [][]int64{{1, 2}, {1, 2, 3}, {3}, {3}, nil}})
					case strings.HasPrefix(tc.typ, "float"):
						edges = append(edges, struct {
							value string
							truth [][]int64
						}{"NaN", [][]int64{{1, 2, 3, 4, 5, 6, 7}, {1, 2, 3, 4, 5, 6, 7, 8, 9}, {8, 9}, {8, 9}, nil}})
						classRoutes(t, p, b, conn, ginIDs(queries[4], []int64{8, 9}, "Infinity"))
						classRoutes(t, p, b, conn, ginIDs(queries[1], []int64{1}, "-Infinity"))
						classRoutes(t, p, b, conn, ginIDs(queries[5], []int64{1, 2, 3, 4, 5, 6, 7}, "NaN"))
					default:
						edges = append(edges, struct {
							value string
							truth [][]int64
						}{tc.lower, [][]int64{nil, {1}, {1}, {1, 2, 3, 4, 5, 6}, {2, 3, 4, 5, 6}}}, struct {
							value string
							truth [][]int64
						}{tc.upper, [][]int64{{1, 2, 3, 4, 5}, {1, 2, 3, 4, 5, 6}, {6}, {6}, nil}})
					}
					for _, edge := range edges {
						for i, ids := range edge.truth {
							s := ginIDs(queries[i], ids, edge.value)
							classRoutes(t, p, b, conn, s)
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						}
					}
				}
				check()
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE INDEX scalar_search_i ON scalar_search USING gin(v public."+tc.typ+"_ops)", 0))
				check()
				insert := "INSERT INTO scalar_search VALUES(90,$1::" + tc.typ + ")"
				stmt := classHeld(t, p, conn, insert)
				fixture.RunStep(t, p.Ctx, stmt, b.Recorder, fixture.Exec(insert, 1, tc.boundary))
				equal := append(append([]int64{}, scalarTruth(tc)[2]...), 90)
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], equal, tc.boundary))
				run(fixture.Prepared(fixture.Exec("UPDATE scalar_search SET v=$1::"+tc.typ+" WHERE id=90", 1, tc.upper)))
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], scalarTruth(tc)[2], tc.boundary))
				upperSQL := "SELECT id FROM scalar_search WHERE id=90 AND v=$1::" + tc.typ + " ORDER BY id"
				classRoutes(t, p, b, conn, ginIDs(upperSQL, []int64{90}, tc.upper))
				run(fixture.Prepared(fixture.Exec("DELETE FROM scalar_search WHERE id=90", 1)))
				check()
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT scalar_before", 0))
				fixture.RunStep(t, p.Ctx, stmt, b.Recorder, fixture.Exec(insert, 1, tc.boundary))
				run(fixture.Exec("ROLLBACK TO scalar_before", 0))
				check()
				run(fixture.Exec("COMMIT", 0))
			})
		})
	}
	t.Run("representable_boundaries", scalarBoundarySearch)
}

// Decimal inputs and exact adjacent values match the existing float fixtures.
func scalarBoundarySearch(t *testing.T) {
	for _, tc := range []struct {
		typ, rows string
		probes    []struct {
			value string
			ids   []int64
		}
	}{
		{"int8", "(1,9007199254740992),(2,9007199254740993),(3,9007199254740994)", []struct {
			value string
			ids   []int64
		}{{"9007199254740992", []int64{1}}, {"9007199254740993", []int64{2}}, {"9007199254740994", []int64{3}}}},
		{"float4", "(1,'-1e-45'),(2,'1.17549435e-38'),(3,'3.4028235e38'),(4,'16777217'),(5,'16777218'),(6,'0.1'),(7,'7.038531e-26')", []struct {
			value string
			ids   []int64
		}{{"16777217", []int64{4}}, {"16777218", []int64{5}}, {"1.17549435e-38", []int64{2}}, {"3.4028235e38", []int64{3}}, {"-1e-45", []int64{1}}}},
		{"float8", "(1,'-5e-324'),(2,'2.2250738585072014e-308'),(3,'1.7976931348623157e308'),(4,'1'),(5,'1.0000000000000002')", []struct {
			value string
			ids   []int64
		}{{"1", []int64{4}}, {"1.0000000000000002", []int64{5}}, {"2.2250738585072014e-308", []int64{2}}, {"1.7976931348623157e308", []int64{3}}, {"-5e-324", []int64{1}}}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
				run(fixture.Exec("CREATE TABLE scalar_edges(id integer NOT NULL,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO scalar_edges VALUES"+tc.rows, int64(strings.Count(tc.rows, "("))))
				q := "SELECT id FROM scalar_edges WHERE v=$1::" + tc.typ + " ORDER BY id"
				held := classHeld(t, p, conn, q)
				check := func() {
					for _, probe := range tc.probes {
						s := ginIDs(q, probe.ids, probe.value)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
				check()
				run(fixture.Exec("CREATE INDEX scalar_edges_i ON scalar_edges USING gin(v public."+tc.typ+"_ops)", 0))
				check()
				if tc.typ == "float4" {
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM scalar_edges WHERE v=16777217 ORDER BY id", nil))
					want := .1
					narrow := 7.038531e-26
					if !b.PG {
						want = float64(float32(.1))
						narrow = float64(math.Float32frombits(0x15ae43fd))
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT v FROM scalar_edges WHERE id IN(6,7) ORDER BY id", []string{"v"}, []string{"FLOAT4"}, [][]any{{want}, {narrow}}))
				}
			})
		})
	}
}
