package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func queryLockPosition(q, token string) string {
	i := strings.LastIndex(q, token)
	return fixture.Position(len([]rune(q[:i])) + 1)
}
func TestReferenceQueryLockDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, token, stage string }{
		{"SELECT id FROM t FOR UPDATE OF missing", "42P01", `relation "missing" in FOR UPDATE clause not found in FROM clause`, "missing", "prepare"},
		{"SELECT id FROM t a FOR SHARE OF t", "42P01", `relation "t" in FOR SHARE clause not found in FROM clause`, "t", "prepare"},
		{`SELECT id FROM t "é" FOR KEY SHARE OF "不存在"`, "42P01", `relation "不存在" in FOR KEY SHARE clause not found in FROM clause`, `"不存在"`, "prepare"},
		{"SELECT id FROM t FOR UPDATE OF public.t", "42601", "FOR UPDATE must specify unqualified relation names", "public.t", "prepare"},
		{"SELECT j.id FROM (t JOIN u USING(id)) j FOR UPDATE OF j", "0A000", "FOR UPDATE cannot be applied to a join", "j", "prepare"},
		{"SELECT a.id FROM t a LEFT JOIN u b USING(id) LIMIT 0 FOR SHARE OF b", "0A000", "FOR SHARE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT a.id FROM t a RIGHT JOIN u b USING(id) WHERE FALSE FOR UPDATE OF a", "0A000", "FOR UPDATE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT a.id FROM t a FULL JOIN u b USING(id) FOR KEY SHARE OF a", "0A000", "FOR KEY SHARE cannot be applied to the nullable side of an outer join", "", "execute"},
		// Independent source-backed hypotheses: planner uses normalized strength and
		// first occurrence insertion order, including the order within an OF list.
		{"SELECT a.id FROM t a LEFT JOIN u b USING(id) FOR KEY SHARE OF b FOR UPDATE OF b", "0A000", "FOR UPDATE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT a.id FROM t a FULL JOIN u b USING(id) FOR SHARE OF a FOR UPDATE OF b", "0A000", "FOR SHARE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT a.id FROM t a FULL JOIN u b USING(id) FOR UPDATE OF b FOR SHARE OF a", "0A000", "FOR UPDATE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT a.id FROM t a FULL JOIN u b USING(id) FOR KEY SHARE OF b,a FOR UPDATE OF b", "0A000", "FOR UPDATE cannot be applied to the nullable side of an outer join", "", "execute"},
		{"SELECT id FROM t FOR UPDATE LIMIT 1 FOR SHARE", "42601", `syntax error at or near "FOR"`, "FOR", "prepare"},
		{"SELECT id FROM t FOR UPDATE NOWAIT SKIP LOCKED", "42601", `syntax error at or near "SKIP"`, "SKIP", "prepare"},
		{"SELECT id FROM t FOR UPDATE NOWAIT OF t", "42601", `syntax error at or near "OF"`, "OF", "prepare"},
		{"SELECT id FROM t FOR SHARE ORDER BY id", "42601", `syntax error at or near "ORDER"`, "ORDER", "prepare"},
		{"SELECT id FROM t FOR UPDATE SKIP", "42601", "syntax error at end of input", "<end>", "prepare"},
		{"SELECT id FROM t FOR UPDATE OF", "42601", "syntax error at end of input", "<end>", "prepare"},
		{"SELECT id FROM t FOR UPDATE OF t, NOWAIT", "42P01", `relation "nowait" in FOR UPDATE clause not found in FROM clause`, "NOWAIT", "prepare"},
		{"SELECT id FROM t FOR UPDATE garbage", "42601", `syntax error at or near "garbage"`, "garbage", "prepare"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", tc.q, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
					if tc.token == "<end>" {
						want.Position = fixture.Position(len([]rune(tc.q)) + 1)
					} else if tc.token != "" {
						want.Position = queryLockPosition(tc.q, tc.token)
					}
					stage := "execute"
					if prepared {
						stage = tc.stage
					}
					fixture.QueryLockFailure(t, p, b, a, tc.q, prepared, want, stage)
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", false, 4)
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE u SET v=v", true, 4)
				})
			})
		}
	}
}

func TestReferenceQueryLockRejectedShapes(t *testing.T) {
	for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
		for _, tc := range []struct{ prefix, reason string }{
			{"SELECT DISTINCT id FROM t", "DISTINCT clause"},
			{"SELECT DISTINCT ON(id) id FROM t ORDER BY id", "DISTINCT clause"},
			{"SELECT id FROM t GROUP BY id", "GROUP BY clause"},
			{"SELECT count(*) FROM t HAVING count(*)>0", "HAVING clause"},
			{"SELECT count(*) FROM t", "aggregate functions"},
		} {
			for _, prepared := range []bool{false, true} {
				q := tc.prefix + " FOR " + mode + " OF t SKIP LOCKED FOR UPDATE NOWAIT"
				t.Run(fmt.Sprintf("%s/%v", q, prepared), func(t *testing.T) {
					fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
						stage := "execute"
						if prepared {
							stage = "prepare"
						}
						fixture.QueryLockFailure(t, p, b, a, q, prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR " + mode + " is not allowed with " + tc.reason}, stage)
						fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", true, 4)
					})
				})
			}
		}
	}
	for _, op := range []string{"UNION", "INTERSECT", "EXCEPT"} {
		for _, modifier := range []string{"", " ALL", " DISTINCT"} {
			for _, q := range []string{
				"SELECT id FROM t " + op + modifier + " SELECT id FROM u FOR SHARE NOWAIT",
				"(SELECT id FROM t " + op + modifier + " SELECT id FROM u) FOR SHARE NOWAIT",
				"(SELECT id FROM t FOR SHARE OF t SKIP LOCKED) " + op + modifier + " SELECT id FROM u",
				"SELECT id FROM t " + op + modifier + " (SELECT id FROM u FOR SHARE NOWAIT)",
				"(SELECT id FROM t " + op + modifier + " (SELECT id FROM u FOR SHARE)) UNION SELECT id FROM t",
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%v", q, prepared), func(t *testing.T) {
						fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
							stage := "execute"
							if prepared {
								stage = "prepare"
							}
							fixture.QueryLockFailure(t, p, b, a, q, prepared, fixture.Diagnostic{Code: "0A000", Message: "FOR SHARE is not allowed with UNION/INTERSECT/EXCEPT"}, stage)
							fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", false, 4)
							fixture.RowAffected(t, p.Ctx, peer, "UPDATE u SET v=v", false, 4)
						})
					})
				}
			}
		}
	}
	// Competing diagnostics are independent hypotheses for the pinned oracle.
	for _, tc := range []struct{ q, code, message, token string }{
		{"SELECT DISTINCT absent FROM t FOR UPDATE", "42703", `column "absent" does not exist`, "absent"},
		{"SELECT DISTINCT id FROM absent FOR UPDATE", "42P01", `relation "absent" does not exist`, "absent"},
		{"SELECT missing FROM t ORDER BY id FOR UPDATE OF missing", "42703", `column "missing" does not exist`, "missing FROM"},
		{"SELECT id FROM t ORDER BY missing FOR UPDATE OF missing", "42703", `column "missing" does not exist`, "missing FOR"},
		{"SELECT id FROM t ORDER BY id FOR UPDATE OF missing", "42P01", `relation "missing" in FOR UPDATE clause not found in FROM clause`, "missing"},
		{"SELECT DISTINCT ON(id) id FROM t ORDER BY v FOR UPDATE", "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions", "id)"},
		{"SELECT id FROM t UNION SELECT id,v FROM u FOR UPDATE", "0A000", "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT", ""},
		{"SELECT id FROM t UNION SELECT 'x'::text FROM u FOR UPDATE", "0A000", "FOR UPDATE is not allowed with UNION/INTERSECT/EXCEPT", ""},
		{"SELECT DISTINCT id FROM t FOR UPDATE SKIP", "42601", "syntax error at end of input", "<end>"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
				if tc.token == "<end>" {
					want.Position = fixture.Position(len([]rune(tc.q)) + 1)
				} else if tc.token != "" {
					want.Position = queryLockPosition(tc.q, tc.token)
				}
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					fixture.QueryLockFailure(t, p, b, a, tc.q, prepared, want, stage)
				}
			})
		})
	}
}
