package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL 18 ALTER LARGE OBJECT; REL_18_6 alter.c (AlterObjectOwner_internal),
// acl.c (aclnewowner), and pg_shdepend.c (changeDependencyOnOwner).
// Expectations are source-derived until the configured oracle has observed them.
func largeObjectOwnerScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step), bool)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"lo_old", "lo_new", "lo_actor", "lo_reader", "lo_other", "lo_mid", "lo_alt", "lo_future", "Lo Mixed", "lo\"quote"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer func() {
						c.ExecContext(p.Ctx, "ROLLBACK")
						c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					}()
					run := func(s fixture.Step) {
						t.Helper()
						s.Prepared = prepared
						notices = nil
						if prepared && s.Diagnostic != nil && s.Diagnostic.Code == "25P02" {
							s.ErrorStage = "prepare"
						}
						objectGrantStep(t, p.Ctx, c, record, largeObjectBackendStep(s, b))
						if len(notices) != 0 {
							t.Fatalf("%s: unexpected notices: %#v", s.Query, notices)
						}
					}
					for _, role := range []string{"lo_old", "lo_new", "lo_actor", "lo_reader", "lo_other", "lo_mid", "lo_alt"} {
						run(fixture.Exec("CREATE ROLE "+role+" NOLOGIN", 0))
					}
					body(t, p, b, c, run, prepared)
				})
			}
		})
	}
}

func largeObjectOwnerOID(t *testing.T, p *fixture.Pair, c *sql.Conn, role string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func largeObjectOwnerAlter(oid uint32, role string) string {
	return fmt.Sprintf("ALTER LARGE OBJECT %d OWNER TO %s", oid, role)
}

func largeObjectOwnerError(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.ErrorStage = "execute"
	return s
}

func largeObjectOwnerCreate(run func(fixture.Step), oid uint32) {
	fixture.RoleGrantActor(run, "lo_old", func() {
		run(largeObjectValue(fmt.Sprintf("lo_from_bytea(%d::oid,decode('00017f80ff00','hex'))", oid), "OID", int64(oid)))
	})
}

// Unlike RoleGrantActor, preserve the authenticated SESSION_USER.
func largeObjectOwnerActor(run func(fixture.Step), role string, body func()) {
	run(fixture.Exec("SET ROLE "+role, 0))
	body()
	run(fixture.Exec("RESET ROLE", 0))
}

func largeObjectOwnerMetadata(run func(fixture.Step), oid uint32, owner int64, acl any) {
	run(largeObjectMetadataStep(int64(oid), [][]any{{int64(oid), owner, acl}}))
}

func largeObjectOwnerHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatalf("effect-free Prepare %s: %v", q, err)
	}
	t.Cleanup(func() { stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	return func(s fixture.Step) {
		t.Helper()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, largeObjectBackendStep(s, b))
	}
}

func largeObjectOwnerDropError(role, detail string) fixture.Step {
	s := largeObjectOwnerError("DROP ROLE "+role, "2BP01", "role \""+role+"\" cannot be dropped because some objects depend on it")
	s.Diagnostic.Detail = detail
	return s
}
