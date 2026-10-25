package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapACLProvenance(t *testing.T) {
	for _, mode := range []struct { name string; prepared bool }{{"direct", false}, {"prepared", true}} {
		t.Run(mode.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"heap_acl_holder", "heap_acl_third", "heap_acl_public"})
			heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, source *sql.Conn) {
				heapInstall(t, p, b, source)
				run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, source, b.Recorder, step) }
				for _, name := range []string{"heap_acl_holder", "heap_acl_third", "heap_acl_public"} { run(fixture.Exec("CREATE ROLE "+name, 0)) }
				// Native's canonical public namespace already allows PUBLIC USAGE;
				// the PostgreSQL fixture resets it without that default grant.
				if b.PG { run(fixture.Exec("GRANT USAGE ON SCHEMA public TO heap_acl_holder,heap_acl_third,heap_acl_public", 0)) }
				var owner string
				if err := source.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil { t.Fatal(err) }
				target := "public.verify_heapam"+heapSignature
				actor := func(backend fixture.Backend, c *sql.Conn, role string, body func()) {
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET ROLE "+role, 0))
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil { t.Error("reset ACL provenance actor", err) }
					}()
					body()
				}
				restrict := func(backend fixture.Backend, c *sql.Conn) {
					d := heapDiagnostic("2BP01", "dependent privileges exist")
					d.Hint = "Use CASCADE to revoke them too."
					step := fixture.ErrorDiagnostic("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION "+target+" FROM heap_acl_holder RESTRICT", d)
					step.Prepared = mode.prepared
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
				}
				check := func(backend fixture.Backend, c *sql.Conn, phase string) {
					rows := [][]any{{owner, "heap_acl_holder", "NO"}}
					if phase == "chain" { rows = [][]any{{owner, "heap_acl_holder", "YES"}, {"heap_acl_holder", "heap_acl_third", "NO"}} }
					if phase == "public" { rows = append(rows, []any{owner, "PUBLIC", "NO"}) }
					step := fixture.Select("SELECT grantor::text,grantee::text,is_grantable::text FROM information_schema.routine_privileges WHERE routine_schema='public' AND routine_name='verify_heapam' AND grantee IN ('heap_acl_holder','heap_acl_third','heap_acl_public','PUBLIC')", []string{"grantor", "grantee", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT"}, rows)
					step.Bag, step.Prepared = true, mode.prepared
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
					healthy := heapEmpty("SELECT * FROM public.verify_heapam('public.heap_t')")
					healthy.Prepared = mode.prepared
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, healthy)
					for _, role := range []string{"heap_acl_holder", "heap_acl_third", "heap_acl_public"} {
						actor(backend, c, role, func() {
							query := "SELECT * FROM public.verify_heapam('public.heap_t')"
							step := heapEmpty(query)
							if role == "heap_acl_third" && phase == "cascade" || role == "heap_acl_public" && phase != "public" { step = heapError(query, heapDiagnostic("42501", "permission denied for function verify_heapam")) }
							step.Prepared = mode.prepared
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
						})
					}
				}
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO heap_acl_holder WITH GRANT OPTION", 0))
				actor(b, source, "heap_acl_holder", func() { run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO heap_acl_third", 0)) })
				check(b, source, "chain")
				if !b.PG {
					var snapshot bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil || snapshot.Len() == 0 { t.Fatal("nonowner grantor Dump", err, snapshot.Len()) }
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name()+"/acl-destination")
					if err != nil { t.Fatal(err) }
					destination := sql.OpenDB(connector)
					defer destination.Close()
					recorded, recorder := fixture.RecordedNativePool(t, connector)
					c, err := recorded.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer c.Close()
					dest := b
					dest.DB, dest.Recorder = recorded, recorder
					destRun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, step) }
					// A separate engine owns these roles; allocate an unrelated role
					// first and recreate the downstream role before replay.
					for _, query := range []string{"CREATE ROLE heap_acl_spacer", "CREATE ROLE heap_acl_third", "CREATE ROLE heap_acl_public", "CREATE TABLE heap_acl_sentinel(v INT)", "INSERT INTO heap_acl_sentinel VALUES(29)", "CREATE EXTENSION amcheck", "GRANT EXECUTE ON FUNCTION "+target+" TO heap_acl_spacer", "CREATE TEMP TABLE heap_acl_retained(v INT)", "INSERT INTO heap_acl_retained VALUES(83)"} {
						if _, err := c.ExecContext(p.Ctx, query); err != nil { t.Fatal(query, err) }
					}
					retained := heapTemporaryIdentities(t, p, c)
					var memberOID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='verify_heapam'").Scan(&memberOID); err != nil || memberOID <= 0 { t.Fatal("destination member identity", memberOID, err) }
					missing := func(role string) {
						if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err == nil { t.Fatal("missing ACL role accepted", role) }
						destRun(fixture.Select("SELECT v FROM heap_acl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(29)}}))
						destRun(fixture.Select("SELECT oid::bigint FROM pg_proc WHERE proname='verify_heapam'", []string{"oid"}, []string{"INT8"}, [][]any{{memberOID}}))
						actor(dest, c, "heap_acl_spacer", func() {
							step := heapEmpty("SELECT * FROM public.verify_heapam('public.heap_acl_sentinel')")
							step.Prepared = mode.prepared
							destRun(step)
						})
						if got := heapTemporaryIdentities(t, p, c); !reflect.DeepEqual(got, retained) { t.Fatal("failed ACL import changed retained TEMP identities", got, retained) }
						destRun(fixture.Select("SELECT v FROM pg_temp.heap_acl_retained", []string{"v"}, []string{"INT4"}, [][]any{{int64(83)}}))
					}
					missing("heap_acl_holder") // Required as both grant-option holder and nonowner grantor.
					destRun(fixture.Exec("DROP ROLE heap_acl_third", 0))
					destRun(fixture.Exec("CREATE ROLE heap_acl_holder", 0))
					missing("heap_acl_third") // Required solely as the downstream grantee.
					destRun(fixture.Exec("CREATE ROLE heap_acl_third", 0))
					if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil { t.Fatal("independent role-domain ACL replay", err) }
					check(dest, c, "chain")
					restrict(dest, c)
					check(dest, c, "chain")
					destRun(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION "+target+" FROM heap_acl_holder CASCADE", 0))
					check(dest, c, "cascade")
					destRun(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
					check(dest, c, "public")
					var public bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, destination, &public); err != nil || public.Len() == 0 { t.Fatal("PUBLIC ACL Dump", err, public.Len()) }
					if err := stuntgres.Reset(p.Ctx, destination); err != nil { t.Fatal(err) }
					if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(public.Bytes())); err != nil { t.Fatal("PUBLIC ACL Restore", err) }
					check(dest, c, "public")
					destRun(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					check(dest, c, "cascade")
					var denied bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, destination, &denied); err != nil || denied.Len() == 0 { t.Fatal("revoked PUBLIC ACL Dump", err, denied.Len()) }
					if err := stuntgres.Reset(p.Ctx, destination); err != nil { t.Fatal(err) }
					if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(denied.Bytes())); err != nil { t.Fatal("revoked PUBLIC ACL Restore", err) }
					check(dest, c, "cascade")
				}
				// Shared SQL establishes PostgreSQL's actual grantor/option bag and
				// cascading semantics independently of the native metadata codec.
				restrict(b, source)
				check(b, source, "chain")
				run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION "+target+" FROM heap_acl_holder CASCADE", 0))
				check(b, source, "cascade")
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC", 0))
				check(b, source, "public")
			})
		})
	}
}
