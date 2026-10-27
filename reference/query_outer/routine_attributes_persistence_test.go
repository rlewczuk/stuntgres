package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func attributesPersistSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c, "CREATE ROLE ra_persist_owner", "CREATE ROLE ra_persist_caller", "CREATE ROLE ra_persist_denied", "GRANT USAGE ON SCHEMA public TO PUBLIC",
		"CREATE TYPE ra_persist_pair AS (v integer,note text)", "CREATE TABLE ra_persist_data(v integer)", "ALTER TABLE ra_persist_data OWNER TO ra_persist_owner",
		"CREATE TABLE ra_persist_input(k integer,v integer)", "GRANT SELECT ON ra_persist_input TO PUBLIC",
		"CREATE FUNCTION ra_persist_sql(x integer DEFAULT 4) RETURNS integer LANGUAGE SQL IMMUTABLE STRICT SECURITY DEFINER PARALLEL RESTRICTED COST 7 SET application_name='persist' AS 'SELECT x+v FROM public.ra_persist_data'",
		"CREATE FUNCTION ra_persist_standard(x integer DEFAULT 4) RETURNS integer LANGUAGE SQL STABLE STRICT SECURITY DEFINER PARALLEL SAFE COST 9 SET application_name='standard' BEGIN ATOMIC SELECT x+v FROM public.ra_persist_data;END",
		"CREATE FUNCTION ra_persist_pl(x integer DEFAULT 4) RETURNS text LANGUAGE plpgsql STABLE STRICT SECURITY DEFINER PARALLEL UNSAFE COST 11 SET application_name='pl' AS $$BEGIN RETURN current_setting('application_name')||':'||(SELECT v+x FROM public.ra_persist_data)::text||':'||current_user::text;END$$",
		"CREATE FUNCTION ra_persist_set(x integer) RETURNS SETOF integer LANGUAGE SQL STABLE STRICT SECURITY DEFINER PARALLEL RESTRICTED COST 13 ROWS 12 SET application_name='set' AS 'SELECT v+x FROM public.ra_persist_data'",
		"CREATE FUNCTION ra_persist_variadic(VARIADIC xs integer[] DEFAULT '{}'::integer[]) RETURNS integer[] LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE COST 3 SET application_name='array' AS 'SELECT xs'",
		"CREATE FUNCTION ra_persist_pair_fn(x integer) RETURNS ra_persist_pair LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE COST 5 SET application_name='pair' AS $$SELECT x,current_setting('application_name')$$",
		"CREATE PROCEDURE ra_persist_proc(OUT answer integer,OUT actor text,OUT setting text) LANGUAGE SQL SECURITY DEFINER SET application_name='proc' AS $$SELECT v,current_user::text,current_setting('application_name') FROM public.ra_persist_data$$",
		"CREATE PROCEDURE ra_persist_pl_proc(OUT answer integer,OUT actor text,OUT setting text) LANGUAGE plpgsql SECURITY DEFINER SET application_name='plproc' AS $$BEGIN SELECT v,current_user::text,current_setting('application_name') INTO answer,actor,setting FROM public.ra_persist_data;END$$")
	resultsExec(t, p, b, c, `CREATE FUNCTION ra_persist_path() RETURNS text LANGUAGE SQL STABLE SET search_path=pg_catalog,public,"comma,name","quote""name" AS $$SELECT current_setting('search_path')$$`)
	resultsExec(t, p, b, c,
		"CREATE FUNCTION ra_persist_overload(x integer DEFAULT 6,OUT answer text) LANGUAGE SQL SECURITY DEFINER SET application_name='overload-int' AS $$SELECT current_user::text||':'||x::text$$",
		"CREATE FUNCTION ra_persist_overload(x text) RETURNS text LANGUAGE SQL SECURITY DEFINER SET application_name='overload-text' AS $$SELECT current_user::text||':'||x$$",
		"ALTER FUNCTION ra_persist_overload(integer) OWNER TO ra_persist_owner",
		"ALTER FUNCTION ra_persist_overload(text) OWNER TO ra_persist_caller",
		"ALTER FUNCTION ra_persist_variadic(integer[]) OWNER TO ra_persist_denied",
		"CREATE PROCEDURE ra_persist_inout(INOUT x integer,OUT actor text) LANGUAGE SQL SECURITY DEFINER SET application_name='inout' AS $$SELECT x+1,current_user::text$$",
		"ALTER PROCEDURE ra_persist_inout(integer) OWNER TO ra_persist_caller")
	for _, signature := range []string{"ra_persist_sql(integer)", "ra_persist_standard(integer)", "ra_persist_pl(integer)", "ra_persist_set(integer)"} {
		resultsExec(t, p, b, c, "ALTER FUNCTION "+signature+" OWNER TO ra_persist_owner")
	}
	for _, name := range []string{"ra_persist_proc", "ra_persist_pl_proc"} {
		resultsExec(t, p, b, c, "ALTER PROCEDURE "+name+"() OWNER TO ra_persist_owner")
	}
	resultsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION ra_persist_sql(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ra_persist_sql(integer) TO ra_persist_caller")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_persist_data VALUES(42)", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_persist_input VALUES(0,NULL),(1,1)", 2))
}

func attributesPersistCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET application_name='outside'")
	resultsStoredBoth(t, p, b, c, "SELECT v FROM ra_persist_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
	for _, name := range []string{"ra_persist_sql", "ra_persist_standard"} {
		attributesIntBoth(t, p, b, c, "SELECT "+name+"() AS v", 46)
		proceduresHeld(t, p, b, c, "SELECT "+name+"($1::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{3}, {nil}, {5}}, [][][]any{{{int64(45)}}, {{nil}}, {{int64(47)}}})
	}
	attributesTextBoth(t, p, b, c, "SELECT ra_persist_pl() AS v", "pl:46:ra_persist_owner")
	// The identical emitted SQL must preserve list members, including commas
	// and embedded quotes, on both native replacement and PostgreSQL replay.
	attributesTextBoth(t, p, b, c, "SELECT ra_persist_path() AS v", `pg_catalog, public, "comma,name", "quote""name"`)
	attributesBoth(t, p, b, c, "SELECT ra_persist_set(2) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(44)}})
	attributesBoth(t, p, b, c, "SELECT ra_persist_set(NULL::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{})
	attributesBoth(t, p, b, c, "SELECT ra_persist_variadic() AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}})
	attributesBoth(t, p, b, c, "SELECT ra_persist_variadic(1,NULL,3) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{1,NULL,3}"}})
	attributesBoth(t, p, b, c, "SELECT ra_persist_variadic(VARIADIC NULL::integer[]) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{nil}})
	proceduresBoth(t, p, b, c, "SELECT * FROM ra_persist_pair_fn(8)", []string{"v", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "pair"}})
	for _, item := range []struct{ name, setting string }{{"ra_persist_proc", "proc"}, {"ra_persist_pl_proc", "plproc"}} {
		proceduresBoth(t, p, b, c, "CALL "+item.name+"(NULL,NULL,NULL)", []string{"answer", "actor", "setting"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(42), "ra_persist_owner", item.setting}})
	}
	// Same-name overloads have distinct owners; OUT and defaults must not
	// become identity inputs, while INOUT and the variadic array must.
	attributesTextBoth(t, p, b, c, "SELECT ra_persist_overload() AS v", "ra_persist_owner:6")
	attributesTextBoth(t, p, b, c, "SELECT ra_persist_overload('text'::text) AS v", "ra_persist_caller:text")
	proceduresBoth(t, p, b, c, "CALL ra_persist_inout(8,NULL)", []string{"x", "actor"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "ra_persist_caller"}})
	attributesBoth(t, p, b, c,
		"SELECT p.oid='ra_persist_overload(integer)'::regprocedure AS integer_input,p.oid='ra_persist_overload(text)'::regprocedure AS text_input,p.proowner::regrole::text AS owner,p.pronargdefaults::integer AS defaults,p.proargmodes::text AS modes,p.proconfig::text AS config FROM pg_proc p WHERE p.proname='ra_persist_overload' ORDER BY p.pronargdefaults",
		[]string{"integer_input", "text_input", "owner", "defaults", "modes", "config"}, []string{"BOOL", "BOOL", "TEXT", "INT4", "TEXT", "TEXT"},
		[][]any{{false, true, "ra_persist_caller", int64(0), nil, "{application_name=overload-text}"}, {true, false, "ra_persist_owner", int64(1), "{i,o}", "{application_name=overload-int}"}})
	attributesBoth(t, p, b, c,
		"SELECT proowner::regrole::text AS owner,proargmodes::text AS modes FROM pg_proc WHERE oid='ra_persist_variadic(integer[])'::regprocedure",
		[]string{"owner", "modes"}, []string{"TEXT", "TEXT"}, [][]any{{"ra_persist_denied", "{v}"}})
	attributesBothMetadata(t, p, b, c,
		"SELECT has_schema_privilege('ra_persist_owner','public','CREATE') AS owner_create,has_schema_privilege('ra_persist_caller','public','CREATE') AS caller_create,has_schema_privilege('ra_persist_denied','public','CREATE') AS denied_create",
		[]string{"owner_create", "caller_create", "denied_create"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}, []resultsNativeColumn{resultsNullableMetadata, resultsNullableMetadata, resultsNullableMetadata})
	attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
	attributesBoth(t, p, b, c, "SELECT proname::text AS name,provolatile::text AS volatility,proisstrict OR false AS strict,prosecdef OR false AS definer,proparallel::text AS parallel,procost+0::real AS cost,prorows+0::real AS rows,proconfig::text AS config FROM pg_proc WHERE proname IN ('ra_persist_sql','ra_persist_standard','ra_persist_pl','ra_persist_set','ra_persist_variadic','ra_persist_pair_fn','ra_persist_proc','ra_persist_pl_proc') ORDER BY proname",
		[]string{"name", "volatility", "strict", "definer", "parallel", "cost", "rows", "config"}, []string{"TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "FLOAT4", "FLOAT4", "TEXT"}, [][]any{
			{"ra_persist_pair_fn", "i", true, false, "s", float64(5), float64(0), "{application_name=pair}"},
			{"ra_persist_pl", "s", true, true, "u", float64(11), float64(0), "{application_name=pl}"},
			{"ra_persist_pl_proc", "v", false, true, "u", float64(100), float64(0), "{application_name=plproc}"},
			{"ra_persist_proc", "v", false, true, "u", float64(100), float64(0), "{application_name=proc}"},
			{"ra_persist_set", "s", true, true, "r", float64(13), float64(12), "{application_name=set}"},
			{"ra_persist_sql", "i", true, true, "r", float64(7), float64(0), "{application_name=persist}"},
			{"ra_persist_standard", "s", true, true, "s", float64(9), float64(0), "{application_name=standard}"},
			{"ra_persist_variadic", "i", true, false, "s", float64(3), float64(0), "{application_name=array}"},
		})
	attributesBoth(t, p, b, c, "SELECT p.prorettype=t.oid AS same_type,p.proowner='ra_persist_owner'::regrole AS same_owner FROM pg_proc p JOIN pg_type t ON t.typname='ra_persist_pair' WHERE p.proname='ra_persist_pair_fn'", []string{"same_type", "same_owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}})
	resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_persist_caller")
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	attributesIntBoth(t, p, b, c, "SELECT ra_persist_sql() AS v", 46)
	want := proceduresDiagnostic("42501", "permission denied for table ra_persist_data", 0)
	s := fixture.ErrorDiagnostic("SELECT v FROM ra_persist_data", want)
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", "ra_persist_caller")
	resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_persist_denied")
	denied := proceduresDiagnostic("42501", "permission denied for function ra_persist_sql", 0)
	// A constant NULL removes the STRICT call, but an executable call with
	// a table-column argument still requires live EXECUTE authority.
	attributesBoth(t, p, b, c, "SELECT ra_persist_sql(NULL::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
	attributesQueryError(t, p, b, c, "SELECT ra_persist_sql(1)", denied)
	for _, key := range []int{0, 1} {
		q := "SELECT ra_persist_sql(v) AS v FROM ra_persist_input WHERE k=$1::integer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		s := fixture.ErrorDiagnostic(q, denied, key)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		stmt.Close()
	}
	attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
	resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
}

func TestReferenceRoutineAttributesPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_persist_owner", "ra_persist_caller", "ra_persist_denied"})
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for i := len(backends) - 1; i >= 0; i-- {
		b := backends[i]
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			attributesPersistSetup(t, p, b, c)
			check := func(c *sql.Conn, b fixture.Backend) { attributesPersistCheck(t, p, b, c) }
			check(c, b)
			attributesPersistOrdinaryOwnerSQL(t, p, b, c)
			if b.PG {
				return
			} // Only public native controls lack a PostgreSQL counterpart.
			retained, err := c.QueryContext(p.Ctx, "SELECT ra_persist_sql(3) AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var emitted []byte
			for range 2 {
				emitted = resultsNativeReplayRound(t, p, b, c, check)
				if p.Postgres != nil {
					func() {
						pg, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pg.Close()
						resultsExec(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, "RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "GRANT USAGE ON SCHEMA public TO PUBLIC")
						// Roles are shared cluster prerequisites, never synthesized by Dump.
						for _, role := range []string{"ra_persist_owner", "ra_persist_caller", "ra_persist_denied"} {
							var n int
							if err := pg.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", role).Scan(&n); err != nil {
								t.Fatal(err)
							}
							if n == 0 {
								if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE "+role); err != nil {
									t.Fatal(err)
								}
							}
						}
						if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
							t.Fatal("unchanged attribute dump replay", err)
						}
						resultsExec(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, "RESET SESSION AUTHORIZATION", "SET check_function_bodies=true", "GRANT USAGE ON SCHEMA public TO PUBLIC")
						check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
					}()
				}
			}
			attributesMetadata(b, []string{"v"}, []string{"INT4"})(t, retained)
			if !retained.Next() {
				t.Fatal("detached row lost", retained.Err())
			}
			var v any
			if err := retained.Scan(&v); err != nil || fixture.Normalize(v) != int64(45) {
				t.Fatal(v, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			defer raw.Close()
			recorded, rec := fixture.RecordedNativePool(t, connector)
			for _, q := range []string{"CREATE ROLE ra_persist_pressure", "CREATE ROLE ra_persist_owner", "CREATE ROLE ra_persist_caller", "CREATE ROLE ra_persist_denied", "CREATE TYPE ra_persist_pressure AS (v integer)"} {
				fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec(q, 0))
			}
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			clone, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer clone.Close()
			check(clone, fixture.Backend{Recorder: rec})
			fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("UPDATE ra_persist_data SET v=99", 1))
			check(c, b)
			recoverySQL := "SELECT ra_persist_sql($1::integer) AS v"
			recoveryStmt, err := c.PrepareContext(p.Ctx, recoverySQL)
			if err != nil {
				t.Fatal(err)
			}
			defer recoveryStmt.Close()
			recoverHeld := func() {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: recoveryStmt, SQL: recoverySQL}, b.Recorder, attributesStep(b, recoverySQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(45)}}, 3))
			}
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			want := proceduresDiagnostic("22023", "COST must be positive", 0)
			bad := before.String() + ";CREATE TABLE ra_restore_prefix(v integer);ALTER FUNCTION ra_persist_sql(integer) COST 0;"
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			check(c, b)
			proceduresCount(t, p, b, c, "pg_class WHERE relname='ra_restore_prefix'", 0)
			recoverHeld()
			failure := errors.New("attribute restore read failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader("CREATE TABLE ra_restore_prefix(v integer);"), resultsFailReader{failure})); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			check(c, b)
			recoverHeld()
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			reader := &attributesCancelRestoreReader{prefix: []byte("CREATE TABLE ra_restore_prefix(v integer);"), rest: bytes.NewReader(emitted), cancel: cancel}
			if err := stuntgres.Restore(ctx, p.Native, reader); !errors.Is(err, context.Canceled) {
				t.Fatal("admitted canceled Restore", err)
			}
			if !reader.entered {
				t.Fatal("Restore never consumed the admitted prefix")
			}
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			check(c, b)
			proceduresCount(t, p, b, c, "pg_class WHERE relname='ra_restore_prefix'", 0)
			recoverHeld()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed Restore changed incumbent attributes/data")
			}
			attributesPersistOwnerStreams(t, p, b, c, before.Bytes(), recoverHeld)
		})
	}
}

// Owner statements are ordinary SQL; only actual table transfers consume
// private pair authority. An omitted transfer leaves an administrator-created
// object owned by its creator, exactly as the same script does in PostgreSQL.
func attributesPersistOrdinaryOwnerSQL(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	stream := "SET SESSION AUTHORIZATION DEFAULT;" +
		"CREATE FUNCTION public.ra_stream_owner(x integer) RETURNS integer LANGUAGE SQL AS 'SELECT x';" +
		"CREATE FUNCTION public.ra_stream_owner(x text) RETURNS text LANGUAGE SQL AS 'SELECT x';" +
		"ALTER FUNCTION public.ra_stream_owner(integer) OWNER TO ra_persist_owner;" +
		"ALTER FUNCTION public.ra_stream_owner(integer) OWNER TO ra_persist_owner;" +
		"ALTER FUNCTION public.ra_stream_owner(text) OWNER TO ra_persist_caller;" +
		"CREATE FUNCTION public.ra_stream_unowned() RETURNS integer LANGUAGE SQL AS 'SELECT 9';" +
		"SET SESSION AUTHORIZATION ra_persist_owner;SET SESSION AUTHORIZATION DEFAULT;" +
		"CREATE TABLE public.ra_stream_table_unowned(v integer);" +
		"CREATE TABLE public.ra_stream_table_after(v integer);" +
		"SET SESSION AUTHORIZATION ra_persist_denied;"
	if b.PG {
		if _, err := c.ExecContext(p.Ctx, stream); err != nil {
			t.Fatal("ordinary routine ownership SQL", err)
		}
		defer resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "DROP FUNCTION ra_stream_owner(integer)", "DROP FUNCTION ra_stream_owner(text)", "DROP FUNCTION ra_stream_unowned()", "DROP TABLE ra_stream_table_unowned,ra_stream_table_after")
	} else {
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/ordinary-owner")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		for _, role := range []string{"ra_persist_owner", "ra_persist_caller", "ra_persist_denied"} {
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		c, err = recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		b = fixture.Backend{Recorder: recorder}
		// The stream ends as a different actor. Restore must not replace the
		// caller connection's bootstrap or nonbootstrap authorization/settings.
		for _, prior := range []string{"DEFAULT", "ra_persist_caller"} {
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION "+prior, "SET application_name='incumbent'")
			var actor string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&actor); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(stream)); err != nil {
				t.Fatal("ordinary routine ownership Restore", err)
			}
			attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", actor)
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "incumbent")
		}
	}
	resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	attributesBoth(t, p, b, c,
		"SELECT p.oid='ra_stream_owner(integer)'::regprocedure AS integer_input,p.proowner::regrole::text AS owner FROM pg_proc p WHERE p.proname='ra_stream_owner' ORDER BY p.proowner::regrole::text",
		[]string{"integer_input", "owner"}, []string{"BOOL", "TEXT"}, [][]any{{false, "ra_persist_caller"}, {true, "ra_persist_owner"}})
	attributesBoth(t, p, b, c, "SELECT proowner::regrole::text=session_user::text AS authenticated_owner FROM pg_proc WHERE proname='ra_stream_unowned'",
		[]string{"authenticated_owner"}, []string{"BOOL"}, [][]any{{true}})
	attributesBoth(t, p, b, c, "SELECT relowner::regrole::text=session_user::text AS authenticated_owner FROM pg_class WHERE relname IN ('ra_stream_table_unowned','ra_stream_table_after')",
		[]string{"authenticated_owner"}, []string{"BOOL"}, [][]any{{true}, {true}})
}

func attributesPersistOwnerStreams(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, before []byte, recoverHeld func()) {
	t.Helper()
	pair := "SET SESSION AUTHORIZATION ra_persist_owner;SET SESSION AUTHORIZATION DEFAULT;"
	create := "CREATE TABLE public.ra_stream_table(v integer);"
	owner := "ALTER TABLE public.ra_stream_table OWNER TO ra_persist_owner;"
	boundary := proceduresDiagnostic("0A000", "restore accepts only immediately paired table declaration ownership", 0)
	cases := []struct {
		name, sql string
		want      fixture.Diagnostic
	}{
		{"reordered_owner", pair + create + "CREATE TABLE public.ra_stream_interloper(v integer);" + owner, boundary},
		{"duplicate_owner", pair + create + owner + owner, boundary},
		{"neighbor_target", pair + create + "ALTER TABLE public.ra_persist_input OWNER TO ra_persist_owner;", boundary},
		{"wrong_owner", pair + create + "ALTER TABLE public.ra_stream_table OWNER TO ra_persist_caller;", boundary},
		{"nonfresh_constructor", pair + "CREATE TABLE IF NOT EXISTS public.ra_persist_input(k integer,v integer);ALTER TABLE public.ra_persist_input OWNER TO ra_persist_owner;", boundary},
		{"owner_before_constructor", pair + owner + create, boundary},
		{"wrong_routine_signature", "ALTER FUNCTION public.ra_persist_overload(boolean) OWNER TO ra_persist_denied;",
			proceduresDiagnostic("42883", "function public.ra_persist_overload(boolean) does not exist", 0)},
		{"wrong_routine_kind", "ALTER PROCEDURE public.ra_persist_overload(integer) OWNER TO ra_persist_denied;",
			proceduresDiagnostic("42809", "public.ra_persist_overload(integer) is not a procedure", 0)},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise incumbent bootstrap and nonbootstrap physical sessions.
			prior := "DEFAULT"
			if i%2 != 0 {
				prior = "ra_persist_caller"
			}
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION "+prior)
			var actor string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&actor); err != nil {
				t.Fatal(err)
			}
			stream := string(before) + "SET SESSION AUTHORIZATION DEFAULT;CREATE TABLE public.ra_stream_prefix(v integer);" + tc.sql
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(stream)), &tc.want)
			attributesTextBoth(t, p, b, c, "SELECT current_user::text AS v", actor)
			attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			proceduresCount(t, p, b, c, "pg_class WHERE relname IN ('ra_stream_table','ra_stream_prefix')", 0)
			resultsStoredBoth(t, p, b, c, "SELECT v AS v FROM ra_persist_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
			recoverHeld()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after.Bytes()) {
				t.Fatal("failed owner stream changed incumbent catalog, ownership, ACL or data")
			}
		})
	}
}

// Cancel only after Restore has actually consumed a nonempty stream prefix.
// No worker or blocking reader is left behind on a failure path.
type attributesCancelRestoreReader struct {
	prefix  []byte
	rest    io.Reader
	cancel  context.CancelFunc
	entered bool
}

func (r *attributesCancelRestoreReader) Read(dst []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(dst, r.prefix)
		r.prefix = r.prefix[n:]
		r.entered = true
		return n, nil
	}
	r.cancel()
	return r.rest.Read(dst)
}
