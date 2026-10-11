//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Native setup uses the actual internal ACL consumer seam in
// TestAuthorizationNamespaceLookupCheckpoint; SQL ACL mutation remains oracle
// fixture authority only. Expectations here are not taken from native output.
func TestReferenceRoleNamespaceLookupCheckpoint(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			reader := fmt.Sprintf("rn_%v_reader", prepared)
			fixture.RoleReferenceCleanup(t, []string{reader})
			p := fixture.NewPair(t)
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, s) }
			run(fixture.Exec("CREATE USER "+reader, 0))
			run(fixture.Exec("CREATE TABLE secret(id INT)", 0))
			run(fixture.Exec("INSERT INTO secret VALUES(7)", 1))
			if _, err := fixture.Admin.ExecContext(p.Ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
			for _, tc := range []struct{ query, position string }{
				{"SELECT id FROM public.secret", "16"},
				{"SELECT missing FROM public.secret", "21"},
				{"INSERT INTO public.secret VALUES(9)", "13"},
				{"INSERT INTO public.secret VALUES('bad')", "13"},
				{"INSERT INTO public.secret(missing) VALUES(2147483648)", "13"},
				{"INSERT INTO public.secret VALUES($1)", "13"},
				{"SELECT id FROM secret", "16"},
				{"SELECT missing FROM secret", "21"},
				{"INSERT INTO secret VALUES(9)", "13"},
				{"INSERT INTO secret VALUES('bad')", "13"},
				{"INSERT INTO secret(missing) VALUES(2147483648)", "13"},
				{"INSERT INTO secret VALUES($1)", "13"},
			} {
				code, message := "42501", "permission denied for schema public"
				if !strings.Contains(tc.query, "public.secret") {
					code, message = "42P01", `relation "secret" does not exist`
				}
				s := fixture.RoleReferenceError(tc.query, code, message, "", prepared, "prepare")
				s.Diagnostic.Position = tc.position
				s.SelectRows = strings.HasPrefix(tc.query, "SELECT")
				if strings.Contains(tc.query, "$1") {
					s.Args = []any{"bad"}
				}
				run(s)
			}
			if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"SELECT id FROM public.secret", "INSERT INTO secret VALUES(9)"} {
				s := fixture.RoleReferenceError(query, "42501", "permission denied for table secret", "", prepared, "execute")
				s.SelectRows = strings.HasPrefix(query, "SELECT")
				run(s)
			}
			invalid := fixture.RoleReferenceError("INSERT INTO public.secret VALUES($1)", "22P02", `invalid input syntax for type integer: "bad"`, "", prepared, "execute")
			invalid.Args = []any{"bad"}
			invalid.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
			run(invalid)
			run(fixture.RoleReferenceError("INSERT INTO public.secret VALUES(2147483648)", "22003", "integer out of range", "", prepared, "execute"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceRoleLaterDependentBatchAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			prefix := "rb_" + strconv.FormatBool(prepared) + "_"
			endpoint, member, owner := prefix+"endpoint", prefix+"member", prefix+"owner"
			fixture.RoleReferenceCleanup(t, []string{member, endpoint, owner})
			p := fixture.NewPair(t)
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, s) }
			for _, name := range []string{endpoint, member, owner} {
				run(fixture.Exec("CREATE ROLE "+name, 0))
			}
			for _, query := range []string{"GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC", "GRANT " + endpoint + " TO " + member} {
				if _, err := fixture.Admin.ExecContext(p.Ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
			run(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
			run(fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			state := func() []int64 {
				t.Helper()
				values := make([]int64, 0, 6)
				for _, name := range []string{endpoint, member, owner} {
					var oid int64
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", name).Scan(&oid); err != nil || oid == 0 {
						t.Fatal("missing role", name, oid, err)
					}
					values = append(values, oid)
				}
				var oid, relowner int64
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT oid::bigint,relowner::bigint FROM pg_class WHERE oid='public.sentinel'::regclass").Scan(&oid, &relowner); err != nil || oid == 0 || relowner != values[2] {
					t.Fatal("catalog owner", oid, relowner, err)
				}
				var grantor string
				var admin, inherit, set bool
				var membershipOID int64
				if err := fixture.Admin.QueryRowContext(p.Ctx, `SELECT a.oid::bigint,g.rolname,a.admin_option,a.inherit_option,a.set_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname=$1 AND m.rolname=$2`, endpoint, member).Scan(&membershipOID, &grantor, &admin, &inherit, &set); err != nil || membershipOID == 0 || grantor != "stuntgres" || admin || !inherit || !set {
					t.Fatal("membership changed", membershipOID, grantor, admin, inherit, set, err)
				}
				var count int
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_auth_members WHERE roleid=$1 OR member=$1", values[0]).Scan(&count); err != nil || count != 1 {
					t.Fatal("membership set changed", count, err)
				}
				return append(values, oid, relowner, membershipOID)
			}
			before := state()
			run(fixture.RoleReferenceError("DROP ROLE "+endpoint+","+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), "owner of table sentinel", prepared, "execute"))
			if after := state(); !reflect.DeepEqual(before, after) {
				t.Fatal("batch changed identities/catalog", before, after)
			}
			run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
