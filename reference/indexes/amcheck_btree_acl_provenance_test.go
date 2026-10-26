package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each slot has its own scenario so that grants on sibling overloads cannot
// accidentally authorize a replayed member with the wrong slot/signature.
func TestReferenceAmcheckBtreeACLProvenance(t *testing.T) {
	for _, overload := range amcheckOverloads {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_%d/prepared_%t", overload.name, overload.arguments, prepared), func(t *testing.T) {
				roles := []string{"bt_acl_holder", "bt_acl_third", "bt_acl_public"}
				fixture.RoleReferenceCleanup(t, roles)
				heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, source *sql.Conn) {
					run := func(backend fixture.Backend, c *sql.Conn, step fixture.Step) {
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
					}
					for _, query := range []string{"CREATE EXTENSION amcheck", "CREATE TABLE bt_acl_heap(n INT)", "INSERT INTO bt_acl_heap VALUES(2),(1),(NULL)", "CREATE INDEX bt_acl_idx ON bt_acl_heap(n)"} {
						affected := int64(0)
						if strings.HasPrefix(query, "INSERT") {
							affected = 3
						}
						run(b, source, fixture.Exec(query, affected))
					}
					for _, role := range roles {
						run(b, source, fixture.Exec("CREATE ROLE "+role, 0))
					}
					if b.PG {
						run(b, source, fixture.Exec("GRANT USAGE ON SCHEMA public TO bt_acl_holder,bt_acl_third,bt_acl_public", 0))
					}
					var owner string
					if err := source.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
						t.Fatal(err)
					}
					signature := "(regclass" + strings.Repeat(",boolean", overload.arguments-1) + ")"
					target := "public." + overload.name + signature
					actor := func(backend fixture.Backend, c *sql.Conn, role string, body func()) {
						run(backend, c, fixture.Exec("SET ROLE "+role, 0))
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
							defer cancel()
							if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
								t.Error("reset Btree ACL actor", err)
							}
						}()
						body()
					}
					check := func(backend fixture.Backend, c *sql.Conn, phase string) {
						var grants [][]any
						if phase != "empty" {
							grants = [][]any{{owner, "bt_acl_holder", "NO"}}
						}
						if phase == "chain" {
							grants = [][]any{{owner, "bt_acl_holder", "YES"}, {"bt_acl_holder", "bt_acl_third", "NO"}}
						}
						if phase == "public" {
							grants = append(grants, []any{owner, "PUBLIC", "NO"})
						}
						step := fixture.Select("SELECT grantor::text,grantee::text,is_grantable::text FROM information_schema.routine_privileges WHERE routine_schema='public' AND routine_name='"+overload.name+"' AND grantee IN ('bt_acl_holder','bt_acl_third','bt_acl_public','PUBLIC')", []string{"grantor", "grantee", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT"}, grants)
						step.Bag, step.Prepared = true, prepared
						run(backend, c, step)
						if phase == "empty" {
							var oid int64
							if err := c.QueryRowContext(p.Ctx, fmt.Sprintf("SELECT oid::bigint FROM pg_proc WHERE proname='%s' AND pronargs=%d", overload.name, overload.arguments)).Scan(&oid); err != nil {
								t.Fatal("explicit-empty member identity", err)
							}
							step := fixture.Select("SELECT grantor::text,grantee::text,is_grantable::text FROM information_schema.routine_privileges WHERE specific_schema='public' AND specific_name=$1", []string{"grantor", "grantee", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT"}, nil, fmt.Sprintf("%s_%d", overload.name, oid))
							step.Prepared = prepared
							run(backend, c, step)
						}
						for _, role := range roles {
							actor(backend, c, role, func() {
								for _, candidate := range amcheckOverloads {
									query := "SELECT public." + candidate.name + "('public.bt_acl_idx'::regclass" + strings.Repeat(",true", candidate.arguments-1) + ") AS ok"
									selected := candidate.name == overload.name && candidate.arguments == overload.arguments
									allowed := selected && (phase == "public" || phase == "chain" && role != "bt_acl_public" || phase == "cascade" && role == "bt_acl_holder")
									step := amcheckSelect(query, false)
									if !allowed {
										step = heapError(query, heapDiagnostic("42501", "permission denied for function "+candidate.name))
									}
									step.Prepared = prepared
									run(backend, c, step)
								}
							})
						}
					}
					control := func(db *sql.DB) {
						var snapshot bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, db, &snapshot); err != nil || snapshot.Len() == 0 {
							t.Fatal("Btree ACL Dump", err, snapshot.Len())
						}
						if err := stuntgres.Reset(p.Ctx, db); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(snapshot.Bytes())); err != nil {
							t.Fatal("Btree ACL Restore", err)
						}
					}
					restrict := func(backend fixture.Backend, c *sql.Conn) {
						d := heapDiagnostic("2BP01", "dependent privileges exist")
						d.Hint = "Use CASCADE to revoke them too."
						step := fixture.ErrorDiagnostic("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION "+target+" FROM bt_acl_holder RESTRICT", d)
						step.Prepared = prepared
						run(backend, c, step)
					}
					run(b, source, fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO bt_acl_holder WITH GRANT OPTION", 0))
					actor(b, source, "bt_acl_holder", func() { run(b, source, fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO bt_acl_third", 0)) })
					check(b, source, "chain")
					// Controls have no PostgreSQL counterpart. The surrounding SQL
					// body independently specifies authority and provenance in both modes.
					if !b.PG {
						var snapshot bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil || snapshot.Len() == 0 {
							t.Fatal("grant-chain Dump", err, snapshot.Len())
						}
						connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/bt-acl-destination")
						if err != nil {
							t.Fatal(err)
						}
						destination := sql.OpenDB(connector)
						defer destination.Close()
						recorded, recorder := fixture.RecordedNativePool(t, connector)
						c, err := recorded.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						dest := b
						dest.DB, dest.Recorder = recorded, recorder
						for _, query := range []string{"CREATE ROLE bt_acl_spacer", "CREATE ROLE bt_acl_third", "CREATE ROLE bt_acl_public", "CREATE TABLE bt_acl_sentinel(v INT)", "INSERT INTO bt_acl_sentinel VALUES(29)", "CREATE INDEX bt_acl_sentinel_idx ON bt_acl_sentinel(v)", "CREATE EXTENSION amcheck", "GRANT EXECUTE ON FUNCTION " + target + " TO bt_acl_spacer", "CREATE TEMP TABLE bt_acl_retained(v INT)", "INSERT INTO bt_acl_retained VALUES(83)"} {
							if _, err := c.ExecContext(p.Ctx, query); err != nil {
								t.Fatal(query, err)
							}
						}
						retained := heapTemporaryIdentities(t, p, c)
						identitySQL := fmt.Sprintf("SELECT oid::bigint FROM pg_proc WHERE proname='%s' AND pronargs=%d", overload.name, overload.arguments)
						var memberOID int64
						if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&memberOID); err != nil || memberOID <= 0 {
							t.Fatal("destination Btree identity", memberOID, err)
						}
						missing := func(role string) {
							if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err == nil {
								t.Fatal("missing Btree ACL role accepted", role)
							}
							run(dest, c, fixture.Select("SELECT v FROM bt_acl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(29)}}))
							run(dest, c, fixture.Select(identitySQL, []string{"oid"}, []string{"INT8"}, [][]any{{memberOID}}))
							actor(dest, c, "bt_acl_spacer", func() {
								step := amcheckSelect("SELECT public."+overload.name+"('public.bt_acl_sentinel_idx'::regclass"+strings.Repeat(",true", overload.arguments-1)+") AS ok", false)
								step.Prepared = prepared
								run(dest, c, step)
							})
							if got := heapTemporaryIdentities(t, p, c); !reflect.DeepEqual(got, retained) {
								t.Fatal("failed ACL import changed TEMP identities", got, retained)
							}
							run(dest, c, fixture.Select("SELECT v FROM pg_temp.bt_acl_retained", []string{"v"}, []string{"INT4"}, [][]any{{int64(83)}}))
						}
						missing("bt_acl_holder")
						run(dest, c, fixture.Exec("DROP ROLE bt_acl_third", 0))
						run(dest, c, fixture.Exec("CREATE ROLE bt_acl_holder", 0))
						missing("bt_acl_third")
						run(dest, c, fixture.Exec("CREATE ROLE bt_acl_third", 0))
						if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(snapshot.Bytes())); err != nil {
							t.Fatal("independent Btree role-domain replay", err)
						}
						check(dest, c, "chain")
						restrict(dest, c)
						check(dest, c, "chain")
						control(p.Native)
						check(b, source, "chain")
					}
					restrict(b, source)
					check(b, source, "chain")
					for _, phase := range []struct{ name, sql string }{
						{"cascade", "REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION " + target + " FROM bt_acl_holder CASCADE"},
						{"public", "GRANT EXECUTE ON FUNCTION " + target + " TO PUBLIC"},
						{"cascade", "REVOKE EXECUTE ON FUNCTION " + target + " FROM PUBLIC"},
						{"empty", "REVOKE ALL ON FUNCTION " + target + " FROM bt_acl_holder," + owner},
					} {
						run(b, source, fixture.Exec(phase.sql, 0))
						check(b, source, phase.name)
						if !b.PG {
							control(p.Native)
							check(b, source, phase.name)
						}
					}
				})
			})
		}
	}
}
