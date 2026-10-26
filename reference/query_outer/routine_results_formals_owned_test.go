package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsFormalAggregates(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c,
			`CREATE AGGREGATE rr_formal_rank(VARIADIC direct_values "any" ORDER BY VARIADIC row_values "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA,HYPOTHETICAL)`,
			`CREATE AGGREGATE rr_formal_rows(ORDER BY VARIADIC "any") (SFUNC=pg_catalog.ordered_set_transition_multi,STYPE=internal,FINALFUNC=pg_catalog.rank_final,FINALFUNC_EXTRA)`,
			`CREATE AGGREGATE rr_formal_fixed(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')`)
		check := func(c *sql.Conn, b fixture.Backend) {
			resultsFormalBoth(t, p, b, c, "SELECT proname::text AS name,pronargs::integer AS inputs,proargtypes::text AS identity,proallargtypes::text AS alltypes,proargmodes::text AS modes,provariadic::bigint AS variadic,prokind::text AS kind,proargnames::text AS names FROM pg_proc WHERE proname IN ('rr_formal_rank','rr_formal_rows','rr_formal_fixed') ORDER BY proname", []string{"name", "inputs", "identity", "alltypes", "modes", "variadic", "kind", "names"}, []string{"TEXT", "INT4", "TEXT", "TEXT", "TEXT", "INT8", "TEXT", "TEXT"}, [][]any{
				{"rr_formal_fixed", int64(1), "23", nil, nil, int64(0), "a", nil},
				{"rr_formal_rank", int64(1), "2276", "{2276}", "{v}", int64(2276), "a", "{direct_values}"},
				{"rr_formal_rows", int64(1), "2276", "{2276}", "{v}", int64(2276), "a", nil},
			})
			// The paired hypothetical lists share ONE canonical input identity.
			resultsFormalBoth(t, p, b, c, `SELECT oid='rr_formal_rank("any")'::regprocedure AS identity FROM pg_proc WHERE proname='rr_formal_rank'`, []string{"identity"}, []string{"BOOL"}, [][]any{{true}})
		}
		check(c, b)
		resultsFormalReplay(t, p, b, c, check)
	})
}

func TestReferenceRoutineResultsFormalConstructors(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsExec(t, p, b, c, "CREATE TYPE rr_formal_span AS RANGE(SUBTYPE=integer,MULTIRANGE_TYPE_NAME=rr_formal_many)")
		check := func(c *sql.Conn, b fixture.Backend) {
			var rangeOID, arrayOID, multiOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,typarray::bigint FROM pg_type WHERE typname='rr_formal_span'").Scan(&rangeOID, &arrayOID); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE typname='rr_formal_many'").Scan(&multiOID); err != nil {
				t.Fatal(err)
			}
			if rangeOID <= 0 || arrayOID <= 0 || multiOID <= 0 || rangeOID == arrayOID || multiOID == rangeOID || multiOID == arrayOID {
				t.Fatal("invalid owning type identities", rangeOID, arrayOID, multiOID)
			}
			// Generated OIDs are relational facts of this owner, never fixed
			// allocator goldens copied from the native backend to the oracle.
			q := "SELECT proname::text AS name,pronargs::integer AS inputs,proargtypes::text AS identity,proallargtypes::text AS alltypes,proargmodes::text AS modes,provariadic::bigint AS variadic,prorettype::bigint AS result FROM pg_proc WHERE proname IN ('rr_formal_span','rr_formal_many') ORDER BY proname,pronargs,provariadic DESC"
			resultsFormalBoth(t, p, b, c, q, []string{"name", "inputs", "identity", "alltypes", "modes", "variadic", "result"}, []string{"TEXT", "INT4", "TEXT", "TEXT", "TEXT", "INT8", "INT8"}, [][]any{
				{"rr_formal_many", int64(0), "", nil, nil, int64(0), multiOID},
				{"rr_formal_many", int64(1), fmt.Sprint(arrayOID), fmt.Sprintf("{%d}", arrayOID), "{v}", rangeOID, multiOID},
				{"rr_formal_many", int64(1), fmt.Sprint(rangeOID), nil, nil, int64(0), multiOID},
				{"rr_formal_span", int64(2), "23 23", nil, nil, int64(0), rangeOID},
				{"rr_formal_span", int64(3), "23 23 25", nil, nil, int64(0), rangeOID},
			})
		}
		check(c, b)
		resultsFormalReplay(t, p, b, c, check)
	})
}
