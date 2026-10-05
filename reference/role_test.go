//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceRoleCreation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			prefix := "rc_" + mode + "_"
			names := []string{prefix + "role", prefix + "user", prefix + "group", prefix + "login", prefix + "nolog", prefix + "bad", prefix + "quote\"slash\\", prefix + "Mixed", strings.Repeat("x", 63)}
			roleReferenceCleanup(t, append(names, "PUBLIC", "NONE", strings.ToLower(names[7]), strings.Repeat("y", 62)))
			p := newReferencePair(t)
			step := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, tc := range []struct {
				kind, name, options string
				login               bool
			}{{"ROLE", names[0], "", false}, {"USER", names[1], "", true}, {"GROUP", names[2], "", false}, {"ROLE", names[3], " WITH LOGIN", true}, {"USER", names[4], " NOLOGIN", false}} {
				step(referenceExec("CREATE "+tc.kind+" "+pq.QuoteIdentifier(tc.name)+tc.options, 0))
				var login bool
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", tc.name).Scan(&login); err != nil || login != tc.login {
					t.Fatalf("oracle LOGIN %s=%v want %v: %v", tc.name, login, tc.login, err)
				}
			}
			p.step(t, roleReferenceError("CREATE ROLE "+names[0], "42710", fmt.Sprintf("role \"%s\" already exists", names[0]), "", prepared, "execute"))
			step(referenceExec("CREATE ROLE "+pq.QuoteIdentifier(names[6]), 0))
			p.step(t, roleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(names[6]), "42710", fmt.Sprintf("role \"%s\" already exists", names[6]), "", prepared, "execute"))
			step(referenceExec("CREATE ROLE "+pq.QuoteIdentifier(names[7]), 0))
			step(referenceExec("CREATE ROLE "+strings.Repeat("x", 64), 0))
			p.step(t, roleReferenceError("CREATE ROLE "+strings.Repeat("x", 63), "42710", `role "`+strings.Repeat("x", 63)+`" already exists`, "", prepared, "execute"))
			step(referenceExec("CREATE ROLE "+names[7], 0))
			p.step(t, roleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(strings.ToLower(names[7])), "42710", fmt.Sprintf("role \"%s\" already exists", strings.ToLower(names[7])), "", prepared, "execute"))
			step(referenceExec("CREATE ROLE "+pq.QuoteIdentifier(strings.Repeat("y", 62)+"é"), 0))
			p.step(t, roleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(strings.Repeat("y", 62)), "42710", `role "`+strings.Repeat("y", 62)+`" already exists`, "", prepared, "execute"))
			for _, options := range []string{"LOGIN LOGIN", "LOGIN NOLOGIN"} {
				query := "CREATE ROLE " + names[5] + " " + options
				position := strings.LastIndex(query, " ") + 2
				s := roleReferenceError(query, "42601", "conflicting or redundant options", "", prepared, "execute")
				s.diagnostic.Position = strconv.Itoa(position)
				p.step(t, s)
				var count int
				if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", names[5]).Scan(&count); err != nil || count != 0 {
					t.Fatal("duplicate option leaked role", count, err)
				}
			}
			for _, kind := range []string{"ROLE", "USER", "GROUP"} {
				for _, name := range []string{"public", "none", "\"public\"", "\"none\""} {
					decoded := strings.Trim(name, "\"")
					s := roleReferenceError("CREATE "+kind+" "+name, "42939", fmt.Sprintf("role name \"%s\" is reserved", decoded), "", prepared, "prepare")
					s.diagnostic.Position = "13"
					if kind == "GROUP" {
						s.diagnostic.Position = "14"
					}
					p.step(t, s)
				}
				for _, name := range []string{"CURRENT_USER", "SESSION_USER", "CURRENT_ROLE"} {
					s := roleReferenceError("CREATE "+kind+" "+name, "42939", name+" cannot be used as a role name here", "", prepared, "prepare")
					s.diagnostic.Position = "13"
					if kind == "GROUP" {
						s.diagnostic.Position = "14"
					}
					p.step(t, s)
				}
			}
			step(referenceExec(`CREATE ROLE "PUBLIC"`, 0))
			step(referenceExec(`CREATE USER "NONE"`, 0))
			p.step(t, roleReferenceError("CREATE ROLE pg_role_core", "42939", `role name "pg_role_core" is reserved`, `Role names starting with "pg_" are reserved.`, prepared, "execute"))
			step(referenceExec("DROP GROUP "+names[2]+","+names[2], 0))
			step(referenceExec("DROP USER "+names[1], 0))
			step(referenceExec("DROP ROLE "+names[0], 0))
			p.step(t, roleReferenceError("DROP ROLE "+names[0], "42704", fmt.Sprintf("role \"%s\" does not exist", names[0]), "", prepared, "execute"))
		})
	}
}

func TestReferenceRoleAuthorization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			owner := "ra_" + mode + "_owner"
			reader := "ra_" + mode + "_reader"
			nolog := "ra_" + mode + "_nolog"
			free := "ra_" + mode + "_free"
			roleReferenceCleanup(t, []string{owner, reader, nolog, free})
			p := newReferencePair(t)
			for _, name := range []string{owner, reader} {
				s := referenceExec("CREATE USER "+name, 0)
				s.prepared = prepared
				p.step(t, s)
			}
			p.step(t, referenceExec("CREATE ROLE "+nolog, 0))
			p.step(t, referenceExec("CREATE ROLE "+free, 0))
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
				t.Fatal(err)
			}
			for _, backend := range []struct {
				name, bootstrap string
				db              *sql.DB
				record          *referenceRecorder
			}{{"memory", "postgres", p.memory, p.memoryRecord}, {"postgres", "stuntgres", p.postgres, p.postgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, backend.record, s)
					}
					identity := func(c *sql.Conn, want string) {
						run(c, referenceSelect("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{want, want}}))
						run(c, referenceExec("SELECT current_user", 1))
					}
					identity(a, backend.bootstrap)
					identity(b, backend.bootstrap)
					run(a, referenceExec("SET SESSION AUTHORIZATION "+nolog, 0))
					identity(a, nolog)
					run(a, referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(a, referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
					run(b, referenceExec("SET SESSION AUTHORIZATION '"+reader+"'", 0))
					identity(a, owner)
					identity(b, reader)
					run(a, referenceExec("CREATE TABLE secret(id INT)", 0))
					run(a, referenceExec("INSERT INTO secret VALUES(7)", 1))
					sentinel := func() {
						run(a, referenceSelect("SELECT id FROM secret ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					sentinel()
					for _, query := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(9)"} {
						s := roleReferenceError(query, "42501", "permission denied for table secret", "", prepared, "execute")
						s.selectRows = strings.HasPrefix(query, "SELECT")
						run(b, s)
						sentinel()
					}
					parameterFailure := roleReferenceError("INSERT INTO secret VALUES($1)", "22P02", `invalid input syntax for type integer: "bad"`, "", prepared, "execute")
					parameterFailure.args = []any{"bad"}
					postgresParameterFailure := *parameterFailure.diagnostic
					// Reuse the existing declared wire-portal context boundary: native is not
					// a server and never fabricates PostgreSQL portal/source-location fields.
					postgresParameterFailure.Context = "unnamed portal parameter $1 = '...'"
					parameterFailure.postgresDiagnostic = &postgresParameterFailure
					run(b, parameterFailure)
					validParameter := roleReferenceError("INSERT INTO secret VALUES($1)", "42501", "permission denied for table secret", "", prepared, "execute")
					validParameter.args = []any{"9"}
					run(b, validParameter)
					// Immutable int8-to-int4 assignment folds during execution planning,
					// before executor ACL checks, but only after successful Prepare.
					run(b, roleReferenceError("INSERT INTO secret VALUES(2147483648)", "22003", "integer out of range", "", prepared, "execute"))
					sentinel()
					for _, tc := range []struct{ query, code, message, detail string }{{"CREATE ROLE denied_role", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."}, {"DROP ROLE " + free, "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles."}} {
						run(b, roleReferenceError(tc.query, tc.code, tc.message, tc.detail, prepared, "execute"))
						identity(b, reader)
					}
					heldIdentity, err := a.PrepareContext(p.ctx, "SELECT session_user,current_user")
					if err != nil {
						t.Fatal(err)
					}
					defer heldIdentity.Close()
					heldSecret, err := a.PrepareContext(p.ctx, "SELECT id FROM secret")
					if err != nil {
						t.Fatal(err)
					}
					defer heldSecret.Close()
					run(a, referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
					var session, current string
					if err := heldIdentity.QueryRowContext(p.ctx).Scan(&session, &current); err != nil || session != reader || current != reader {
						t.Fatal("held identity", session, current, err)
					}
					var value int64
					err = heldSecret.QueryRowContext(p.ctx).Scan(&value)
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table secret"}
					referenceAssertDiagnostic(t, err, &want)
					run(a, referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
					if err := heldSecret.QueryRowContext(p.ctx).Scan(&value); err != nil || value != 7 {
						t.Fatal("held owner", value, err)
					}
					identity(b, reader)
					run(a, referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(a, roleReferenceError("SET SESSION AUTHORIZATION absent_role", "22023", `role "absent_role" does not exist`, "", prepared, "execute"))
					identity(a, backend.bootstrap)
					// R4: owner dependency, full diagnostic, atomic batch, current-user guard.
					run(a, roleReferenceError("DROP ROLE "+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), "owner of table secret", prepared, "execute"))
					run(a, roleReferenceError("DROP ROLE "+free+",absent_role", "42704", `role "absent_role" does not exist`, "", prepared, "execute"))
					run(a, referenceExec("SET SESSION AUTHORIZATION "+free, 0))
					identity(a, free)
					run(a, referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(a, roleReferenceError("DROP ROLE "+backend.bootstrap, "55006", "current user cannot be dropped", "", prepared, "execute"))
					run(a, referenceSelect("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(a, referenceExec("DROP ROLE "+free, 0))
					run(a, referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(b, referenceExec("RESET SESSION AUTHORIZATION", 0))
				})
			}
		})
	}
}

func TestReferenceRoleAuthenticatedSessions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			aName := "rl_" + mode + "_a"
			bName := "rl_" + mode + "_b"
			roleReferenceCleanup(t, []string{aName, bName})
			p := newReferencePair(t)
			for _, name := range []string{aName, bName} {
				p.step(t, referenceExec("CREATE USER "+name, 0))
				if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE "+pq.QuoteIdentifier(name)+" PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
			}
			memoryDSN := "reference/" + t.Name()
			for _, backend := range []string{"memory", "postgres"} {
				t.Run(backend, func(t *testing.T) {
					var pools []*sql.DB
					for _, name := range []string{aName, bName} {
						var db *sql.DB
						if backend == "memory" {
							connector, err := referenceMemoryDriver.OpenConnectorAs(memoryDSN, name)
							if err != nil {
								t.Fatal(err)
							}
							db = sql.OpenDB(connector)
						} else {
							u, err := url.Parse(referenceDSN)
							if err != nil {
								t.Fatal(err)
							}
							u.User = url.UserPassword(name, "role-core-fixture")
							db, err = sql.Open("postgres", u.String())
							if err != nil {
								t.Fatal(err)
							}
						}
						pools = append(pools, db)
						defer db.Close()
					}
					a, err := pools[0].Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := pools[1].Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					execute := func(c *sql.Conn, q string) error {
						if !prepared {
							_, err := c.ExecContext(p.ctx, q)
							return err
						}
						s, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							return err
						}
						defer s.Close()
						_, err = s.ExecContext(p.ctx)
						return err
					}
					identity := func(c *sql.Conn, want string) {
						rows, err := c.QueryContext(p.ctx, "SELECT session_user,current_user")
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "NAME" || types[1].DatabaseTypeName() != "NAME" {
							t.Fatal(types, err)
						}
						if !rows.Next() {
							t.Fatal("identity no row", rows.Err())
						}
						var session, current string
						if err := rows.Scan(&session, &current); err != nil || session != want || current != want {
							t.Fatal(session, current, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("identity extra/error", rows.Err())
						}
					}
					identity(a, aName)
					identity(b, bName)
					err = execute(a, "SET SESSION AUTHORIZATION "+bName)
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf("permission denied to set session authorization \"%s\"", bName)}
					referenceAssertDiagnostic(t, err, &want)
					identity(a, aName)
					identity(b, bName)
					for _, q := range []string{"SET SESSION AUTHORIZATION " + aName, "SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
						if err := execute(a, q); err != nil {
							t.Fatal(err)
						}
						identity(a, aName)
						identity(b, bName)
					}
				})
			}
		})
	}
}

// SQL membership mutation is oracle-fixture authority only. Native membership
// behavior is independently exercised through the real internal seam in U4.
func TestReferenceRoleMembershipDependencies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for index, order := range []string{"endpoint", "grantor,endpoint", "endpoint,grantor", "grantor,member", "member,grantor"} {
			t.Run(fmt.Sprintf("prepared=%v/order=%d", prepared, index), func(t *testing.T) {
				prefix := fmt.Sprintf("rm_%v_%d_", prepared, index)
				endpoint, grantor, member := prefix+"endpoint", prefix+"grantor", prefix+"member"
				roleReferenceCleanup(t, []string{member, grantor, endpoint})
				p := newReferencePair(t)
				exec := func(query string) error {
					if !prepared {
						_, err := referenceAdmin.ExecContext(p.ctx, query)
						return err
					}
					stmt, err := referenceAdmin.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal("unexpected protocol Prepare failure", query, err)
					}
					defer stmt.Close()
					_, err = stmt.ExecContext(p.ctx)
					return err
				}
				for _, name := range []string{endpoint, grantor, member} {
					if err := exec("CREATE ROLE " + name); err != nil {
						t.Fatal(err)
					}
				}
				for _, query := range []string{
					"GRANT " + endpoint + " TO " + grantor + " WITH ADMIN OPTION",
					"GRANT " + endpoint + " TO " + member + " GRANTED BY " + grantor,
				} {
					if _, err := referenceAdmin.ExecContext(p.ctx, query); err != nil {
						t.Fatal(err)
					}
				}
				memberships := func(want int) {
					rows, err := referenceAdmin.QueryContext(p.ctx, `SELECT m.rolname,g.rolname,a.admin_option,a.inherit_option,a.set_option
						FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid
						JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor
						WHERE r.rolname=$1 ORDER BY m.rolname`, endpoint)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					count := 0
					for rows.Next() {
						var m, g string
						var admin, inherit, set bool
						if err := rows.Scan(&m, &g, &admin, &inherit, &set); err != nil {
							t.Fatal(err)
						}
						if !inherit || !set {
							t.Fatal("membership default flags", inherit, set)
						}
						if m == grantor {
							if g != "stuntgres" || !admin {
								t.Fatal("upstream membership", m, g, admin)
							}
						} else if m == member {
							if g != grantor || admin {
								t.Fatal("grantor membership", m, g, admin)
							}
						} else {
							t.Fatal("unexpected member", m)
						}
						count++
					}
					if rows.Err() != nil || count != want {
						t.Fatal("membership count", count, want, rows.Err())
					}
				}
				memberships(2)
				err := exec("DROP ROLE " + grantor)
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01",
					Message: fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", grantor),
					Detail:  "privileges for membership of role " + member + " in role " + endpoint}
				referenceAssertDiagnostic(t, err, &want)
				memberships(2)
				err = exec("DROP ROLE " + endpoint + ",missing_membership_role")
				missing := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "missing_membership_role" does not exist`}
				referenceAssertDiagnostic(t, err, &missing)
				memberships(2)
				targets := strings.ReplaceAll(order, "endpoint", endpoint)
				targets = strings.ReplaceAll(targets, "grantor", grantor)
				targets = strings.ReplaceAll(targets, "member", member)
				if err := exec("DROP ROLE " + targets); err != nil {
					t.Fatal(err)
				}
				memberships(0)
			})
		}
	}
}
