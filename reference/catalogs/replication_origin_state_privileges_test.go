package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceReplicationOriginStatePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"state_origin_user", "state_origin_repl"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "state_privileges"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				c.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
				for i, fn := range originStateFunctions {
					c.ExecContext(ctx, "REVOKE ALL ON FUNCTION "+fn+"("+originStateSignatures[i]+") FROM state_origin_user,state_origin_repl")
				}
				c.ExecContext(ctx, "REVOKE USAGE ON SCHEMA public FROM state_origin_user")
				c.ExecContext(ctx, "DROP ROLE state_origin_user,state_origin_repl")
			}()
			run := func(s fixture.Step) { originStateRun(t, p, b, c, s) }
			run(fixture.Exec("CREATE ROLE state_origin_user LOGIN", 0))
			run(fixture.Exec("CREATE ROLE state_origin_repl LOGIN REPLICATION", 0))
			// initdb records ACLs for its bootstrap owner, not the later login
			// role used by the fixture's SQL sessions.
			bootstrap := "postgres"
			if b.PG {
				bootstrap = "stuntgres"
			}
			initial := "SELECT count(*) AS v FROM pg_init_privs WHERE classoid=1255 AND objoid IN (6003,6004,6005,6012,6013) AND objsubid=0 AND privtype='i' AND initprivs::text=$1::text"
			initialArgs := []any{"{" + bootstrap + "=X/" + bootstrap + "}"}
			run(originStateScalar(initial, "INT8", int64(5), initialArgs...))
			run(originStateScalar("SELECT count(*) AS v FROM pg_proc WHERE oid IN (6003,6004,6005,6012,6013) AND proacl::text=$1::text", "INT8", int64(5), initialArgs...))
			for i, fn := range originStateFunctions {
				args := "$1::text"
				values := []any{name}
				typ := "OID"
				if i == 1 || i == 3 {
					typ = "VOID"
				}
				if i == 3 {
					args += ",$2::pg_lsn"
					values = append(values, "0/9")
				}
				if i == 4 {
					args += ",$2::boolean"
					values = append(values, false)
					typ = "PG_LSN"
				}
				q := "SELECT " + fn + "(" + args + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				denied := originStateError(q, "42501", "permission denied for function "+fn, "", values...)
				for _, role := range []string{"state_origin_user", "state_origin_repl"} {
					run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
					run(denied)
					originStateRun(t, p, b, held, denied)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				identity := fn + "(" + originStateSignatures[i] + ")"
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+identity+" TO state_origin_user", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION state_origin_user", 0))
				var want any
				switch i {
				case 0:
					values[0] = "state_granted_create"
					originStateCleanup(t, b.DB, "state_granted_create")
					rows, err := held.QueryContext(p.Ctx, q, values...)
					if err != nil {
						t.Fatal(err)
					}
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					var id int64
					if err := rows.Scan(&id); err != nil || id <= 0 || id >= 65535 {
						t.Fatal(id, err)
					}
					rows.Close()
				case 1:
					originStateRun(t, p, b, held, originStateScalar(q, typ, "", values...))
				case 2:
					originStateRun(t, p, b, held, originStateScalar(q, typ, nil, values...))
				default:
					// Recreate after the explicitly granted drop, as the bootstrap caller.
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					if i == 3 {
						originStateCreate(t, p, b, c, name)
					}
					run(fixture.Exec("SET SESSION AUTHORIZATION state_origin_user", 0))
					if i == 3 {
						want = ""
					} else {
						want = "0/9"
					}
					originStateRun(t, p, b, held, originStateScalar(q, typ, want, values...))
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM state_origin_user", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION state_origin_user", 0))
				denied = originStateError(q, "42501", "permission denied for function "+fn, "", values...)
				originStateRun(t, p, b, held, denied)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				stmt.Close()
			}
			// Ordinary grants/revokes change live admission, never initdb history.
			run(originStateScalar(initial, "INT8", int64(5), initialArgs...))
			// The fixture login's own schema precedes public; role switching
			// must not change which table supplies the runtime NULL.
			run(fixture.Exec("CREATE TABLE public.state_runtime_nulls(name text)", 0))
			run(fixture.Exec("INSERT INTO public.state_runtime_nulls VALUES(NULL)", 1))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO state_origin_user", 0))
			run(fixture.Exec("GRANT SELECT ON public.state_runtime_nulls TO state_origin_user", 0))
			run(originStateScalar("SELECT pg_get_userbyid(relowner)=current_user AS v FROM pg_class WHERE oid='public.state_runtime_nulls'::regclass", "BOOL", true))
			run(fixture.Exec("SET SESSION AUTHORIZATION state_origin_user", 0))
			run(originStateScalar("SELECT name AS v FROM public.state_runtime_nulls", "TEXT", nil))
			// Planner-elided constant NULL differs from a surviving row-dependent call.
			run(originStateScalar("SELECT pg_replication_origin_progress(NULL,true) AS v", "PG_LSN", nil))
			run(originStateError("SELECT pg_replication_origin_progress(name,false) FROM public.state_runtime_nulls", "42501", "permission denied for function pg_replication_origin_progress", ""))
			for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
				run(fixture.Exec("SET plan_cache_mode="+mode, 0))
				q := "SELECT pg_replication_origin_oid($1::text) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := originStateScalar(q, "OID", nil, nil)
				if mode == "force_generic_plan" {
					s = originStateError(q, "42501", "permission denied for function pg_replication_origin_oid", "", nil)
				}
				originStateRun(t, p, b, held, s)
				stmt.Close()
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("RESET plan_cache_mode", 0))
			run(fixture.Exec("DROP TABLE public.state_runtime_nulls", 0))
			if !b.PG {
				originStateACLControls(t, p, b)
			}
		})
	}
}
func originStateACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var pristine bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &pristine); err != nil {
		t.Fatal(err)
	}
	for _, fn := range originStateFunctions {
		if strings.Contains(pristine.String(), "WHERE oid=E'pg_catalog."+fn+"(") {
			t.Fatal("pristine ACL emitted repair", fn)
		}
	}
	for _, suffix := range []string{
		"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_replication_origin_oid(text)'::pg_catalog.regprocedure;\n",
		"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_replication_origin_oid(text)'::pg_catalog.regprocedure OR TRUE;\n",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pristine.String()+suffix)); err == nil {
			t.Fatal("noncanonical ACL repair published")
		}
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pristine.Bytes(), unchanged.Bytes()) {
			t.Fatal("rejected ACL repair changed destination")
		}
	}
	// Use the exact bounded shapes emitted by Dump. Empty ACLs use
	// the lossless constructor, not the rejected noncanonical '{}' input.
	for _, raw := range []string{"NULL", `pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)`} {
		var script strings.Builder
		script.WriteString(pristine.String())
		for i, fn := range originStateFunctions {
			script.WriteString("UPDATE pg_catalog.pg_proc SET proacl=" + raw + "::pg_catalog.aclitem[] WHERE oid=E'pg_catalog." + fn + "(" + originStateSignatures[i] + ")'::pg_catalog.regprocedure;\n")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script.String())); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), "SET proacl="+raw+"::pg_catalog.aclitem[]") != len(originStateFunctions) {
			t.Fatal("raw origin ACL repairs omitted or duplicated", raw)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, oid := range []int64{6003, 6004, 6005, 6012, 6013} {
			var acl sql.NullString
			if err := p.Native.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1::oid", oid).Scan(&acl); err != nil || raw == "NULL" && acl.Valid || raw != "NULL" && (!acl.Valid || acl.String != "{}") {
				t.Fatal(oid, raw, acl, err)
			}
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(pristine.Bytes())); err != nil {
		t.Fatal(err)
	}
	originStateRun(t, p, b, b.DB, fixture.Exec("GRANT EXECUTE ON FUNCTION pg_replication_origin_progress(text,boolean) TO state_origin_user", 0))
	var changed bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &changed); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(changed.Bytes())); err != nil {
		t.Fatal(err)
	}
	originStateRun(t, p, b, b.DB, originStateScalar("SELECT has_function_privilege('state_origin_user','pg_replication_origin_progress(text,boolean)','EXECUTE') AS v", "BOOL", true))
	originStateRun(t, p, b, b.DB, fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_replication_origin_progress(text,boolean) FROM state_origin_user", 0))
}
