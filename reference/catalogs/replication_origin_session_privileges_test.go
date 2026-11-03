package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationOriginSessionPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"session_origin_user", "session_origin_repl"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "session_privileges"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			c := originSessionConn(t, p, b.DB)
			run := func(s fixture.Step) { originStateRun(t, p, b, c, s) }
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				c.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			})
			run(fixture.Exec("CREATE ROLE session_origin_user LOGIN", 0))
			run(fixture.Exec("CREATE ROLE session_origin_repl LOGIN REPLICATION", 0))
			bootstrap := "postgres"
			if b.PG {
				bootstrap = "stuntgres"
			}
			for _, q := range []string{
				"SELECT count(*) AS v FROM pg_init_privs WHERE classoid=1255 AND objoid BETWEEN 6006 AND 6011 AND objsubid=0 AND privtype='i' AND initprivs::text=$1::text",
				"SELECT count(*) AS v FROM pg_proc WHERE oid BETWEEN 6006 AND 6011 AND proacl::text=$1::text",
			} {
				run(originStateScalar(q, "INT8", int64(6), "{"+bootstrap+"=X/"+bootstrap+"}"))
			}
			args := []string{"$1::text", "", "", "false", "'0/1'::pg_lsn,'2022-03-04+00'::timestamptz", ""}
			for i, fn := range originSessionFunctions {
				var selected bool
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_session_is_setup()").Scan(&selected); err != nil {
					t.Fatal(err)
				}
				if selected {
					run(fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
				}
				if i > 0 && i < 5 {
					originSessionSetup(t, p, b, c, name)
				}
				q := "SELECT " + fn + "(" + args[i] + ") AS v"
				values := []any{}
				if i == 0 {
					values = append(values, name)
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				denied := originStateError(q, "42501", "permission denied for function "+fn, "", values...)
				for _, role := range []string{"session_origin_user", "session_origin_repl"} {
					run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
					run(denied)
					originStateRun(t, p, b, held, denied)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				identity := fn + "(" + originSessionSignatures[i] + ")"
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+identity+" TO session_origin_user", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION session_origin_user", 0))
				typ := []string{"VOID", "VOID", "BOOL", "PG_LSN", "VOID", "VOID"}[i]
				var want any = ""
				if i == 2 {
					want = true
				}
				if i == 3 {
					want = nil
				}
				originStateRun(t, p, b, held, originStateScalar(q, typ, want, values...))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM session_origin_user", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION session_origin_user", 0))
				originStateRun(t, p, b, held, denied)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+identity+" TO session_origin_user", 0))
				// Restore state for mutating calls, then the same held statement observes
				// re-granted authority rather than a prepare-time authorization snapshot.
				if i == 0 {
					run(fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
				}
				if i == 1 {
					originSessionSetup(t, p, b, c, name)
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION session_origin_user", 0))
				originStateRun(t, p, b, held, originStateScalar(q, typ, want, values...))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				stmt.Close()
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM session_origin_user", 0))
			}
			run(fixture.Exec("CREATE TABLE public.session_origin_runtime_nulls(v pg_lsn)", 0))
			run(fixture.Exec("INSERT INTO public.session_origin_runtime_nulls VALUES(NULL)", 1))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO session_origin_user", 0))
			run(fixture.Exec("GRANT SELECT ON public.session_origin_runtime_nulls TO session_origin_user", 0))
			for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
				run(fixture.Exec("SET plan_cache_mode="+mode, 0))
				q := "SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				run(fixture.Exec("SET SESSION AUTHORIZATION session_origin_user", 0))
				s := originStateScalar(q, "VOID", nil, nil, "2022-03-04+00")
				if mode == "force_generic_plan" {
					s = originStateError(q, "42501", "permission denied for function pg_replication_origin_xact_setup", "", nil, "2022-03-04+00")
				}
				originStateRun(t, p, b, held, s)
				run(originStateScalar("SELECT pg_replication_origin_xact_setup(NULL::pg_lsn,'2022-03-04+00') AS v", "VOID", nil))
				run(originStateError("SELECT pg_replication_origin_xact_setup(v,'2022-03-04+00') FROM public.session_origin_runtime_nulls", "42501", "permission denied for function pg_replication_origin_xact_setup", ""))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				stmt.Close()
			}
			run(fixture.Exec("RESET plan_cache_mode", 0))
			run(fixture.Exec("DROP TABLE public.session_origin_runtime_nulls", 0))
			run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM session_origin_user", 0))
			var selected bool
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_session_is_setup()").Scan(&selected); err != nil {
				t.Fatal(err)
			}
			if selected {
				run(fixture.Exec("SELECT pg_replication_origin_session_reset()", 1))
			}
			if !b.PG {
				originSessionACLControls(t, p, b)
			}
			run(fixture.Exec("DROP ROLE session_origin_user,session_origin_repl", 0))
		})
	}
}
func originSessionACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var pristine bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &pristine); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"NULL", `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`} {
		var script strings.Builder
		script.Write(pristine.Bytes())
		for i, fn := range originSessionFunctions {
			script.WriteString("UPDATE pg_catalog.pg_proc SET proacl=" + raw + "::pg_catalog.aclitem[] WHERE oid=E'pg_catalog." + fn + "(" + originSessionSignatures[i] + ")'::pg_catalog.regprocedure;\n")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script.String())); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), "SET proacl="+raw+"::pg_catalog.aclitem[]") != 6 {
			t.Fatal("session ACL repair roster")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for oid := 6006; oid <= 6011; oid++ {
			var acl sql.NullString
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", oid).Scan(&acl); err != nil || raw == "NULL" && acl.Valid || raw != "NULL" && (!acl.Valid || acl.String != "{}") {
				t.Fatal(oid, acl, err)
			}
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(pristine.Bytes())); err != nil {
		t.Fatal(err)
	}
	for i, fn := range originSessionFunctions {
		originStateRun(t, p, b, b.DB, fixture.Exec("GRANT EXECUTE ON FUNCTION "+fn+"("+originSessionSignatures[i]+") TO session_origin_user", 0))
	}
	var granted bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &granted); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(granted.Bytes())); err != nil {
		t.Fatal(err)
	}
	for i, fn := range originSessionFunctions {
		identity := fn + "(" + originSessionSignatures[i] + ")"
		originStateRun(t, p, b, b.DB, originStateScalar("SELECT has_function_privilege('session_origin_user',$1::text,'EXECUTE') AS v", "BOOL", true, identity))
		originStateRun(t, p, b, b.DB, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM session_origin_user", 0))
	}
}
