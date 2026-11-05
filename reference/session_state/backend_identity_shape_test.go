package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendIdentityDeclarations(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		q := "SELECT oid::bigint,proname::text,pronargs::bigint,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,proretset,prorows::text,procost::text,proargtypes::text,proargnames::text,proallargtypes::text,proargmodes::text,pronargdefaults::bigint,proargdefaults IS NULL AS no_defaults FROM pg_proc WHERE oid IN (1936,1937,1938,1939,1391,1392,1393) ORDER BY oid"
		want := [][]any{
			{int64(1391), identityFunctions[3], int64(1), int64(1184), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
			{int64(1392), identityFunctions[4], int64(1), int64(869), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
			{int64(1393), identityFunctions[5], int64(1), int64(23), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
			{int64(1936), "pg_stat_get_backend_idset", int64(0), int64(23), true, "s", "r", true, "100", "1", "", nil, nil, nil, int64(0), true},
			{int64(1937), identityFunctions[0], int64(1), int64(23), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
			{int64(1938), identityFunctions[1], int64(1), int64(26), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
			{int64(1939), identityFunctions[2], int64(1), int64(26), true, "s", "r", false, "0", "1", "23", nil, nil, nil, int64(0), true},
		}
		step := fixture.Select(q, []string{"oid", "proname", "pronargs", "prorettype", "proisstrict", "provolatile", "proparallel", "proretset", "prorows", "procost", "proargtypes", "proargnames", "proallargtypes", "proargmodes", "pronargdefaults", "no_defaults"}, []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL"}, want)
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		var ids []int64
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"SELECT pg_stat_get_backend_idset()", "SELECT * FROM pg_stat_get_backend_idset()", "SELECT id FROM pg_stat_get_backend_idset() AS b(id)"} {
				name := "pg_stat_get_backend_idset"
				if query == "SELECT id FROM pg_stat_get_backend_idset() AS b(id)" {
					name = "id"
				}
				var e fixture.Executor = o
				if prepared {
					s, err := o.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					e = fixture.HeldStatement{Executor: o, Statement: s, SQL: query}
				}
				r, err := e.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				rows := identityRows(t, r, b.PG, []string{name}, []string{"INT4"})
				var got []int64
				seen := map[int64]bool{}
				for _, row := range rows {
					id := row[0].(int64)
					if id < 0 || seen[id] {
						t.Fatal("idset bag", rows)
					}
					seen[id] = true
					got = append(got, id)
				}
				sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
				if len(got) == 0 {
					t.Fatal("live observer absent")
				}
				if ids != nil && !reflect.DeepEqual(got, ids) {
					t.Fatal("shared idset target/FROM cut", got, ids)
				}
				ids = got
			}
			query := "SELECT id,n FROM pg_stat_get_backend_idset() WITH ORDINALITY AS b(id,n) ORDER BY n"
			var e fixture.Executor = o
			if prepared {
				s, err := o.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				e = fixture.HeldStatement{Executor: o, Statement: s, SQL: query}
			}
			r, err := e.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			rows := identityRows(t, r, b.PG, []string{"id", "n"}, []string{"INT4", "INT8"})
			if len(rows) != len(ids) {
				t.Fatal("ordinal cardinality", rows, ids)
			}
			for i, row := range rows {
				if row[1] != int64(i+1) {
					t.Fatal("one-based ordinal", rows)
				}
			}
			filtered := fixture.Select("SELECT count(*),min(n),max(n) FROM pg_stat_get_backend_idset() WITH ORDINALITY AS b(id,n) WHERE n>1", []string{"count", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(len(ids) - 1), int64(2), int64(len(ids))}})
			if len(ids) == 1 {
				filtered.Rows = [][]any{{int64(0), nil, nil}}
			}
			if prepared {
				filtered = fixture.Prepared(filtered)
			}
			fixture.RunStep(t, p.Ctx, o, b.Recorder, filtered)
		}
		for _, query := range []string{"SELECT pg_stat_get_backend_idset() LIMIT 0", "SELECT * FROM pg_stat_get_backend_idset() LIMIT 0"} {
			step := fixture.Select(query, []string{"pg_stat_get_backend_idset"}, []string{"INT4"}, [][]any{})
			fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
		}
	})
}

func TestReferenceBackendIdentityMapping(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bi_mapping_login"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		admin := statActivityConn(t, p, b)
		peer := statActivityConn(t, p, b)
		other := statActivityConn(t, p, b)
		activityExec(t, p, admin, "CREATE ROLE bi_mapping_login LOGIN PASSWORD 'role-core-fixture'")
		activityExec(t, p, admin, "CREATE DATABASE bi_mapping_database")
		defer admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS bi_mapping_database")
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, "bi_mapping_database", "bi_mapping_login")
		defer pool.Close()
		cross, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer cross.Close()
		for _, c := range []*sql.Conn{o, admin, peer, other, cross} {
			identityMapped(t, p, b, o, c)
		}
		before := identityMapped(t, p, b, o, peer)
		held, err := o.PrepareContext(p.Ctx, identitySetSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		e := fixture.HeldStatement{Executor: o, Statement: held, SQL: identitySetSQL}
		identityEqual(t, identityForPID(t, identitySet(t, p, b, e), before[1].(int64)), before)
		for i, name := range identityFunctions {
			for _, from := range []bool{false, true} {
				q := "SELECT " + name + "($1) AS value"
				if from {
					q = "SELECT value FROM " + name + "($1) AS f(value)"
				}
				s, err := o.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, prepared := range []bool{false, true} {
					var source fixture.Executor = o
					if prepared {
						source = fixture.HeldStatement{Executor: o, Statement: s, SQL: q}
					}
					r, err := source.QueryContext(p.Ctx, q, before[0])
					if err != nil {
						s.Close()
						t.Fatal(err)
					}
					got := identityRows(t, r, b.PG, []string{"value"}, []string{identityTypes[i]})
					if len(got) != 1 || !reflect.DeepEqual(fixture.Normalize(got[0][0]), fixture.Normalize(before[i+1])) {
						s.Close()
						t.Fatal("independently mapped direct/prepared scalar target/FROM", name, got, before)
					}
				}
				s.Close()
			}
		}
		otherPID := statActivityPID(t, p, other)
		statActivitySignal(t, p, admin, otherPID, true)
		statActivityRetired(t, p, admin, otherPID)
		statActivityClear(t, p, o)
		identityEqual(t, identityForPID(t, identitySet(t, p, b, o), before[1].(int64)), before)
		rows := identitySet(t, p, b, o)
		// Establish namespace nonmembership before using a PID as a negative slot.
		for _, c := range []*sql.Conn{peer, cross, admin, o} {
			pid := statActivityPID(t, p, c)
			represented := false
			for _, row := range rows {
				represented = represented || row[0] == pid
			}
			if !represented {
				got := identityLookup(t, p, b, o, pid)
				for _, value := range got[1:] {
					if value != nil {
						t.Fatal("PID used as slot", got)
					}
				}
				return
			}
		}
		t.Fatal("no independently established PID/slot separation witness")
	})
}

func TestReferenceBackendIdentityNulls(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		rows := identitySet(t, p, b, o)
		for _, row := range rows {
			if row[0] == int64(2147483647) {
				t.Fatal("high absent-ID prerequisite", rows)
			}
		}
		for i, name := range identityFunctions {
			q := "SELECT " + name + "($1) AS value"
			s, err := o.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = o
				if prepared {
					e = fixture.HeldStatement{Executor: o, Statement: s, SQL: q}
				}
				for _, input := range []any{nil, int64(-1), int64(-2147483648), int64(2147483647)} {
					r, err := e.QueryContext(p.Ctx, q, input)
					if err != nil {
						t.Fatal(err)
					}
					got := identityRows(t, r, b.PG, []string{"value"}, []string{identityTypes[i]})
					if !reflect.DeepEqual(got, [][]any{{nil}}) {
						t.Fatal(name, input, got)
					}
				}
			}
			for _, arg := range []string{"NULL", "NULL::integer", "-1", "-2147483648", "2147483647"} {
				for _, from := range []bool{false, true} {
					query := "SELECT " + name + "(" + arg + ") AS value"
					if from {
						query = "SELECT value FROM " + name + "(" + arg + ") AS f(value)"
					}
					for _, limit := range []string{"", " LIMIT 0"} {
						want := [][]any{{nil}}
						if limit != "" {
							want = [][]any{}
						}
						step := fixture.Select(query+limit, []string{"value"}, []string{identityTypes[i]}, want)
						step.CheckMetadata = func(t *testing.T, r *sql.Rows) {
							identityMetadata(t, r, b.PG, []string{"value"}, []string{identityTypes[i]})
						}
						fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
						fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
					}
				}
			}
			// Coercion is allowed, but slot 1 (and especially 0) is not assumed absent.
			for _, arg := range []string{"'1'", "1::smallint", "0"} {
				id := int64(1)
				if arg == "0" {
					id = 0
				}
				var want any
				for _, row := range rows {
					if row[0] == id {
						activity := statActivityGet(t, p, o, row[1].(int64), false)
						want = fixture.Normalize(activity[[]int{2, 0, 4, 10, 7, 9}[i]])
					}
				}
				step := fixture.Select(fmt.Sprintf("SELECT %s(%s) AS value", name, arg), []string{"value"}, []string{identityTypes[i]}, [][]any{{want}})
				fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, o, b.Recorder, fixture.Prepared(step))
			}
		}
	})
}
