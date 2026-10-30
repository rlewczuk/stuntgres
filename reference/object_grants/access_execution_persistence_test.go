package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

type accessExecutionReadFailure struct{}

func (accessExecutionReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("access execution reader failure")
}

type accessExecutionWriteFailure struct{}

func (accessExecutionWriteFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func accessExecutionPersistCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, recorder, s) }
	for _, o := range []struct{ function, name, catalog, key string }{
		{"has_function_privilege", "public.ae_f(integer)", "pg_proc", "oid='public.ae_f(integer)'::regprocedure"},
		{"has_function_privilege", "public.ae_proc()", "pg_proc", "oid='public.ae_proc()'::regprocedure"},
		{"has_language_privilege", "sql", "pg_language", "lanname='sql'"},
		{"has_foreign_data_wrapper_privilege", "ae_fdw", "pg_foreign_data_wrapper", "fdwname='ae_fdw'"},
		{"has_server_privilege", "ae_srv", "pg_foreign_server", "srvname='ae_srv'"},
	} {
		privilege := "USAGE"
		if o.function == "has_function_privilege" {
			privilege = "EXECUTE"
		}
		run(objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", true, o.name, privilege))
		run(objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',oid,$1::text) AS v FROM "+o.catalog+" WHERE "+o.key, true, privilege))
	}
	run(fixture.Select("SELECT proname,pg_get_acl(1255,oid,0) IS NULL AS absent FROM pg_proc WHERE proname IN('ae_null','ae_empty','ae_equivalent') ORDER BY proname", []string{"proname", "absent"}, []string{"NAME", "BOOL"}, [][]any{{"ae_empty", false}, {"ae_equivalent", false}, {"ae_null", true}}))
	run(fixture.Select("SELECT pg_get_acl(1255,oid,0)::text AS acl FROM pg_proc WHERE proname='ae_empty'", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}}))
	equivalent := fixture.Select("SELECT unnest(pg_get_acl(1255,oid,0))::text AS acl FROM pg_proc WHERE proname='ae_equivalent'", []string{"acl"}, []string{"TEXT"}, [][]any{{"=X/postgres"}, {"postgres=X/postgres"}})
	equivalent.Bag = true
	run(equivalent)
	run(fixture.Select("SELECT proname,proowner='postgres'::regrole AS owned,has_function_privilege('og_object_reader','public.'||proname::text||'()','EXECUTE') AS named,has_function_privilege('og_object_reader',oid,'EXECUTE') AS numbered,has_function_privilege('og_object_reader','public.'||proname::text||'()','EXECUTE WITH GRANT OPTION') AS named_option,has_function_privilege('og_object_reader',oid,'EXECUTE WITH GRANT OPTION') AS numbered_option FROM pg_proc WHERE proname IN('ae_null','ae_empty','ae_equivalent') ORDER BY proname", []string{"proname", "owned", "named", "numbered", "named_option", "numbered_option"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"ae_empty", true, false, false, false, false}, {"ae_equivalent", true, true, true, false, false}, {"ae_null", true, true, true, false, false}}))
	s := fixture.Select("SELECT unnest(pg_get_acl(1255,oid,0))::text AS acl FROM pg_proc WHERE oid='public.ae_f(integer)'::regprocedure", []string{"acl"}, []string{"TEXT"}, [][]any{{"postgres=X/postgres"}, {"og_object_other=X*/postgres"}, {"og_object_reader=X/og_object_other"}})
	s.Bag = true
	run(s)
	run(objectGrantAccessValue("SELECT lanowner='og_object_other'::regrole AS v FROM pg_language WHERE lanname='sql'", true))
	run(objectGrantAccessValue("SELECT has_language_privilege('og_object_other','sql','USAGE WITH GRANT OPTION') AS v", true))
	// Procedure identity and EXECUTE authority roundtrip independently of
	// CALL output/effect persistence in query_outer.
	run(fixture.Select("SELECT prokind::text AS kind,proowner='og_object_other'::regrole AS owned FROM pg_proc WHERE oid='public.ae_proc()'::regprocedure", []string{"kind", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"p", true}}))
	procedure := fixture.Select("SELECT unnest(pg_get_acl(1255,'public.ae_proc()'::regprocedure,0))::text AS acl", []string{"acl"}, []string{"TEXT"}, [][]any{{"og_object_other=X/og_object_other"}, {"og_object_reader=X*/og_object_other"}, {"postgres=X/og_object_reader"}})
	procedure.Bag = true
	run(procedure)
	for _, privilege := range []string{"EXECUTE", "EXECUTE WITH GRANT OPTION"} {
		run(fixture.Select("SELECT has_function_privilege(r.rolname,'public.ae_proc()',$1::text) AS nn,has_function_privilege(r.rolname,p.oid,$1::text) AS no,has_function_privilege(r.oid,'public.ae_proc()',$1::text) AS oname,has_function_privilege(r.oid,p.oid,$1::text) AS oo FROM (SELECT oid FROM pg_proc WHERE oid='public.ae_proc()'::regprocedure) p CROSS JOIN (SELECT oid,rolname FROM pg_roles WHERE rolname='og_object_reader') r", []string{"nn", "no", "oname", "oo"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, privilege))
	}
	run(objectGrantAccessValue("SELECT has_function_privilege('public','public.ae_proc()','EXECUTE') AS v", false))
	for _, raw := range []struct {
		query   string
		entries [][]any
	}{
		{"SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='sql'", [][]any{{"=U/postgres"}, {"postgres=U/postgres"}, {"og_object_other=U*/postgres"}, {"og_object_reader=U/og_object_other"}}},
		{"SELECT unnest(fdwacl)::text AS acl FROM pg_foreign_data_wrapper WHERE fdwname='ae_fdw'", [][]any{{"postgres=U/postgres"}, {"og_object_other=U*/postgres"}, {"og_object_reader=U/og_object_other"}}},
		{"SELECT unnest(srvacl)::text AS acl FROM pg_foreign_server WHERE srvname='ae_srv'", [][]any{{"og_object_other=U/og_object_other"}, {"og_object_reader=U*/og_object_other"}, {"=U/og_object_reader"}}},
	} {
		step := fixture.Select(raw.query, []string{"acl"}, []string{"TEXT"}, raw.entries)
		step.Bag = true
		run(step)
	}
	run(objectGrantAccessValue("SELECT srvowner='og_object_other'::regrole AS v FROM pg_foreign_server WHERE srvname='ae_srv'", true))
	run(objectGrantAccessValue("SELECT fdwowner='postgres'::regrole AS v FROM pg_foreign_data_wrapper WHERE fdwname='ae_fdw'", true))
	run(objectGrantAccessValue("SELECT has_language_privilege('og_object_reader','sql','USAGE WITH GRANT OPTION') AS v", false))
	run(objectGrantAccessValue("SELECT has_foreign_data_wrapper_privilege('og_object_other','ae_fdw','USAGE WITH GRANT OPTION') AS v", true))
	run(objectGrantAccessValue("SELECT has_foreign_data_wrapper_privilege('og_object_reader','ae_fdw','USAGE WITH GRANT OPTION') AS v", false))
	run(objectGrantAccessValue("SELECT has_server_privilege('og_object_reader','ae_srv','USAGE WITH GRANT OPTION') AS v", true))
	run(objectGrantAccessValue("SELECT has_server_privilege(4294967294::oid,'ae_srv','USAGE') AS v", true))
	run(fixture.Select("SELECT lanname,lanacl IS NULL AS absent,lanacl::text AS acl FROM pg_language WHERE lanname IN('c','internal','plpgsql') ORDER BY lanname", []string{"lanname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"c", true, nil}, {"internal", true, nil}, {"plpgsql", false, "{}"}}))
	run(fixture.Select("SELECT fdwname,fdwacl IS NULL AS absent,fdwacl::text AS acl FROM pg_foreign_data_wrapper WHERE fdwname IN('ae_fdw_null','ae_fdw_empty') ORDER BY fdwname", []string{"fdwname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"ae_fdw_empty", false, "{}"}, {"ae_fdw_null", true, nil}}))
	run(fixture.Select("SELECT srvname,srvacl IS NULL AS absent,srvacl::text AS acl FROM pg_foreign_server WHERE srvname IN('ae_srv_null','ae_srv_empty') ORDER BY srvname", []string{"srvname", "absent", "acl"}, []string{"NAME", "BOOL", "TEXT"}, [][]any{{"ae_srv_empty", false, "{}"}, {"ae_srv_null", true, nil}}))
	for _, name := range []string{"ae_fdw_null", "ae_fdw_empty"} {
		run(objectGrantAccessValue("SELECT has_foreign_data_wrapper_privilege('og_object_reader',$1::text,'USAGE') AS v", false, name))
	}
	for _, name := range []string{"ae_srv_null", "ae_srv_empty"} {
		run(objectGrantAccessValue("SELECT has_server_privilege('og_object_reader',$1::text,'USAGE') AS v", false, name))
	}
	run(objectGrantAccessValue("SELECT has_language_privilege('og_object_reader','plpgsql','USAGE') AS v", false))
	run(fixture.Select("SELECT s.srvname,s.srvfdw=w.oid AS linked FROM (SELECT srvname,srvfdw FROM pg_foreign_server WHERE srvname IN('ae_srv','ae_srv_null','ae_srv_empty')) s JOIN pg_foreign_data_wrapper w ON w.fdwname='ae_fdw' ORDER BY s.srvname", []string{"srvname", "linked"}, []string{"NAME", "BOOL"}, [][]any{{"ae_srv", true}, {"ae_srv_empty", true}, {"ae_srv_null", true}}))
	run(fixture.Select("SELECT ae_f(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
	run(fixture.Select("SELECT ae_sum(v) AS v FROM (VALUES(1),(2)) s(v)", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
}

// Native installation replay transfers these preexisting extension members to
// its real source owner. PostgreSQL's public-schema reset cannot retire them.
func accessExecutionReplayRoutineCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	for _, identity := range []string{
		"pg_catalog.plpgsql_call_handler()",
		"pg_catalog.plpgsql_inline_handler(pg_catalog.internal)",
		"pg_catalog.plpgsql_validator(pg_catalog.oid)",
	} {
		var owner string
		var acl sql.NullString
		if err := c.QueryRowContext(p.Ctx, "SELECT proowner::regrole::text,proacl::text FROM pg_catalog.pg_proc WHERE oid=$1::regprocedure", identity).Scan(&owner, &acl); err != nil {
			t.Fatal("capture retained replay routine", identity, err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			admin, err := fixture.Admin.Conn(ctx)
			if err != nil {
				t.Error("retained replay routine admin", err)
				return
			}
			defer admin.Close()
			// This independent connection is not replay's possibly aborted or
			// reauthorized session. DDL repairs shared-role dependencies before
			// restoring exact NULL/empty ACL provenance and original grantors.
			for _, query := range []string{
				"ALTER FUNCTION " + identity + " OWNER TO " + pq.QuoteIdentifier(owner),
				"REVOKE ALL ON FUNCTION " + identity + " FROM postgres,og_object_reader,og_object_other CASCADE",
			} {
				if _, err := admin.ExecContext(ctx, query); err != nil {
					t.Error("restore retained replay routine", query, err)
				}
			}
			if _, err := admin.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2::regprocedure", acl, identity); err != nil {
				t.Error("restore retained replay routine ACL", identity, err)
			}
		})
	}
}

func TestReferenceAccessExecutionPersistence(t *testing.T) {
	var emitted string
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		if b.PG {
			accessExecutionReplayRoutineCleanup(t, p, c)
			var originalACL sql.NullString
			var originalOwner string
			if err := c.QueryRowContext(p.Ctx, "SELECT lanacl::text,lanowner::regrole::text FROM pg_language WHERE lanname='sql'").Scan(&originalACL, &originalOwner); err != nil {
				t.Fatal(err)
			}
			var originalPLACL sql.NullString
			if err := c.QueryRowContext(p.Ctx, "SELECT lanacl::text FROM pg_language WHERE lanname='plpgsql'").Scan(&originalPLACL); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := b.DB.ExecContext(ctx, "UPDATE pg_catalog.pg_language SET lanacl=$1::aclitem[] WHERE lanname='plpgsql'", originalPLACL); err != nil {
					t.Error("restore retained procedural language ACL", err)
				}
			})
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			accessExecutionCleanup(t, b, originalOwner, originalACL)
			if emitted == "" {
				t.Fatal("missing actual emitted native stream")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			accessExecutionPersistCheck(t, p, c, b.Recorder)
			return
		}
		// Consume source-local dynamic OIDs without adding persistent objects.
		for range 8 {
			run(fixture.Exec("CREATE TABLE ae_pressure(v integer)", 0))
			run(fixture.Exec("DROP TABLE ae_pressure", 0))
		}
		accessExecutionSetup(t, p, b, c, run)
		for _, q := range []string{
			"ALTER SCHEMA public OWNER TO og_object_other",
			"CREATE FUNCTION ae_null() RETURNS integer RETURN 1",
			"CREATE FUNCTION ae_empty() RETURNS integer RETURN 2",
			"CREATE FUNCTION ae_equivalent() RETURNS integer RETURN 3",
			"CREATE PROCEDURE ae_proc() LANGUAGE SQL AS 'SELECT 1'",
			"CREATE AGGREGATE ae_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')",
			"ALTER PROCEDURE ae_proc() OWNER TO og_object_other",
			"REVOKE EXECUTE ON PROCEDURE ae_proc() FROM PUBLIC",
			"GRANT EXECUTE ON PROCEDURE ae_proc() TO og_object_reader WITH GRANT OPTION",
			"SET ROLE og_object_reader",
			"GRANT EXECUTE ON PROCEDURE ae_proc() TO postgres",
			"RESET ROLE",
			"CREATE FOREIGN DATA WRAPPER ae_fdw_null NO HANDLER",
			"CREATE FOREIGN DATA WRAPPER ae_fdw_empty NO HANDLER",
			"REVOKE ALL ON FOREIGN DATA WRAPPER ae_fdw_empty FROM postgres",
			"CREATE SERVER ae_srv_null FOREIGN DATA WRAPPER ae_fdw",
			"CREATE SERVER ae_srv_empty FOREIGN DATA WRAPPER ae_fdw",
			"REVOKE ALL ON FOREIGN SERVER ae_srv_empty FROM postgres",
			"GRANT EXECUTE ON FUNCTION ae_equivalent() TO PUBLIC",
			"REVOKE EXECUTE ON FUNCTION ae_empty() FROM PUBLIC,postgres",
			"REVOKE EXECUTE ON FUNCTION ae_f(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION ae_f(integer) TO og_object_other WITH GRANT OPTION",
			"SET ROLE og_object_other",
			"GRANT EXECUTE ON FUNCTION ae_f(integer) TO og_object_reader",
			"RESET ROLE",
			"GRANT USAGE ON LANGUAGE sql TO og_object_other WITH GRANT OPTION",
			"GRANT USAGE ON FOREIGN DATA WRAPPER ae_fdw TO og_object_other WITH GRANT OPTION",
			"SET ROLE og_object_other",
			"GRANT USAGE ON LANGUAGE sql TO og_object_reader",
			"GRANT USAGE ON FOREIGN DATA WRAPPER ae_fdw TO og_object_reader",
			"RESET ROLE",
			"ALTER SERVER ae_srv OWNER TO og_object_other",
			"GRANT USAGE ON FOREIGN SERVER ae_srv TO og_object_reader WITH GRANT OPTION",
			"SET ROLE og_object_reader",
			"GRANT USAGE ON FOREIGN SERVER ae_srv TO PUBLIC",
			"RESET ROLE",
			"REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC,postgres",
		} {
			run(fixture.Exec(q, 0))
		}
		var captured bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &captured); err != nil {
			t.Fatal(err)
		}
		// Retained builtin language owner repair is a bounded native-control
		// input, not a new public ALTER LANGUAGE implementation.
		ownerRepair := "UPDATE pg_catalog.pg_language SET lanowner='og_object_other'::pg_catalog.regrole WHERE lanname='sql'::pg_catalog.name;\n"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(captured.String()+ownerRepair)); err != nil {
			t.Fatal(err)
		}
		accessExecutionPersistCheck(t, p, c, b.Recorder)
		if err := stuntgres.Dump(p.Ctx, p.Native, accessExecutionWriteFailure{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal("writer failure lost", err)
		}
		var saved bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		emitted = saved.String()
		connector, err := stuntgres.NewDriver().OpenConnector("access-execution-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		for _, q := range []string{"CREATE ROLE og_object_other", "CREATE ROLE og_object_reader", "CREATE TABLE ae_sentinel(v integer)", "INSERT INTO ae_sentinel VALUES(91)"} {
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec(q, map[bool]int64{true: 1}[strings.HasPrefix(q, "INSERT")]))
		}
		sentinel := func() {
			fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Select("SELECT v FROM ae_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
		for _, invalid := range []string{
			"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid='ae_f(integer)'::pg_catalog.regprocedure OR TRUE;",
			"UPDATE pg_catalog.pg_proc SET proacl='{og_object_reader=X/ae_missing_grantor}'::pg_catalog.aclitem[] WHERE oid='ae_f(integer)'::pg_catalog.regprocedure;",
			"UPDATE pg_catalog.pg_language SET lanacl='{og_object_reader=X/postgres}'::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name;",
		} {
			if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted+invalid)); err == nil {
				t.Fatal("invalid late repair accepted", invalid)
			}
			sentinel()
		}
		if err := stuntgres.Restore(p.Ctx, fresh, io.MultiReader(strings.NewReader(emitted), accessExecutionReadFailure{})); err == nil {
			t.Fatal("reader failure accepted")
		}
		sentinel()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, fresh, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		sentinel()
		retained, err := c.QueryContext(p.Ctx, "SELECT ae_f(4) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		inquiry := accessExecutionHeld(t, p, c, "SELECT has_function_privilege('og_object_reader',$1::text,'EXECUTE') AS v")
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		accessExecutionPersistCheck(t, p, recorded, recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != emitted {
			t.Fatal("logical dump changed after identity remap")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, false, retained, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, false)
		fixture.RunStep(t, p.Ctx, inquiry, b.Recorder, objectGrantAccessValue(inquiry.SQL, true, "ae_f(integer)"))
		run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION ae_f(integer) FROM og_object_other CASCADE", 0))
		fixture.RunStep(t, p.Ctx, inquiry, b.Recorder, objectGrantAccessValue(inquiry.SQL, false, "ae_f(integer)"))
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION ae_f(integer) FROM og_object_other CASCADE", 0))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_function_privilege('og_object_reader','ae_f(integer)','EXECUTE') AS v", false))
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON FOREIGN DATA WRAPPER ae_fdw FROM og_object_other CASCADE", 0))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_foreign_data_wrapper_privilege('og_object_reader','ae_fdw','USAGE') AS v", false))
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON FOREIGN SERVER ae_srv FROM og_object_reader CASCADE", 0))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_server_privilege('public','ae_srv','USAGE') AS v", false))
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON PROCEDURE ae_proc() FROM og_object_reader CASCADE", 0))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_function_privilege('og_object_reader','ae_proc()','EXECUTE WITH GRANT OPTION') AS v", false))
		fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT has_function_privilege('og_object_reader','ae_proc()','EXECUTE') AS v", true))
		procedureAfter := fixture.Select("SELECT unnest(pg_get_acl(1255,'public.ae_proc()'::regprocedure,0))::text AS acl", []string{"acl"}, []string{"TEXT"}, [][]any{{"og_object_other=X/og_object_other"}, {"og_object_reader=X/og_object_other"}})
		procedureAfter.Bag = true
		fixture.RunStep(t, p.Ctx, recorded, recorder, procedureAfter)
		// The repair recognizer is not an ordinary catalog UPDATE capability.
		run(fixture.Error("UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='ae_f(integer)'::pg_catalog.regprocedure", "0A000"))
	})
}

func TestReferenceAccessExecutionSmoke(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessExecutionSetup(t, p, b, c, run)
		for _, o := range objects {
			call := accessExecutionCalls(t, p, c, o)[0]
			held := accessExecutionHeld(t, p, c, call.query)
			run(fixture.Exec("REVOKE "+o.privilege+" ON "+o.target+" FROM PUBLIC", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, false, call.args...))
			run(fixture.Exec("GRANT "+o.privilege+" ON "+o.target+" TO og_object_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, true, call.args...))
			for _, variant := range accessExecutionCalls(t, p, c, o)[:4] {
				run(objectGrantAccessValue(variant.query, true, variant.args...))
			}
			run(objectGrantAccessValue("SELECT "+o.function+"(NULL::text,'bad') AS v", nil))
			run(accessExecutionError("SELECT "+o.function+"('ae_missing',$1::text,$2::text) AS v", "42704", `role "ae_missing" does not exist`, o.name, o.privilege))
		}
		if !b.PG {
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector("access-execution-smoke/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			for _, q := range []string{"CREATE ROLE og_object_other", "CREATE ROLE og_object_reader"} {
				if _, err := fresh.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, o := range objects {
				fixture.RunStep(t, p.Ctx, recorded, recorder, objectGrantAccessValue("SELECT "+o.function+"('og_object_reader',$1::text,$2::text) AS v", true, o.name, o.privilege))
				column := map[string]string{"has_function_privilege": "pg_get_acl(1255,oid,0)", "has_language_privilege": "lanacl", "has_foreign_data_wrapper_privilege": "fdwacl", "has_server_privilege": "srvacl"}[o.function]
				bits := "U"
				if o.privilege == "EXECUTE" {
					bits = "X"
				}
				step := fixture.Select("SELECT unnest("+column+")::text AS acl FROM "+o.catalog+" WHERE "+o.key, []string{"acl"}, []string{"TEXT"}, [][]any{{"postgres=" + bits + "/postgres"}, {"og_object_reader=" + bits + "/postgres"}})
				step.Bag = true
				fixture.RunStep(t, p.Ctx, recorded, recorder, step)
			}
		}
	})
}
