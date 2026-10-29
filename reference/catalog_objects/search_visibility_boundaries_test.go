package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSearchVisibilityBoundaries(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sv_reader"})
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		run("SET search_path=sv_a,sv_b")
		for i, k := range searchVisibilityClasses {
			stmt, err := c.PrepareContext(p.Ctx, searchVisibilityStep(k, nil, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, arg := range []any{nil, int64(0), int64(4294967295)} {
				searchVisibilityRoutes(t, p, b, c, k, stmt, arg, nil)
			}
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			for _, arg := range []any{int(a), a, strconv.FormatInt(a, 10)} {
				searchVisibilityRoutes(t, p, b, c, k, stmt, arg, true)
			}
			for _, literal := range []string{"NULL", "NULL::oid"} {
				s := fixture.Select("SELECT pg_catalog."+k.fn+"("+literal+")", []string{k.fn}, []string{"BOOL"}, [][]any{{nil}})
				s.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				literalStatement, err := c.PrepareContext(p.Ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer literalStatement.Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literalStatement, SQL: s.Query}, b.Recorder, s)
				if err := literalStatement.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// OIDs are class-local: establish absence before claiming a wrong-class NULL.
			var count int64
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_catalog."+k.catalog+" WHERE oid=23::oid").Scan(&count); err != nil || count != 0 {
				t.Fatal("wrong-class absence", count, err)
			}
			searchVisibilityRoutes(t, p, b, c, k, stmt, int64(23), nil)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
				"SELECT oid::bigint,pronamespace::bigint,proargtypes::text,prorettype::bigint,proisstrict,provolatile::text,proparallel::text,proretset,pronargs,procost,prosrc,prolang::bigint,prokind::text,pronargdefaults,provariadic::bigint FROM pg_catalog.pg_proc WHERE oid=$1",
				[]string{"oid", "pronamespace", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "proretset", "pronargs", "procost", "prosrc", "prolang", "prokind", "pronargdefaults", "provariadic"},
				[]string{"INT8", "INT8", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "INT2", "FLOAT4", "TEXT", "INT8", "TEXT", "INT2", "INT8"},
				[][]any{{k.routine, int64(11), "26", int64(16), true, "s", "s", false, int64(1), float64(10), k.fn, int64(12), "f", int64(0), int64(0)}}, k.routine))
			if k.builtin != 0 {
				builtinName := "simple"
				if i == 2 {
					builtinName = "default"
				}
				if got := searchVisibilityDiscover(t, p, c, k, "pg_catalog", builtinName); got != k.builtin {
					t.Fatal("released builtin identity", got, k.builtin)
				}
				searchVisibilityRoutes(t, p, b, c, k, stmt, k.builtin, true)
				run("BEGIN")
				run("ALTER " + k.ddl + " sv_a.v RENAME TO \"" + builtinName + "\"")
				run("SET LOCAL search_path=sv_a,pg_catalog")
				a := searchVisibilityDiscover(t, p, c, k, "sv_a", builtinName)
				searchVisibilityRoutes(t, p, b, c, k, stmt, a, true)
				searchVisibilityRoutes(t, p, b, c, k, stmt, k.builtin, false)
				run("SET LOCAL search_path=pg_catalog,sv_a")
				searchVisibilityRoutes(t, p, b, c, k, stmt, a, false)
				searchVisibilityRoutes(t, p, b, c, k, stmt, k.builtin, true)
				run("ROLLBACK")
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_statistic_ext WHERE stxnamespace=11", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}

			// The target exists in b only. An earlier a descriptor of a different
			// class has the same spelling but cannot participate in shadowing.
			isolated := fmt.Sprintf("isolated_%d", i)
			switch i {
			case 0:
				run("CREATE TEXT SEARCH CONFIGURATION sv_b." + isolated + " (PARSER=sv_b.v)")
			case 1:
				run("CREATE TEXT SEARCH DICTIONARY sv_b." + isolated + " (TEMPLATE=sv_b.v)")
			default:
				run(searchVisibilityCreate(i, "sv_b."+isolated))
			}
			if i == 2 {
				run("CREATE STATISTICS sv_a." + isolated + " ON x,y FROM sv_off.stats_input")
			} else {
				run(searchVisibilityCreate(2, "sv_a."+isolated))
			}
			run("SET search_path=sv_a,sv_b")
			searchVisibilityRoutes(t, p, b, c, k, stmt, searchVisibilityDiscover(t, p, c, k, "sv_b", isolated), true)
		}

		run("CREATE ROLE sv_reader")
		run("GRANT USAGE ON SCHEMA sv_b TO sv_reader")
		for _, allow := range []bool{false, true, false} {
			if allow {
				run("GRANT USAGE ON SCHEMA sv_a TO sv_reader")
			} else {
				run("REVOKE USAGE ON SCHEMA sv_a FROM sv_reader")
			}
			// Discover while authorized. The reader owns no objects and has no
			// SELECT on the underlying statistics relation in this phase.
			identities := make([][2]int64, len(searchVisibilityClasses))
			for i, k := range searchVisibilityClasses {
				identities[i] = [2]int64{searchVisibilityDiscover(t, p, c, k, "sv_a", "v"), searchVisibilityDiscover(t, p, c, k, "sv_b", "v")}
			}
			run("SET ROLE sv_reader")
			for i, k := range searchVisibilityClasses {
				searchVisibilityRoutes(t, p, b, c, k, nil, identities[i][0], allow)
				searchVisibilityRoutes(t, p, b, c, k, nil, identities[i][1], !allow)
			}
			run("RESET ROLE")
		}
		run("GRANT USAGE ON SCHEMA sv_off TO sv_reader")
		run("GRANT CREATE ON SCHEMA sv_b TO sv_reader")
		for _, i := range []int{0, 1} {
			run("ALTER " + searchVisibilityClasses[i].ddl + " sv_b.v OWNER TO sv_reader")
		}
		subjects := make([]int64, len(searchVisibilityClasses))
		for i, k := range searchVisibilityClasses {
			subjects[i] = searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
		}
		for _, grant := range []bool{true, false} {
			if grant {
				run("GRANT SELECT ON sv_off.stats_input TO sv_reader")
			} else {
				run("REVOKE SELECT ON sv_off.stats_input FROM sv_reader")
			}
			run("SET ROLE sv_reader")
			for i, k := range searchVisibilityClasses {
				searchVisibilityRoutes(t, p, b, c, k, nil, subjects[i], true)
			}
			run("RESET ROLE")
		}
	})
}

func TestReferenceSearchVisibilityCallableResolution(t *testing.T) {
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
		for _, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			run("CREATE FUNCTION sv_a." + k.fn + "(text) RETURNS boolean LANGUAGE SQL AS $$SELECT FALSE$$")
			run("SET search_path=sv_a,pg_catalog")
			query := "SELECT " + k.fn + "($1::oid)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			check := func(want bool) {
				s := fixture.Select(query, []string{k.fn}, []string{"BOOL"}, [][]any{{want}}, a)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
			}
			check(true)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+k.fn+"('x'::text)", []string{k.fn}, []string{"BOOL"}, [][]any{{false}}))
			run("CREATE FUNCTION sv_a." + k.fn + "(oid) RETURNS boolean LANGUAGE SQL AS $$SELECT FALSE$$")
			selected := fixture.Select(query, []string{k.fn}, []string{"BOOL"}, [][]any{{false}}, a)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, selected)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(selected))
			searchVisibilityRoutes(t, p, b, c, k, nil, a, true)
			// Force actual held-plan path transitions, not a CREATE-only reparse promise.
			run("SET search_path=pg_catalog,sv_a")
			check(true)
			run("SET search_path=sv_a,pg_catalog")
			check(false)
			run("SET search_path=pg_catalog,sv_a")
			check(true)
		}
	})
}
