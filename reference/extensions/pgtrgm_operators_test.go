package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmOperators(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		for _, op := range pgTrgmOperators {
			t.Run(op.token, func(t *testing.T) {
				run := pgTrgmRun(t, p, b, c)
				q := "SELECT $1::text " + op.token + " $2::text AS result"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, threshold := range []string{"0", "1"} {
					for _, setting := range pgTrgmSettings {
						run(fixture.Exec("SET "+setting.name+"="+threshold, 0))
					}
					for _, tc := range []struct {
						left, right any
						score       [2]int
					}{
						{"cat", "cat", [2]int{1, 1}},
						{"", "", [2]int{0, 1}},
						{"!!!", "cat", [2]int{0, 1}},
						{nil, "cat", [2]int{0, 1}},
						{"cat", nil, [2]int{0, 1}},
						{nil, nil, [2]int{0, 1}},
					} {
						var want any
						if tc.left != nil && tc.right != nil {
							if op.result == "BOOL" {
								want = threshold == "0" || tc.score[0] == tc.score[1]
							} else {
								want = pgTrgmFloat(b.PG, 1-float32(tc.score[0])/float32(tc.score[1]))
							}
						}
						s := pgTrgmScalar(b, q, "result", op.result, want, tc.left, tc.right)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						run(pgTrgmScalar(b, "SELECT "+op.routine+"($1,$2)", op.routine, op.result, want, tc.left, tc.right))
					}
				}
				if op.result == "BOOL" {
					// Scores are exactly representable; widening score to the real
					// double-valued threshold distinguishes equality and just-above.
					setting := pgTrgmSettings[op.kernel].name
					equal, below, above := "0.25", "0.249999999", "0.250000001"
					if op.kernel == 1 {
						equal, below, above = "0.5", "0.499999999", "0.500000001"
					}
					left, right := "a", "aa"
					if op.reverse {
						left, right = right, left
					}
					for _, tc := range []struct {
						threshold string
						want      bool
					}{{equal, true}, {below, true}, {above, false}} {
						run(fixture.Exec("SET "+setting+"="+pgTrgmLiteral(tc.threshold), 0))
						s := pgTrgmScalar(b, q, "result", "BOOL", tc.want, left, right)
						run(s)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						run(pgTrgmScalar(b, "SELECT "+op.routine+"($1,$2)", op.routine, "BOOL", tc.want, left, right))
					}
					// Reverse direction is not the same arbitrary/strict extent.
					run(fixture.Exec("SET "+setting+"='0.5'", 0))
					left, right = "word", "two words"
					if op.reverse {
						left, right = right, left
					}
					want := op.kernel != 0
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "result", "BOOL", want, left, right))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "result", "BOOL", false, right, left))
					if op.kernel != 0 {
						t.Run("PositionalExtents", func(t *testing.T) {
							backingSQL := "SELECT " + op.routine + "($1::text,$2::text) AS result"
							backingStmt, err := c.PrepareContext(p.Ctx, backingSQL)
							if err != nil {
								t.Fatal(err)
							}
							defer backingStmt.Close()
							backing := fixture.HeldStatement{Executor: c, Statement: backingStmt, SQL: backingSQL}
							scoreRoutine, distanceRoutine := "word_similarity", "word_similarity_dist_op"
							if op.kernel == 2 {
								scoreRoutine, distanceRoutine = "strict_word_similarity", "strict_word_similarity_dist_op"
							}
							for _, tc := range []struct {
								name, left, right string
								word, strict      [2]int
							}{
								{"RepeatedMatching", "a", strings.Repeat("a ", 64), [2]int{1, 1}, [2]int{1, 1}},
								{"RepeatedPartial", "a", strings.Repeat("aa ", 64), [2]int{1, 2}, [2]int{1, 4}},
								{"RepeatedNonBinary", "aa", strings.Repeat("a ", 64), [2]int{1, 3}, [2]int{1, 4}},
								{"LaterBetter", "a", strings.Repeat("aa ", 32) + "a", [2]int{1, 1}, [2]int{1, 1}},
								{"LaterLowerBound", "a", strings.Repeat("x ", 32) + "a", [2]int{1, 1}, [2]int{1, 1}},
								{"StrictEndBoundary", "word", strings.Repeat("words ", 32), [2]int{4, 5}, [2]int{4, 7}},
								{"StrictStartBoundary", "ord", strings.Repeat("word ", 32), [2]int{1, 2}, [2]int{2, 7}},
								{"StrictMultiword", "two words", strings.Repeat("x ", 16) + "two words " + strings.Repeat("y ", 16), [2]int{1, 1}, [2]int{1, 1}},
								{"NoMatchingExtent", "a", strings.Repeat("b ", 64), [2]int{0, 1}, [2]int{0, 1}},
								{"EmptyPattern", "", strings.Repeat("a ", 64), [2]int{0, 1}, [2]int{0, 1}},
							} {
								t.Run(tc.name, func(t *testing.T) {
									run := pgTrgmRun(t, p, b, c)
									// Independent distinct-set counts: a has two keys;
									// aa has three, sharing one. word has five;
									// words has six, sharing four. ord has four,
									// sharing two with word. Repetition adds positions,
									// never weight; strict extents retain both word edges.
									ratio := tc.word
									if op.kernel == 2 {
										ratio = tc.strict
									}
									score := float32(ratio[0]) / float32(ratio[1])
									// A low predicate threshold must not truncate the
									// full score/distance at an earlier qualifying extent.
									run(fixture.Exec("SET "+setting+"='0.25'", 0))
									fullSQL := "SELECT " + scoreRoutine + "($1,$2) AS score, " + distanceRoutine + "($1,$2) AS distance"
									full := pgTrgmSelect(b, fullSQL, []string{"score", "distance"}, []string{"FLOAT4", "FLOAT4"},
										[][]any{{pgTrgmFloat(b.PG, score), pgTrgmFloat(b.PG, 1-score)}}, tc.left, tc.right)
									run(full)
									run(fixture.Prepared(full))
									thresholds := []struct {
										name, value string
										want        bool
									}{
										{"Zero", "0", true},
										{"Low", "0.25", score >= 0.25},
										{"One", "1", score == 1},
									}
									if score > 0 && score < 1 {
										// Compare against the widened independently
										// rounded float4, not a narrowed threshold or
										// the unrounded rational. Adjacent float64s
										// deliberately round back to the same float4.
										for _, edge := range []struct {
											name  string
											value float64
											want  bool
										}{
											{"Below", math.Nextafter(float64(score), 0), true},
											{"Equal", float64(score), true},
											{"Above", math.Nextafter(float64(score), 1), false},
										} {
											thresholds = append(thresholds, struct {
												name, value string
												want        bool
											}{edge.name, strconv.FormatFloat(edge.value, 'g', -1, 64), edge.want})
										}
									}
									left, right := tc.left, tc.right
									if op.reverse {
										left, right = right, left
									}
									for _, threshold := range thresholds {
										t.Run(threshold.name, func(t *testing.T) {
											run := pgTrgmRun(t, p, b, c)
											run(fixture.Exec("SET "+setting+"="+pgTrgmLiteral(threshold.value), 0))
											s := pgTrgmScalar(b, q, "result", "BOOL", threshold.want, left, right)
											run(s)
											fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
											s = pgTrgmScalar(b, backingSQL, "result", "BOOL", threshold.want, left, right)
											run(s)
											fixture.RunStep(t, p.Ctx, backing, b.Recorder, s)
										})
									}
								})
							}
						})
					}
				} else {
					for _, tc := range []struct {
						left, right      string
						forward, reverse [2]int
					}{
						{"a", "aa", [2]int{1, 4}, [2]int{1, 4}},
						{"word", "two words", [2]int{4, 11}, [2]int{4, 11}},
					} {
						f, r := tc.forward, tc.reverse
						if op.kernel == 1 {
							if tc.left == "a" {
								f, r = [2]int{1, 2}, [2]int{1, 3}
							} else {
								f, r = [2]int{4, 5}, [2]int{2, 5}
							}
						}
						if op.kernel == 2 && tc.left == "word" {
							f = [2]int{4, 7}
						}
						if op.reverse {
							f, r = r, f
						}
						for _, direction := range []struct {
							left, right string
							ratio       [2]int
						}{{tc.left, tc.right, f}, {tc.right, tc.left, r}} {
							want := pgTrgmFloat(b.PG, 1-float32(direction.ratio[0])/float32(direction.ratio[1]))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "result", "FLOAT4", want, direction.left, direction.right))
							run(pgTrgmScalar(b, "SELECT "+op.routine+"($1,$2)", op.routine, "FLOAT4", want, direction.left, direction.right))
						}
					}
				}
			})
		}
		for _, setting := range pgTrgmSettings {
			run(fixture.Exec("RESET "+setting.name, 0))
		}
		run(fixture.Exec("CREATE TABLE trgm_predicate(id int,v text)", 0))
		run(fixture.Exec("INSERT INTO trgm_predicate VALUES(1,'a'),(2,'aa'),(3,NULL)", 3))
		q := "SELECT id FROM trgm_predicate WHERE v % 'aa' ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("SET pg_trgm.similarity_threshold='0.25'", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		pgTrgmNotices(t, notices)
	})
}
