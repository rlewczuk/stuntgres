package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectDrop(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, target := range []string{"aggregate", "transition", "final", "caller"} {
			t.Run(fmt.Sprintf("minimal/%s/prepared=%v", target, prepared), func(t *testing.T) {
				aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					aggregateDirectSetup(t, p, b, c)
					aggregateDirectExec(t, p, b, c, "CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END")
					ids := aggregateDirectIDs(t, p, c)
					held := aggregateDirectHeld(t, p, c, "SELECT ad_c() AS v")
					check := func() {
						aggregateDirectSameIDs(t, p, c, ids)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, int64(30)))
						createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
						createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_c"}, [2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"}, [2]string{"ad_c", "ad_a"}))
					}
					root, kind := "ad_a(integer)", "AGGREGATE"
					detail := "function ad_c() depends on function ad_a(integer)"
					notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to function ad_c()"}
					if target == "transition" || target == "final" {
						root = "ad_s(integer,integer)"
						if target == "final" {
							root = "ad_f(integer)"
						}
						kind = "FUNCTION"
						detail = "function ad_a(integer) depends on function " + root + "\n" + detail
						notice.Message = "drop cascades to 2 other objects"
						notice.Detail = "drop cascades to function ad_a(integer)\ndrop cascades to function ad_c()"
					}
					if target == "caller" {
						root = "ad_c()"
						kind = "FUNCTION"
					} else {
						for _, suffix := range []string{"", " RESTRICT"} {
							s := aggregateDirectRestrict("DROP "+kind+" "+root+suffix, root, detail)
							s.Prepared = prepared
							createAggregateRun(t, p, b, c, s)
							check()
						}
					}
					notices = nil
					s := fixture.Exec("DROP "+kind+" "+root+" CASCADE", 0)
					s.Prepared = prepared
					createAggregateRun(t, p, b, c, s)
					if target == "caller" {
						if len(notices) != 0 {
							t.Fatalf("caller notices=%#v", notices)
						}
					} else if len(notices) != 1 || notices[0] != notice {
						t.Fatalf("cascade notices=%#v want=%#v", notices, notice)
					}
					remaining := map[string]int64{}
					for name, id := range ids {
						if name == "public.ad_c" {
							continue
						}
						if target != "caller" && name == "public.ad_a" {
							continue
						}
						if target == "transition" && name == "public.ad_s" {
							continue
						}
						if target == "final" && name == "public.ad_f" {
							continue
						}
						remaining[name] = id
					}
					aggregateDirectSameIDs(t, p, c, remaining)
					createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
					createAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM ad_input ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
					if target == "aggregate" || target == "caller" {
						createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ad_f(ad_s(1,2)) AS v", int64(30)))
					}
				})
			})
		}
		for _, shape := range []string{"explicit", "mixed", "mixed_transition", "mixed_final", "shared", "duplicate"} {
			t.Run(fmt.Sprintf("graph/%s/prepared=%v", shape, prepared), func(t *testing.T) {
				aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					aggregateDirectSetup(t, p, b, c)
					root, kind := "ad_a(integer)", "AGGREGATE"
					var edges [][2]string
					edges = append(edges, [2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"})
					var detail, noticeDetail string
					noticeMessage := "drop cascades to function ad_m()"
					switch shape {
					case "explicit":
						aggregateDirectExec(t, p, b, c, "DROP AGGREGATE ad_a(integer)", "CREATE FUNCTION ad_e() RETURNS integer RETURN ad_f(ad_s(1,2))")
						root = "ad_s(integer,integer)"
						kind = "FUNCTION"
						edges = [][2]string{{"ad_e", "ad_s"}, {"ad_e", "ad_f"}}
						detail = "function ad_e() depends on function ad_s(integer,integer)"
						noticeMessage = "drop cascades to function ad_e()"
					case "mixed", "mixed_transition", "mixed_final", "duplicate":
						aggregateDirectExec(t, p, b, c, "CREATE FUNCTION ad_m() RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_f(ad_s(1,2)) FROM ad_input; END")
						edges = append(edges, [2]string{"ad_m", "ad_a"}, [2]string{"ad_m", "ad_s"}, [2]string{"ad_m", "ad_f"})
						detail = "function ad_m() depends on function ad_a(integer)"
						if shape == "mixed_transition" || shape == "mixed_final" {
							root = "ad_s(integer,integer)"
							if shape == "mixed_final" {
								root = "ad_f(integer)"
							}
							kind = "FUNCTION"
							detail = "function ad_a(integer) depends on function " + root + "\nfunction ad_m() depends on function " + root
							noticeMessage = "drop cascades to 2 other objects"
							noticeDetail = "drop cascades to function ad_a(integer)\ndrop cascades to function ad_m()"
						}
					case "shared":
						aggregateDirectExec(t, p, b, c, "CREATE AGGREGATE ad_b(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)", "CREATE FUNCTION ad_two() RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_b(v) FROM ad_input; END")
						root = "ad_s(integer,integer)"
						kind = "FUNCTION"
						edges = append(edges, [2]string{"ad_b", "ad_s"}, [2]string{"ad_b", "ad_f"}, [2]string{"ad_two", "ad_a"}, [2]string{"ad_two", "ad_b"})
						detail = "function ad_a(integer) depends on function ad_s(integer,integer)\nfunction ad_b(integer) depends on function ad_s(integer,integer)\nfunction ad_two() depends on function ad_b(integer)"
						noticeMessage = "drop cascades to 3 other objects"
						noticeDetail = "drop cascades to function ad_a(integer)\ndrop cascades to function ad_b(integer)\ndrop cascades to function ad_two()"
					}
					ids := aggregateDirectIDs(t, p, c)
					name := "ad_m"
					want := int64(60)
					if shape == "explicit" {
						name = "ad_e"
						want = 30
					}
					if shape == "shared" {
						name = "ad_two"
					}
					held := aggregateDirectHeld(t, p, c, "SELECT "+name+"() AS v")
					for _, suffix := range []string{"", " RESTRICT"} {
						s := aggregateDirectRestrict("DROP "+kind+" "+root+suffix, root, detail)
						s.Prepared = prepared
						createAggregateRun(t, p, b, c, s)
						aggregateDirectSameIDs(t, p, c, ids)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, want))
						createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_b", "ad_e", "ad_m", "ad_two"}, edges...))
						createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
					}
					if shape == "duplicate" {
						root += ",ad_a(integer)"
					}
					notices = nil
					s := fixture.Exec("DROP "+kind+" "+root+" CASCADE", 0)
					s.Prepared = prepared
					createAggregateRun(t, p, b, c, s)
					wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: noticeMessage, Detail: noticeDetail}
					if len(notices) != 1 || notices[0] != wantNotice {
						t.Fatalf("cascade notices=%#v want=%#v", notices, wantNotice)
					}
					remaining := map[string]int64{"public.ad_f": ids["public.ad_f"]}
					if shape == "mixed" || shape == "duplicate" {
						remaining["public.ad_s"] = ids["public.ad_s"]
					}
					if shape == "mixed_final" {
						remaining = map[string]int64{"public.ad_s": ids["public.ad_s"]}
					}
					aggregateDirectSameIDs(t, p, c, remaining)
					if shape == "mixed_final" {
						createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ad_s(1,2) AS v", int64(3)))
					} else {
						createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ad_f(3) AS v", int64(30)))
					}
					createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
				})
			})
		}
		t.Run(fmt.Sprintf("atomic/prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ad_actor", "ad_other"})
			aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				aggregateDirectSetup(t, p, b, c)
				aggregateDirectExec(t, p, b, c,
					"CREATE AGGREGATE ad_free(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)",
					"CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END")
				ids := aggregateDirectIDs(t, p, c)
				held := aggregateDirectHeld(t, p, c, "SELECT ad_c() AS v")
				check := func() {
					aggregateDirectSameIDs(t, p, c, ids)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, createAggregateValue(held.SQL, int64(30)))
					createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_free", "ad_c"}, [2]string{"ad_a", "ad_s"}, [2]string{"ad_a", "ad_f"}, [2]string{"ad_free", "ad_s"}, [2]string{"ad_free", "ad_f"}, [2]string{"ad_c", "ad_a"}))
					createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ad_sentinel", int64(73)))
				}
				for _, tc := range []struct{ q, code, message, detail, hint string }{
					{"DROP AGGREGATE ad_free(integer),ad_missing(integer)", "42883", "aggregate ad_missing(integer) does not exist", "", ""},
					{"DROP AGGREGATE IF EXISTS ad_free(integer),ad_f(integer)", "42809", "function ad_f(integer) is not an aggregate", "", ""},
					{"DROP AGGREGATE ad_free(integer),ad_a(integer)", "2BP01", "cannot drop desired object(s) because other objects depend on them", "function ad_c() depends on function ad_a(integer)", "Use DROP ... CASCADE to drop the dependent objects too."},
				} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					createAggregateRun(t, p, b, c, s)
					check()
				}
				aggregateDirectExec(t, p, b, c, "CREATE ROLE ad_actor", "CREATE ROLE ad_other", "GRANT USAGE,CREATE ON SCHEMA public TO ad_actor,ad_other", "ALTER AGGREGATE ad_free(integer) OWNER TO ad_actor", "ALTER AGGREGATE ad_a(integer) OWNER TO ad_other", "SET SESSION AUTHORIZATION ad_actor")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				s := fixture.ImplicitError("DROP AGGREGATE ad_free(integer),ad_a(integer)", fixture.Diagnostic{Code: "42501", Message: "must be owner of aggregate ad_a"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				createAggregateRun(t, p, b, c, s)
				aggregateDirectExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				check()
			})
		})
	}
}
