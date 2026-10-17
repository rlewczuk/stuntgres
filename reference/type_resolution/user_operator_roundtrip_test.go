package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func userOperatorRoundTripSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	userOperatorExec(t, p, b, c,
		"CREATE ROLE userop_rt_owner LOGIN",
		"CREATE SCHEMA userop_rt",
		"CREATE SCHEMA userop_rt_moved AUTHORIZATION userop_rt_owner",
		"CREATE OPERATOR userop_rt.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
		"CREATE OPERATOR userop_rt.##~ (RIGHTARG=int4,FUNCTION=int4um)",
		"CREATE OPERATOR userop_rt.##~ (RIGHTARG=text,FUNCTION=lower)",
		"CREATE OPERATOR userop_rt.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(userop_rt.#=#),NEGATOR=OPERATOR(userop_rt.#!#),RESTRICT=eqsel,JOIN=eqjoinsel,HASHES,MERGES)",
		"CREATE OPERATOR userop_rt.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(userop_rt.#@#))",
		"DROP OPERATOR userop_rt.#?# (int4,int4)",
		"ALTER OPERATOR userop_rt.#~# (int4,int4) SET SCHEMA userop_rt_moved",
		"ALTER OPERATOR userop_rt_moved.#~# (int4,int4) OWNER TO userop_rt_owner",
		"ALTER SCHEMA userop_rt RENAME TO userop_rt_new",
		"CREATE DOMAIN public.userop_rt_alias AS regoperator",
		"CREATE TYPE public.userop_rt_pair AS (op regoperator,routine regprocedure)",
		"CREATE TABLE public.userop_rt_data(id integer,op regoperator,short regoper,wrapped userop_rt_alias,ops userop_rt_pair[],mixed userop_rt_pair)",
		"CREATE VIEW public.userop_rt_view AS SELECT 2 OPERATOR(userop_rt_moved.#~#) 3 AS v",
		"CREATE TABLE public.userop_rt_consumer(i integer DEFAULT (2 OPERATOR(userop_rt_moved.#~#) 3),g integer GENERATED ALWAYS AS (i OPERATOR(userop_rt_moved.#~#) 3) STORED,CONSTRAINT userop_rt_positive CHECK ((i OPERATOR(userop_rt_moved.#~#) 3)>0))",
		"CREATE INDEX userop_rt_idx ON public.userop_rt_consumer ((i OPERATOR(userop_rt_moved.#~#) 3))")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_rt_data VALUES (1,'userop_rt_moved.#~#(int4,int4)'::regoperator,'userop_rt_moved.#~#(int4,int4)'::regoperator::regoper,'userop_rt_moved.#~#(int4,int4)'::regoperator::userop_rt_alias,ARRAY[ROW('userop_rt_moved.#~#(int4,int4)'::regoperator,'int4pl(int4,int4)'::regprocedure)::userop_rt_pair,NULL::userop_rt_pair,ROW('0'::regoperator,'0'::regprocedure)::userop_rt_pair,ROW('4294967295'::regoperator,'4294967295'::regprocedure)::userop_rt_pair],ROW('userop_rt_moved.#~#(int4,int4)'::regoperator,'int4pl(int4,int4)'::regprocedure)::userop_rt_pair)", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE public.userop_rt_data SET ops[0]=ops[1]", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_rt_consumer DEFAULT VALUES", 1))
}
func userOperatorRoundTripCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_rt_moved.#~#) 3 AS v", "INT4", int64(5))
	userOperatorValue(t, p, b, c, "SELECT OPERATOR(userop_rt_new.##~) 3 AS v", "INT4", int64(-3))
	userOperatorValue(t, p, b, c, "SELECT OPERATOR(userop_rt_new.##~) 'ABC'::text AS v", "TEXT", "abc")
	userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_rt_view", "INT4", int64(5))
	userOperatorValue(t, p, b, c, "SELECT g AS v FROM public.userop_rt_consumer", "INT4", int64(8))
	userOperatorValue(t, p, b, c, "SELECT oprcode::oid=177 AND oprleft=23 AND oprright=23 AND oprresult=23 AND oprowner=(SELECT oid FROM pg_roles WHERE rolname='userop_rt_owner') AS v FROM pg_operator WHERE oid='userop_rt_moved.#~#(int4,int4)'::regoperator", "BOOL", true)
	userOperatorValue(t, p, b, c, "SELECT oprcanhash AND oprcanmerge AND oprrest::oid=101 AND oprjoin::oid=105 AND oprcom=oid AND oprnegate='userop_rt_new.#!#(int4,int4)'::regoperator::oid AS v FROM pg_operator WHERE oid='userop_rt_new.#=#(int4,int4)'::regoperator", "BOOL", true)
	for _, name := range []string{"#!#", "#@#"} {
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid=0 AND oprresult=0 AS v FROM pg_operator WHERE oid='userop_rt_new."+name+"(int4,int4)'::regoperator", "BOOL", true)
	}
	userOperatorValue(t, p, b, c, "SELECT oprcom=0 AS v FROM pg_operator WHERE oid='userop_rt_new.#@#(int4,int4)'::regoperator", "BOOL", true)
	userOperatorValue(t, p, b, c, "SELECT op='userop_rt_moved.#~#(int4,int4)'::regoperator AND short=op::regoper AND wrapped::regoperator=op AND array_lower(ops,1)=0 AND (ops[0]).op=op AND (ops[1]).op=op AND ops[2] IS NULL AND (ops[3]).op::oid=0 AND (ops[4]).op::oid=4294967295 AND (mixed).op=op AND (mixed).routine='int4pl(int4,int4)'::regprocedure AS v FROM public.userop_rt_data", "BOOL", true)
}
func TestReferenceUserOperatorNativeRoundTrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_rt_owner"})
	fixture.SchemaReferenceCleanup(t, "userop_rt", "userop_rt_new", "userop_rt_moved")
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			userOperatorRoundTripSetup(t, p, b, c)
			userOperatorRoundTripCheck(t, p, b, c)
			if b.PG {
				if len(emitted) == 0 {
					t.Fatal("actual native dump is required")
				}
				userOperatorExec(t, p, b, c, "DROP VIEW public.userop_rt_view", "DROP TABLE public.userop_rt_consumer", "DROP TABLE public.userop_rt_data", "DROP TYPE public.userop_rt_pair", "DROP DOMAIN public.userop_rt_alias", "DROP SCHEMA userop_rt_new CASCADE", "DROP SCHEMA userop_rt_moved CASCADE")
				if _, err = c.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("actual native emitted SQL replay", err)
				}
				userOperatorRoundTripCheck(t, p, b, c)
				return
			}
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			retained, err := c.QueryContext(p.Ctx, "SELECT op FROM public.userop_rt_data")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			fixture.RegtypeCursorMetadata(t, retained, false, []string{"op"}, []string{"REGOPERATOR"}, []bool{true})
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			userOperatorRoundTripCheck(t, p, b, c)
			fixture.RegtypeAssertRows(t, retained, [][]any{{"userop_rt_moved.#~#(integer,integer)"}}, false)
			var redump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(emitted, redump.Bytes()) {
				t.Fatal("equivalent operator catalog redump changed")
			}
			fresh := fixture.NewFixture(t, false, true)
			fc, err := fresh.Memory.Conn(fresh.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fc.Close()
			fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
			userOperatorExec(t, fresh, fb, fc, "CREATE ROLE userop_rt_owner LOGIN", "CREATE TABLE unrelated(i integer)", "CREATE OPERATOR public.#/# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)")
			if err = stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			userOperatorRoundTripCheck(t, fresh, fb, fc)
			userOperatorExec(t, p, b, c, "CREATE TABLE public.userop_rt_sentinel(v integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_rt_sentinel VALUES(42)", 1))
			for _, bad := range [][]byte{append(bytes.Clone(emitted), []byte(" SELECT (")...), []byte("CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl); ALTER OPERATOR public.#~# (int4,int4) OWNER TO userop_rt_missing;")} {
				if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
					t.Fatal("invalid restore succeeded")
				}
				userOperatorRoundTripCheck(t, p, b, c)
				userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_rt_sentinel", "INT4", int64(42))
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled restore", err)
			}
			userOperatorRoundTripCheck(t, p, b, c)
		})
	}
}

func TestReferenceUserOperatorFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_smoke_reader"})
	fixture.SchemaReferenceCleanup(t, "userop_smoke", "userop_smoke_new", "userop_smoke_moved", "userop_smoke_permission")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		userOperatorExec(t, p, b, c, "CREATE SCHEMA userop_smoke", "CREATE SCHEMA userop_smoke_moved", "CREATE ROLE userop_smoke_reader", "CREATE OPERATOR userop_smoke.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "CREATE OPERATOR userop_smoke.##~ (RIGHTARG=int4,FUNCTION=int4um)", "CREATE VIEW public.userop_smoke_view AS SELECT 2 OPERATOR(userop_smoke.#~#) 3 AS v", "CREATE TABLE public.userop_smoke_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.userop_smoke_sentinel VALUES(42)", 1))
		userOperatorValue(t, p, b, c, "SELECT $1::int4 OPERATOR(userop_smoke.#~#) $2::int4 AS v", "INT4", int64(5), 2, 3)
		query := "SELECT $1::int4 OPERATOR(userop_smoke.#~#) $2::int4 AS v"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for _, tc := range []struct {
			left, right any
			want        any
		}{{2, 3, int64(5)}, {nil, 3, nil}, {4, 5, int64(9)}, {2, 3, int64(5)}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.left, tc.right))
		}
		userOperatorValue(t, p, b, c, "SELECT NULL::int4 OPERATOR(userop_smoke.#~#) 3 AS v", "INT4", nil)
		userOperatorValue(t, p, b, c, "SELECT OPERATOR(userop_smoke.##~) 3 AS v", "INT4", int64(-3))
		userOperatorValue(t, p, b, c, "SELECT oprcode::oid::int8 AS v FROM pg_operator WHERE oid='userop_smoke.#~#(int4,int4)'::regoperator", "INT8", int64(177))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'userop_smoke.#~#(int4,int4)'::regoperator::oid").Scan(&oid); err != nil || oid == 0 {
			t.Fatal(oid, err)
		}
		// Keep the explicit namespace ACL in a separate permission fixture:
		// native Dump deliberately does not serialize custom schema ACLs.
		userOperatorExec(t, p, b, c, "CREATE SCHEMA userop_smoke_permission", "CREATE OPERATOR userop_smoke_permission.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "GRANT USAGE ON SCHEMA userop_smoke_permission TO userop_smoke_reader", "SET ROLE userop_smoke_reader")
		userOperatorError(t, p, b, c, "ALTER OPERATOR userop_smoke_permission.#~# (int4,int4) SET (RESTRICT=NONE)", "42501", "must be owner of operator #~#", "", "", "", false)
		userOperatorExec(t, p, b, c, "RESET ROLE", "DROP SCHEMA userop_smoke_permission CASCADE")
		userOperatorError(t, p, b, c, "CREATE OPERATOR userop_smoke.#?# (LEFTARG=int2,RIGHTARG=int2,FUNCTION=int4pl)", "42883", "function int4pl(smallint, smallint) does not exist", "", "", "", false)
		userOperatorExec(t, p, b, c, "ALTER SCHEMA userop_smoke RENAME TO userop_smoke_new", "ALTER OPERATOR userop_smoke_new.#~# (int4,int4) SET SCHEMA userop_smoke_moved")
		userOperatorValue(t, p, b, c, "SELECT 'userop_smoke_moved.#~#(int4,int4)'::regoperator::oid::int8 AS v", "INT8", oid)
		userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_smoke_view", "INT4", int64(5))
		userOperatorError(t, p, b, c, "DROP OPERATOR userop_smoke_moved.#~# (int4,int4)", "2BP01", "cannot drop operator userop_smoke_moved.#~#(integer,integer) because other objects depend on it", "view userop_smoke_view depends on operator userop_smoke_moved.#~#(integer,integer)", "Use DROP ... CASCADE to drop the dependent objects too.", "", false)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_smoke_view", "INT4", int64(5))
		}
		userOperatorExec(t, p, b, c, "DROP OPERATOR userop_smoke_moved.#~# (int4,int4) CASCADE")
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='userop_smoke_view' AND relnamespace='public'::regnamespace", "INT8", int64(0))
		userOperatorValue(t, p, b, c, "SELECT v FROM public.userop_smoke_sentinel", "INT4", int64(42))
	})
}
