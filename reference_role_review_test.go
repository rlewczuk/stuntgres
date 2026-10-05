//go:build reference

package stuntgres_test

import (
	"fmt"
	"github.com/lib/pq"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Native setup uses the actual internal ACL consumer seam in
// TestAuthorizationNamespaceLookupCheckpoint; SQL ACL mutation remains oracle
// fixture authority only. Expectations here are not taken from native output.
func TestReferenceRoleNamespaceLookupCheckpoint(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			reader := fmt.Sprintf("rn_%v_reader", prepared)
			roleReferenceCleanup(t, []string{reader})
			p := newReferencePair(t)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
			run(referenceExec("CREATE USER "+reader, 0))
			run(referenceExec("CREATE TABLE secret(id INT)", 0))
			run(referenceExec("INSERT INTO secret VALUES(7)", 1))
			if _, err := referenceAdmin.ExecContext(p.ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
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
				s := roleReferenceError(tc.query, code, message, "", prepared, "prepare")
				s.diagnostic.Position = tc.position
				s.selectRows = strings.HasPrefix(tc.query, "SELECT")
				if strings.Contains(tc.query, "$1") {
					s.args = []any{"bad"}
				}
				run(s)
			}
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"SELECT id FROM public.secret", "INSERT INTO secret VALUES(9)"} {
				s := roleReferenceError(query, "42501", "permission denied for table secret", "", prepared, "execute")
				s.selectRows = strings.HasPrefix(query, "SELECT")
				run(s)
			}
			invalid := roleReferenceError("INSERT INTO public.secret VALUES($1)", "22P02", `invalid input syntax for type integer: "bad"`, "", prepared, "execute")
			invalid.args = []any{"bad"}
			invalid.diagnostic.Context = "unnamed portal parameter $1 = '...'"
			run(invalid)
			run(roleReferenceError("INSERT INTO public.secret VALUES(2147483648)", "22003", "integer out of range", "", prepared, "execute"))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceRoleDependencyAddressDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("rd_%v_owner", prepared), fmt.Sprintf("rd_%v_reader", prepared)
			roleReferenceCleanup(t, []string{reader, owner})
			p := newReferencePair(t)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
			for _, name := range []string{owner, reader} {
				run(referenceExec("CREATE USER "+name, 0))
			}
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("CREATE TABLE secret(a INT,b INT)", 0))
			run(referenceExec("INSERT INTO secret VALUES(7,8)", 1))
			run(referenceExec(`CREATE TABLE "column table"("odd column" INT)`, 0))
			run(referenceExec(`INSERT INTO "column table" VALUES(7)`, 1))
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT SELECT(a) ON secret TO "+reader); err != nil {
				t.Fatal(err)
			}
			run(roleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), "privileges for column a of table secret", prepared, "execute"))
			// Reverse grant order still reports stable SubID order; duplicate
			// grants do not produce duplicate dependency descriptions.
			for _, query := range []string{"GRANT SELECT(b,a) ON secret TO " + reader, `GRANT SELECT("odd column") ON "column table" TO ` + reader} {
				if _, err := referenceAdmin.ExecContext(p.ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			run(referenceSelect("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			// REL_18_0 getObjectDescription uses raw attname, while the
			// relation description applies quote_qualified_identifier.
			detail := "privileges for column a of table secret\nprivileges for column b of table secret\nprivileges for column odd column of table \"column table\""
			run(roleReferenceError("DROP ROLE "+reader, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", reader), detail, prepared, "execute"))
			run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
			run(referenceSelect("SELECT a,b FROM secret", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
			run(referenceSelect(`SELECT "odd column" FROM "column table"`, []string{"odd column"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			for _, query := range []string{"REVOKE SELECT(a,b) ON secret FROM " + reader, `REVOKE SELECT("odd column") ON "column table" FROM ` + reader} {
				if _, err := referenceAdmin.ExecContext(p.ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("DROP ROLE "+reader, 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			var previous int64
			for _, name := range []string{"z", "a", "odd table", "select", "quote\"table", "integer", "Mixed"} {
				run(referenceExec("CREATE TABLE "+pq.QuoteIdentifier(name)+"(id INT)", 0))
				var oid int64
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", name).Scan(&oid); err != nil || oid <= previous {
					t.Fatal("creation identity ordering", name, oid, previous, err)
				}
				previous = oid
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT SELECT(id) ON z TO "+owner); err != nil {
				t.Fatal(err)
			}
			detail = "owner of table z\nowner of table a\nowner of table \"odd table\"\nowner of table \"select\"\nowner of table \"quote\"\"table\"\nowner of table \"integer\"\nowner of table \"Mixed\""
			run(roleReferenceError("DROP ROLE "+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), detail, prepared, "execute"))
			var count int
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", owner).Scan(&count); err != nil || count != 1 {
				t.Fatal("owner disappeared", count, err)
			}
		})
	}
}

func TestReferenceRolePublicGrantOptionsInvalid(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := newReferencePair(t)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("CREATE TABLE secret(id INT)", 0))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO secret VALUES(7)", 1))
			var before, after string
			acl := "SELECT COALESCE(relacl::text,'absent') FROM pg_class WHERE oid='public.secret'::regclass"
			if err := referenceAdmin.QueryRowContext(p.ctx, acl).Scan(&before); err != nil {
				t.Fatal(err)
			}
			// This invalid command is oracle fixture authority only, not native
			// SQL syntax. Host ACL rejection is an ordinary validation error.
			query := "GRANT SELECT ON secret TO PUBLIC WITH GRANT OPTION"
			var err error
			if prepared {
				stmt, prepareErr := referenceAdmin.PrepareContext(p.ctx, query)
				if prepareErr != nil {
					t.Fatal("unexpected Prepare rejection", prepareErr)
				}
				defer stmt.Close()
				_, err = stmt.ExecContext(p.ctx)
			} else {
				_, err = referenceAdmin.ExecContext(p.ctx, query)
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0LP01", Message: "grant options can only be granted to roles"}
			referenceAssertDiagnostic(t, err, &want)
			if err := referenceAdmin.QueryRowContext(p.ctx, acl).Scan(&after); err != nil || after != before {
				t.Fatal("invalid grant changed ACL", before, after, err)
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceRoleLaterDependentBatchAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			prefix := "rb_" + strconv.FormatBool(prepared) + "_"
			endpoint, member, owner := prefix+"endpoint", prefix+"member", prefix+"owner"
			roleReferenceCleanup(t, []string{member, endpoint, owner})
			p := newReferencePair(t)
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
			for _, name := range []string{endpoint, member, owner} {
				run(referenceExec("CREATE ROLE "+name, 0))
			}
			for _, query := range []string{"GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC", "GRANT " + endpoint + " TO " + member} {
				if _, err := referenceAdmin.ExecContext(p.ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
			run(referenceExec("CREATE TABLE sentinel(id INT)", 0))
			run(referenceExec("INSERT INTO sentinel VALUES(7)", 1))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			state := func() []int64 {
				t.Helper()
				values := make([]int64, 0, 6)
				for _, name := range []string{endpoint, member, owner} {
					var oid int64
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", name).Scan(&oid); err != nil || oid == 0 {
						t.Fatal("missing role", name, oid, err)
					}
					values = append(values, oid)
				}
				var oid, relowner int64
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT oid::bigint,relowner::bigint FROM pg_class WHERE oid='public.sentinel'::regclass").Scan(&oid, &relowner); err != nil || oid == 0 || relowner != values[2] {
					t.Fatal("catalog owner", oid, relowner, err)
				}
				var grantor string
				var admin, inherit, set bool
				var membershipOID int64
				if err := referenceAdmin.QueryRowContext(p.ctx, `SELECT a.oid::bigint,g.rolname,a.admin_option,a.inherit_option,a.set_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname=$1 AND m.rolname=$2`, endpoint, member).Scan(&membershipOID, &grantor, &admin, &inherit, &set); err != nil || membershipOID == 0 || grantor != "stuntgres" || admin || !inherit || !set {
					t.Fatal("membership changed", membershipOID, grantor, admin, inherit, set, err)
				}
				var count int
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_auth_members WHERE roleid=$1 OR member=$1", values[0]).Scan(&count); err != nil || count != 1 {
					t.Fatal("membership set changed", count, err)
				}
				return append(values, oid, relowner, membershipOID)
			}
			before := state()
			run(roleReferenceError("DROP ROLE "+endpoint+","+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), "owner of table sentinel", prepared, "execute"))
			if after := state(); !reflect.DeepEqual(before, after) {
				t.Fatal("batch changed identities/catalog", before, after)
			}
			run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
