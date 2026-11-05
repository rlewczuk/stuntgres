package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStateDeclarations(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		want := [][]any{
			{int64(1940), stateFunctions[0], int64(25), nil, nil, nil},
			{int64(2094), stateFunctions[1], int64(1184), nil, nil, nil},
			{int64(2857), stateFunctions[2], int64(1184), nil, nil, nil},
			{int64(2853), stateFunctions[3], int64(25), nil, nil, nil},
			{int64(2788), stateFunctions[4], int64(25), nil, nil, nil},
			{int64(6107), stateFunctions[5], int64(2249), "{23,23,16}", "{i,o,o}", "{bid,subxact_count,subxact_overflowed}"},
		}
		for i := range want {
			want[i] = append(want[i], int64(12), float64(1), float64(0), "f", true, false, "s", "r", int64(1), "23", int64(0), false, false, false, nil, nil, nil, false, stateFunctions[i])
		}
		step := fixture.Select("SELECT oid::bigint,proname::text,prorettype::bigint,proallargtypes::text,proargmodes::text,proargnames::text,prolang::bigint,procost,prorows,prokind::text,proisstrict,proretset,provolatile::text,proparallel::text,pronargs::bigint,proargtypes::text,pronargdefaults::bigint,prosecdef,proleakproof,proargdefaults IS NOT NULL AS defaults,proconfig::text,probin,protrftypes::text,prosqlbody IS NOT NULL AS body,prosrc FROM pg_proc WHERE proname IN ('pg_stat_get_backend_activity','pg_stat_get_backend_activity_start','pg_stat_get_backend_xact_start','pg_stat_get_backend_wait_event','pg_stat_get_backend_wait_event_type','pg_stat_get_backend_subxact')", []string{"oid", "proname", "prorettype", "proallargtypes", "proargmodes", "proargnames", "prolang", "procost", "prorows", "prokind", "proisstrict", "proretset", "provolatile", "proparallel", "pronargs", "proargtypes", "pronargdefaults", "prosecdef", "proleakproof", "defaults", "proconfig", "probin", "protrftypes", "body", "prosrc"}, []string{"INT8", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "INT8", "FLOAT4", "FLOAT4", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "INT8", "TEXT", "INT8", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT"}, want)
		step.Bag = true
		stateBoth(t, p, b, o, step)
		// Preparing before birth cannot acquire the status cut.
		held := statePrepare(t, p, o)
		peer := statActivityConn(t, p, b)
		mapped := stateMapped(t, p, b, o, peer)
		id := mapped[0]
		activityExec(t, p, peer, "/* backend state shape */ SELECT 17")
		visible := stateRead(t, p, b, o, id)
		stateEqual(t, stateRead(t, p, b, held, id), visible)
		for i, f := range stateFunctions {
			queries := []string{"SELECT pg_catalog." + f + "($1) AS value"}
			if i < 5 {
				queries = append(queries, "SELECT * FROM "+f+"($1) AS f(value)")
			}
			for _, q := range queries {
				for _, prepared := range []bool{false, true} {
					var e fixture.Executor = o
					if prepared {
						s, err := o.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						e = fixture.HeldStatement{Executor: o, Statement: s, SQL: q}
					}
					r, err := e.QueryContext(p.Ctx, q, id)
					if err != nil {
						t.Fatal(err)
					}
					got := stateRows(t, r, b.PG, []string{"value"}, []string{stateTypes[i]})
					if len(got) != 1 {
						t.Fatal("scalar FROM/target cardinality", got)
					}
					stateEqual(t, got[0], []any{visible[i]})
				}
			}
		}
		for _, q := range []string{"SELECT * FROM pg_stat_get_backend_subxact($1)", "SELECT (pg_stat_get_backend_subxact($1)).*", "SELECT (pg_stat_get_backend_subxact(bid => $1)).subxact_count,(pg_stat_get_backend_subxact(bid => $1)).subxact_overflowed"} {
			for _, limit := range []string{"", " LIMIT 0"} {
				want := [][]any{{int64(0), false}}
				if limit != "" {
					want = [][]any{}
				}
				stateBoth(t, p, b, o, fixture.Select(q+limit, stateSubNames, stateSubTypes, want, id))
			}
		}
		for i, f := range stateFunctions {
			var value any = stateMissing
			if i == 1 || i == 2 {
				value = nil
			}
			if i == 5 {
				value = "(,)"
			}
			for _, arg := range []string{"'-1'", "-1::smallint"} {
				stateBoth(t, p, b, o, fixture.Select("SELECT "+f+"("+arg+") AS value", []string{"value"}, []string{stateTypes[i]}, [][]any{{value}}))
			}
		}
		// Hold actual unread public Rows while a different physical session changes
		// and retires. Consume before issuing another command on streaming o.
		r, err := held.QueryContext(p.Ctx, stateSQL, id)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		activityExec(t, p, peer, "SELECT 99")
		admin := statActivityConn(t, p, b)
		statActivitySignal(t, p, admin, mapped[1].(int64), true)
		statActivityRetired(t, p, admin, mapped[1].(int64))
		got := stateRows(t, r, b.PG, stateNames, stateTypes)
		if len(got) != 1 {
			t.Fatal(got)
		}
		stateEqual(t, got[0], visible)
	})
}

func TestReferenceBackendStateNulls(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		mapped := stateMapped(t, p, b, o, peer)
		activityExec(t, p, o, "CREATE TABLE bs_null(v integer)")
		activityExec(t, p, o, "INSERT INTO bs_null VALUES(NULL)")
		for i, f := range stateFunctions {
			for _, q := range []string{"SELECT " + f + "(NULL) AS value", "SELECT " + f + "($1::integer) AS value", "SELECT " + f + "(v) AS value FROM bs_null"} {
				args := []any{}
				if q == "SELECT "+f+"($1::integer) AS value" {
					args = []any{nil}
				}
				stateBoth(t, p, b, o, fixture.Select(q, []string{"value"}, []string{stateTypes[i]}, [][]any{{nil}}, args...))
			}
		}
		for _, id := range []int64{-1, -2147483648, 2147483647} {
			stateAbsent(t, p, b, o, id)
			stateBoth(t, p, b, o, fixture.Select("SELECT pg_stat_get_backend_subxact(NULL)::text AS strict,pg_stat_get_backend_subxact($1)::text AS missing", []string{"strict", "missing"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "(,)"}}, id))
			stateBoth(t, p, b, o, fixture.Select("SELECT * FROM pg_stat_get_backend_subxact($1)", stateSubNames, stateSubTypes, [][]any{{nil, nil}}, id))
			stateBoth(t, p, b, o, fixture.Select("SELECT * FROM pg_stat_get_backend_subxact(NULL)", stateSubNames, stateSubTypes, [][]any{{nil, nil}}))
			for _, f := range stateFunctions {
				if backendCount(t, p, o, fmt.Sprintf("SELECT count(*) FROM %s(%d)", f, id)) != 1 {
					t.Fatal("strict scalar is never SRF", f)
				}
			}
		}
		stateSub(t, p, b, o, mapped[0], 0, false, true)
		// A literal scalar STRICT NULL must not initialize activity membership.
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		stateBoth(t, p, b, o, fixture.Select("SELECT pg_stat_get_backend_activity(NULL) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{nil}}))
		newcomer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, newcomer)
		identityForPID(t, identitySet(t, p, b, o), pid)
		activityExec(t, p, o, "ROLLBACK")
	})
}
