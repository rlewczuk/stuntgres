package reference_test

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
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
			fixture.RoleReferenceCleanup(t, append(names, "PUBLIC", "NONE", strings.ToLower(names[7]), strings.Repeat("y", 62)))
			p := fixture.NewScenario(t)
			step := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			for _, tc := range []struct {
				kind, name, options string
				login               bool
			}{{"ROLE", names[0], "", false}, {"USER", names[1], "", true}, {"GROUP", names[2], "", false}, {"ROLE", names[3], " WITH LOGIN", true}, {"USER", names[4], " NOLOGIN", false}} {
				step(fixture.Exec("CREATE "+tc.kind+" "+pq.QuoteIdentifier(tc.name)+tc.options, 0))
				var login bool
				if p.Postgres != nil {
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", tc.name).Scan(&login); err != nil || login != tc.login {
						t.Fatalf("oracle LOGIN %s=%v want %v: %v", tc.name, login, tc.login, err)
					}
				}
				connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs("reference/"+t.Name(), tc.name)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := connector.Connect(p.Ctx)
				if (err == nil) != tc.login {
					t.Fatal("LOGIN admission", tc.name, err)
				}
				if raw != nil {
					raw.Close()
				}
			}
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE "+names[0], "42710", fmt.Sprintf("role \"%s\" already exists", names[0]), "", prepared, "execute"))
			step(fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier(names[6]), 0))
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(names[6]), "42710", fmt.Sprintf("role \"%s\" already exists", names[6]), "", prepared, "execute"))
			step(fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier(names[7]), 0))
			step(fixture.Exec("CREATE ROLE "+strings.Repeat("x", 64), 0))
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE "+strings.Repeat("x", 63), "42710", `role "`+strings.Repeat("x", 63)+`" already exists`, "", prepared, "execute"))
			step(fixture.Exec("CREATE ROLE "+names[7], 0))
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(strings.ToLower(names[7])), "42710", fmt.Sprintf("role \"%s\" already exists", strings.ToLower(names[7])), "", prepared, "execute"))
			step(fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier(strings.Repeat("y", 62)+"é"), 0))
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE "+pq.QuoteIdentifier(strings.Repeat("y", 62)), "42710", `role "`+strings.Repeat("y", 62)+`" already exists`, "", prepared, "execute"))
			for _, options := range []string{"LOGIN LOGIN", "LOGIN NOLOGIN"} {
				query := "CREATE ROLE " + names[5] + " " + options
				position := strings.LastIndex(query, " ") + 2
				s := fixture.RoleReferenceError(query, "42601", "conflicting or redundant options", "", prepared, "execute")
				s.Diagnostic.Position = strconv.Itoa(position)
				p.Step(t, s)
				var count int
				if p.Postgres != nil {
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", names[5]).Scan(&count); err != nil || count != 0 {
						t.Fatal("duplicate option leaked role", count, err)
					}
				}
				p.Step(t, fixture.RoleReferenceError("DROP ROLE "+names[5], "42704", fmt.Sprintf("role %q does not exist", names[5]), "", prepared, "execute"))
			}
			for _, kind := range []string{"ROLE", "USER", "GROUP"} {
				for _, name := range []string{"public", "none", "\"public\"", "\"none\""} {
					decoded := strings.Trim(name, "\"")
					s := fixture.RoleReferenceError("CREATE "+kind+" "+name, "42939", fmt.Sprintf("role name \"%s\" is reserved", decoded), "", prepared, "prepare")
					s.Diagnostic.Position = "13"
					if kind == "GROUP" {
						s.Diagnostic.Position = "14"
					}
					p.Step(t, s)
				}
				for _, name := range []string{"CURRENT_USER", "SESSION_USER", "CURRENT_ROLE"} {
					s := fixture.RoleReferenceError("CREATE "+kind+" "+name, "42939", name+" cannot be used as a role name here", "", prepared, "prepare")
					s.Diagnostic.Position = "13"
					if kind == "GROUP" {
						s.Diagnostic.Position = "14"
					}
					p.Step(t, s)
				}
			}
			step(fixture.Exec(`CREATE ROLE "PUBLIC"`, 0))
			step(fixture.Exec(`CREATE USER "NONE"`, 0))
			p.Step(t, fixture.RoleReferenceError("CREATE ROLE pg_role_core", "42939", `role name "pg_role_core" is reserved`, `Role names starting with "pg_" are reserved.`, prepared, "execute"))
			step(fixture.Exec("DROP GROUP "+names[2]+","+names[2], 0))
			step(fixture.Exec("DROP USER "+names[1], 0))
			step(fixture.Exec("DROP ROLE "+names[0], 0))
			p.Step(t, fixture.RoleReferenceError("DROP ROLE "+names[0], "42704", fmt.Sprintf("role \"%s\" does not exist", names[0]), "", prepared, "execute"))
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
			fixture.RoleReferenceCleanup(t, []string{owner, reader, nolog, free})
			p := fixture.NewScenario(t)
			for _, name := range []string{owner, reader} {
				s := fixture.Exec("CREATE USER "+name, 0)
				s.Prepared = prepared
				p.Step(t, s)
			}
			p.Step(t, fixture.Exec("CREATE ROLE "+nolog, 0))
			p.Step(t, fixture.Exec("CREATE ROLE "+free, 0))
			for _, backend := range p.Backends() {
				bootstrap := "postgres"
				if backend.PG {
					bootstrap = "stuntgres"
				}
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					identity := func(c *sql.Conn, want string) {
						run(c, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{want, want}}))
						run(c, fixture.Exec("SELECT current_user", 1))
					}
					identity(a, bootstrap)
					identity(b, bootstrap)
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+nolog, 0))
					identity(a, nolog)
					run(a, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(b, fixture.Exec("SET SESSION AUTHORIZATION '"+reader+"'", 0))
					identity(a, owner)
					identity(b, reader)
					fixture.WithPublicSchemaOwner(t, p, backend, owner, func() { run(a, fixture.Exec("CREATE TABLE secret(id INT)", 0)) })
					run(a, fixture.Exec("INSERT INTO secret VALUES(7)", 1))
					sentinel := func() {
						run(a, fixture.Select("SELECT id FROM secret ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					sentinel()
					for _, query := range []string{"SELECT id FROM secret", "INSERT INTO secret VALUES(9)"} {
						s := fixture.RoleReferenceError(query, "42501", "permission denied for table secret", "", prepared, "execute")
						s.SelectRows = strings.HasPrefix(query, "SELECT")
						run(b, s)
						sentinel()
					}
					parameterFailure := fixture.RoleReferenceError("INSERT INTO secret VALUES($1)", "22P02", `invalid input syntax for type integer: "bad"`, "", prepared, "execute")
					parameterFailure.Args = []any{"bad"}
					postgresParameterFailure := *parameterFailure.Diagnostic
					// True typed input failures retain the actual failing parameter index.
					postgresParameterFailure.Context = "unnamed portal parameter $1 = '...'"
					parameterFailure.Diagnostic = &postgresParameterFailure
					run(b, parameterFailure)
					validParameter := fixture.RoleReferenceError("INSERT INTO secret VALUES($1)", "42501", "permission denied for table secret", "", prepared, "execute")
					validParameter.Args = []any{"9"}
					run(b, validParameter)
					// Immutable int8-to-int4 assignment folds during execution planning,
					// before executor ACL checks, but only after successful Prepare.
					run(b, fixture.RoleReferenceError("INSERT INTO secret VALUES(2147483648)", "22003", "integer out of range", "", prepared, "execute"))
					sentinel()
					for _, tc := range []struct{ query, code, message, detail string }{{"CREATE ROLE denied_role", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles."}, {"DROP ROLE " + free, "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles."}} {
						run(b, fixture.RoleReferenceError(tc.query, tc.code, tc.message, tc.detail, prepared, "execute"))
						identity(b, reader)
					}
					heldIdentity, err := a.PrepareContext(p.Ctx, "SELECT session_user,current_user,session_user")
					if err != nil {
						t.Fatal(err)
					}
					defer heldIdentity.Close()
					for _, query := range []string{"SET SESSION AUTHORIZATION " + nolog, "SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
						run(a, fixture.Exec(query, 0))
						want := bootstrap
						if strings.HasSuffix(query, nolog) {
							want = nolog
						}
						roleScenarioIdentity(t, a, want, heldIdentity, backend.PG)
						identity(b, reader)
					}
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					heldSecret, err := a.PrepareContext(p.Ctx, "SELECT id FROM secret")
					if err != nil {
						t.Fatal(err)
					}
					defer heldSecret.Close()
					heldInsert, err := a.PrepareContext(p.Ctx, "INSERT INTO secret VALUES(9)")
					if err != nil {
						t.Fatal(err)
					}
					defer heldInsert.Close()
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
					roleScenarioIdentity(t, a, reader, heldIdentity, backend.PG)
					var value int64
					err = heldSecret.QueryRowContext(p.Ctx).Scan(&value)
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table secret"}
					fixture.AssertDiagnostic(t, err, &want)
					result, err := heldInsert.ExecContext(p.Ctx)
					if result != nil {
						t.Fatal("denied held INSERT result")
					}
					fixture.AssertDiagnostic(t, err, &want)
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					if err := heldSecret.QueryRowContext(p.Ctx).Scan(&value); err != nil || value != 7 {
						t.Fatal("held owner", value, err)
					}
					identity(b, reader)
					run(a, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					roleScenarioIdentity(t, a, bootstrap, heldIdentity, backend.PG)
					run(a, fixture.RoleReferenceError("SET SESSION AUTHORIZATION absent_role", "22023", `role "absent_role" does not exist`, "", prepared, "execute"))
					identity(a, bootstrap)
					// R4: owner dependency, full diagnostic, atomic batch, current-user guard.
					run(a, fixture.RoleReferenceError("DROP ROLE "+owner, "2BP01", fmt.Sprintf("role \"%s\" cannot be dropped because some objects depend on it", owner), "owner of table secret", prepared, "execute"))
					run(a, fixture.RoleReferenceError("DROP ROLE "+free+",absent_role", "42704", `role "absent_role" does not exist`, "", prepared, "execute"))
					run(a, fixture.Exec("SET SESSION AUTHORIZATION "+free, 0))
					identity(a, free)
					run(a, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(a, fixture.RoleReferenceError("DROP ROLE "+bootstrap, "55006", "current user cannot be dropped", "", prepared, "execute"))
					run(a, fixture.Select("SELECT id FROM secret", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(a, fixture.Exec("DROP ROLE "+free, 0))
					run(a, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(b, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
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
			fixture.RoleReferenceCleanup(t, []string{aName, bName})
			p := fixture.NewScenario(t)
			for _, name := range []string{aName, bName} {
				p.Step(t, fixture.Exec("CREATE USER "+name, 0))
				if p.Postgres != nil {
					if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+pq.QuoteIdentifier(name)+" PASSWORD 'role-core-fixture'"); err != nil {
						t.Fatal(err)
					}
				}
			}
			memoryDSN := "reference/" + t.Name()
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var pools []*sql.DB
					for _, name := range []string{aName, bName} {
						var db *sql.DB
						if !backend.PG {
							connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(memoryDSN, name)
							if err != nil {
								t.Fatal(err)
							}
							db = sql.OpenDB(connector)
						} else {
							u, err := url.Parse(fixture.DSN)
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
					a, err := pools[0].Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := pools[1].Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					execute := func(c *sql.Conn, q string) error {
						if !prepared {
							_, err := c.ExecContext(p.Ctx, q)
							return err
						}
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							return err
						}
						defer s.Close()
						_, err = s.ExecContext(p.Ctx)
						return err
					}
					identity := func(c *sql.Conn, want string) {
						rows, err := c.QueryContext(p.Ctx, "SELECT session_user,current_user")
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
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf("permission denied to set session authorization \"%s\"", bName)}
					fixture.AssertDiagnostic(t, err, &want)
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
