package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextSearch(t *testing.T) {
	for _, tc := range textCases {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE TABLE text_search(id integer NOT NULL CHECK(id>0),v "+textSQLType(tc)+")", 0))
				textSeed(run, "text_search", tc)
				ops := []string{"<", "<=", "=", ">=", ">", "<>"}
				held := make([]fixture.HeldStatement, len(ops))
				queries := make([]string, len(ops))
				for i, op := range ops {
					queries[i] = "SELECT id FROM text_search WHERE v" + op + "$1::" + textSQLType(tc) + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				check := func() {
					for i := range ops {
						for _, probe := range []struct {
							arg any
							ids []int64
						}{{textArgument(tc, tc.boundary), textTruth(tc)[i]}, {nil, nil}} {
							s := ginIDs(queries[i], probe.ids, probe.arg)
							classRoutes(t, p, b, conn, s)
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
						}
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_search WHERE v"+ops[i]+networkLiteral(tc.boundary, textSQLType(tc))+" ORDER BY id", textTruth(tc)[i]))
					}
					all := make([]int64, len(textTexts(tc)))
					for i := range all {
						all[i] = int64(i + 1)
					}
					empty := [][]int64{nil, {1}, {1}, all, all[1:]}
					for i := range 5 {
						s := ginIDs(queries[i], empty[i], textArgument(tc, tc.lower))
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
					}
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_search WHERE v IS NULL ORDER BY id", []int64{int64(len(all) + 1)}))
					classRoutes(t, p, b, conn, fixture.Select("SELECT v=$1::"+textSQLType(tc)+" AS eq FROM text_search WHERE id=1", []string{"eq"}, []string{"BOOL"}, [][]any{{nil}}, nil))
					classRoutes(t, p, b, conn, textValueStep(b, "SELECT id,v FROM text_search ORDER BY id", tc, textRows(tc)))
				}
				check()
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE INDEX text_search_i ON text_search USING gin(v public."+tc.typ+"_ops)", 0))
				check()
				if !b.PG {
					s := textValueStep(b, "SELECT id,v FROM text_search ORDER BY id", tc, textRows(tc))
					rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					s.CheckMetadata(t, rows)
					defer fixture.RangeConsumeHeld(t, rows, s)
				}
				insert := "INSERT INTO text_search VALUES(90,$1::" + textSQLType(tc) + ")"
				stmt := classHeld(t, p, conn, insert)
				fixture.RunStep(t, p.Ctx, stmt, b.Recorder, fixture.Exec(insert, 1, textArgument(tc, tc.boundary)))
				equal := append(append([]int64{}, textTruth(tc)[2]...), 90)
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], equal, textArgument(tc, tc.boundary)))
				run(fixture.Prepared(fixture.Exec("UPDATE text_search SET v=$1::"+textSQLType(tc)+" WHERE id=90", 1, textArgument(tc, tc.upper))))
				fixture.RunStep(t, p.Ctx, held[2], b.Recorder, ginIDs(queries[2], textTruth(tc)[2], textArgument(tc, tc.boundary)))
				run(fixture.Prepared(fixture.Exec("DELETE FROM text_search WHERE id=90", 1)))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT text_before", 0))
				fixture.RunStep(t, p.Ctx, stmt, b.Recorder, fixture.Exec(insert, 1, textArgument(tc, tc.boundary)))
				run(fixture.Exec("ROLLBACK TO text_before", 0))
				run(fixture.Exec("COMMIT", 0))
				d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "text_search" violates check constraint "text_search_id_check"`)
				d.Schema = "public"
				d.Table = "text_search"
				d.Constraint = "text_search_id_check"
				// A stable NULL row makes the complete failing-row detail independent
				// of binary rendering and character padding settings.
				d.Detail = "Failing row contains (-1, null)."
				run(fixture.ErrorDiagnostic("INSERT INTO text_search VALUES(91,NULL),(-1,NULL)", d))
				check()
			})
		})
	}
	t.Run("character_edges", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			for _, tc := range []networkCase{textCases[0], textCases[1], textCases[2], textCases[4]} {
				table := "text_edges_" + tc.typ
				run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES(1,'ab'),(2,'ab '),(3,E'ab\\t'),(4,E'ab\\n'),(5,'é')", 5))
				run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v)", 0))
				eq := []int64{1}
				gt := []int64{2, 3, 4, 5}
				if tc.typ == "bpchar" {
					eq = []int64{1, 2}
					gt = []int64{3, 4, 5}
				}
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v='ab'::"+tc.typ+" ORDER BY id", eq))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v>'ab'::"+tc.typ+" ORDER BY id", gt))
			}
			run(fixture.Exec("CREATE TABLE text_name_clip(id integer NOT NULL,v name)", 0))
			long := strings.Repeat("x", 63)
			run(fixture.Exec("INSERT INTO text_name_clip VALUES(1,$1::name),(2,$2::name),(3,$3::name)", 3, long+"tail", long+"other", strings.Repeat("x", 62)+"é"))
			run(fixture.Exec("CREATE INDEX text_name_clip_i ON text_name_clip USING gin(v)", 0))
			classRoutes(t, p, b, conn, fixture.Select("SELECT id,v FROM text_name_clip ORDER BY id", []string{"id", "v"}, []string{"INT4", "NAME"}, [][]any{{int64(1), long}, {int64(2), long}, {int64(3), strings.Repeat("x", 62)}}))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM text_name_clip WHERE v=$1::name ORDER BY id", []int64{1, 2}, long))
		})
	})
}
