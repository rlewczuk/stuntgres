package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralSources(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralFixture(run)
		ints := [][]any{{int64(1), int64(12)}, {int64(2), int64(13)}, {int64(3), nil}}
		for _, q := range []string{
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT t.v+10 AS m) x ON true ORDER BY t.id",
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL ((SELECT t.v+10 AS m)) x ON true ORDER BY t.id",
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (WITH a AS (SELECT t.v AS v) SELECT v+10 AS m FROM a) x ON true ORDER BY t.id",
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT t.v+10 AS m UNION ALL SELECT t.v+20 WHERE false) x ON true ORDER BY t.id",
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL ((SELECT t.v+10 AS m) INTERSECT (SELECT t.v+10)) x ON true ORDER BY t.id",
			"SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT t.v+10 AS m EXCEPT SELECT t.v+20 WHERE false) x ON true ORDER BY t.id",
		} {
			subqueryBoth(run, fixture.Select(q, []string{"id", "m"}, []string{"INT4", "INT4"}, ints))
		}
		subqueryBoth(run, lateralLatestStep())
		subqueryBoth(run, fixture.Select("SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (((SELECT u.w AS m FROM lat_u u WHERE u.k=t.id ORDER BY u.w DESC LIMIT 1))) x ON true ORDER BY t.id", []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}, {int64(2), "c"}, {int64(3), nil}}))
		subqueryBoth(run, fixture.Select("SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT u.w AS m FROM lat_u u WHERE u.k=t.id ORDER BY u.w LIMIT 1 OFFSET 1) x ON true ORDER BY t.id", []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}, {int64(2), nil}, {int64(3), nil}}))
		subqueryBoth(run, fixture.Select("SELECT t.id,f.a,f.b FROM lat_t t CROSS JOIN LATERAL (VALUES (t.id*2,'x'||t.v)) AS f(a,b) ORDER BY t.id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "x2"}, {int64(2), int64(4), "x3"}, {int64(3), int64(6), nil}}))
		subqueryBoth(run, fixture.Select("SELECT t.id,f.family,f.usage FROM lat_t t CROSS JOIN LATERAL (VALUES ('a',t.v)) f(family,usage) ORDER BY t.id", []string{"id", "family", "usage"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(2)}, {int64(2), "a", int64(3)}, {int64(3), "a", nil}}))
		subqueryBoth(run, fixture.Select("SELECT t.id,f.n FROM lat_t t,LATERAL ((VALUES(t.v),(t.v+1)) ORDER BY 1 NULLS LAST LIMIT 1) f(n) ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), nil}}))
		subqueryBoth(run, fixture.Select("SELECT t.id,s.n FROM lat_t t,LATERAL (SELECT count(*) AS n FROM lat_u u WHERE u.k=t.id) s ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), int64(0)}}))
		subqueryBoth(run, lateralChainStep())
		for _, q := range []string{
			"SELECT t.id,x.n FROM lat_t t LEFT JOIN LATERAL (SELECT t.v AS n WHERE false) x ON true ORDER BY t.id",
			"SELECT t.id,x.n FROM lat_t t LEFT JOIN LATERAL (SELECT t.v AS n) x ON false ORDER BY t.id",
			"SELECT t.id,x.n FROM lat_t t LEFT JOIN LATERAL (VALUES(t.v)) x(n) ON NULL::boolean ORDER BY t.id",
		} {
			subqueryBoth(run, fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(3), nil}}))
		}
		subqueryBoth(run, fixture.Select("SELECT t.id,x.n FROM lat_t t CROSS JOIN LATERAL (SELECT t.v AS n WHERE false) x", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{}))
		subqueryBoth(run, fixture.Select("SELECT t.id,x.n FROM lat_t t LEFT JOIN LATERAL (VALUES(t.v),(t.v+10)) x(n) ON x.n>5 ORDER BY t.id", []string{"id", "n"}, []string{"INT4", "INT4"}, ints))
		// A present all-NULL tuple serializes as (), unlike an absent joined row.
		subqueryBoth(run, fixture.Select("SELECT t.id,x::text AS present,y::text AS absent FROM lat_t t LEFT JOIN LATERAL (VALUES(t.v*NULL::int)) x(n) ON true LEFT JOIN LATERAL (SELECT t.v AS n WHERE false) y ON true ORDER BY t.id", []string{"id", "present", "absent"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "()", nil}, {int64(2), "()", nil}, {int64(3), "()", nil}}))
		// Whole-row null-extension reads remain real dependencies even when
		// the source contributes no ordinary columns.
		subqueryBoth(run, fixture.Select("SELECT t.id,q.s FROM lat_t t LEFT JOIN LATERAL (SELECT WHERE t.id=1) z ON true CROSS JOIN LATERAL (SELECT z::text AS s) q ORDER BY t.id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "()"}, {int64(2), nil}, {int64(3), nil}}))
		for _, present := range []bool{false, true} {
			condition, value := "false", any(nil)
			if present {
				condition, value = "true", "()"
			}
			q := "SELECT t.id,q.s FROM ((SELECT WHERE " + condition + ") z RIGHT JOIN (SELECT) unit ON true), (lat_t t CROSS JOIN LATERAL (SELECT z::text AS s) q) ORDER BY t.id"
			subqueryBoth(run, fixture.Select(q, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), value}, {int64(2), value}, {int64(3), value}}))
		}
		for _, tc := range []struct {
			q       string
			columns []string
		}{
			{"SELECT * FROM lat_t t,LATERAL (VALUES(t.v)) ORDER BY t.id", []string{"id", "v", "column1"}},
			{"SELECT * FROM lat_t t,LATERAL (SELECT t.v) s ORDER BY t.id", []string{"id", "v", "v"}},
			{"SELECT * FROM lat_t t,LATERAL (SELECT t.v) ORDER BY t.id", []string{"id", "v", "v"}},
		} {
			subqueryBoth(run, fixture.Select(tc.q, tc.columns, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(2)}, {int64(2), int64(3), int64(3)}, {int64(3), nil, nil}}))
		}
		subqueryBoth(run, fixture.Select("SELECT s.* FROM lat_t t,LATERAL (SELECT t.id,t.v) s(renamed) ORDER BY renamed", []string{"renamed", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), nil}}))
		subqueryBoth(run, fixture.Select("SELECT f.* FROM lat_t t,LATERAL (VALUES(t.id,t.v)) f(renamed) ORDER BY renamed", []string{"renamed", "column2"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), nil}}))
		// Correlation in a bound/ordering expression must survive child planning.
		subqueryBoth(run, fixture.Select("SELECT t.id,x.w FROM lat_t t LEFT JOIN LATERAL (SELECT u.w FROM lat_u u ORDER BY u.w LIMIT t.id-1) x ON true ORDER BY t.id,x.w", []string{"id", "w"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "a"}, {int64(3), "a"}, {int64(3), "b"}}))
	})
}
