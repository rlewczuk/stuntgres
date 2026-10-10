package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceRoleAttributesAuthority(t *testing.T) {
	// Q1: distinguish CREATE negative toggles from ALTER's symmetric checks,
	// and resolve the documentation/release-source replication-target tension.
	for _, prepared := range []bool{false, true} {
		roleAttributesAuthorityQuestions(t, prepared)
		roleAttributesSuperuserWitness(t, prepared)
		for _, attr := range []string{"CREATEDB", "REPLICATION", "BYPASSRLS"} {
			for _, own := range []bool{false, true} {
				for _, replicationTarget := range []bool{false, true} {
					for _, create := range []bool{false, true} {
						for _, positive := range []bool{false, true} {
							label := fmt.Sprintf("matrix/%s/own%v/repTarget%v/create%v/positive%v/prepared%v", attr, own, replicationTarget, create, positive, prepared)
							t.Run(label, func(t *testing.T) {
								roleReferenceCleanup(t, []string{"raa_actor", "raa_target", "raa_new"})
								p := newReferenceScenario(t)
								for _, b := range p.backends() {
									c, err := b.db.Conn(p.ctx)
									if err != nil {
										t.Fatal(err)
									}
									defer c.Close()
									defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
									run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
									options := " CREATEROLE"
									if own {
										options += " " + attr
									}
									run(referenceExec("CREATE ROLE raa_actor"+options, 0))
									run(referenceExec("SET SESSION AUTHORIZATION raa_actor", 0))
									run(referenceExec("CREATE ROLE raa_target", 0))
									run(referenceExec("RESET SESSION AUTHORIZATION", 0))
									if replicationTarget {
										run(referenceExec("ALTER ROLE raa_target REPLICATION", 0))
									}
									run(referenceExec("SET SESSION AUTHORIZATION raa_actor", 0))
									toggle := attr
									if !positive {
										toggle = "NO" + attr
									}
									query := "ALTER ROLE raa_target " + toggle
									if create {
										query = "CREATE ROLE raa_new " + toggle
									}
									if own || create && !positive {
										run(referenceExec(query, 0))
									} else {
										message := "permission denied to alter role"
										detail := "Only roles with the " + attr + " attribute may change the " + attr + " attribute."
										if create {
											message = "permission denied to create role"
											detail = "Only roles with the " + attr + " attribute may create roles with the " + attr + " attribute."
										}
										run(roleReferenceError(query, "42501", message, detail, prepared, "execute"))
									}
								}
							})
						}
					}
				}
			}
		}
		for _, targetReplication := range []bool{false, true} {
			t.Run(fmt.Sprintf("creator/self/replication%v/prepared%v", targetReplication, prepared), func(t *testing.T) {
				roleReferenceCleanup(t, []string{"raa_creator", "raa_ordinary", "raa_other", "raa_member", "raa_mid", "raa_power", "raa_denied"})
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					c, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
					run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
					for _, q := range []string{"CREATE ROLE raa_creator CREATEROLE", "CREATE ROLE raa_other", "CREATE ROLE raa_member", "CREATE ROLE raa_mid", "CREATE ROLE raa_power CREATEROLE CREATEDB REPLICATION BYPASSRLS LOGIN NOINHERIT CONNECTION LIMIT 0 PASSWORD 'attribute-not-inherited' VALID UNTIL '-infinity'"} {
						run(referenceExec(q, 0))
					}
					run(referenceExec("GRANT raa_power TO raa_member WITH INHERIT TRUE, SET TRUE", 0))
					if b.pg {
						roleAttributesFacts(t, p, "raa_member", roleAttributesDefaults(false))
					}
					run(referenceExec("SET SESSION AUTHORIZATION raa_member", 0))
					run(roleReferenceError("CREATE ROLE raa_denied", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles.", prepared, "execute"))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceExec("SET SESSION AUTHORIZATION raa_creator", 0))
					run(referenceExec("CREATE ROLE raa_ordinary", 0))
					if b.pg {
						var admin, inherit, set bool
						var grantor string
						err := referenceAdmin.QueryRowContext(p.ctx, `SELECT m.admin_option,m.inherit_option,m.set_option,g.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles a ON a.oid=m.member JOIN pg_roles g ON g.oid=m.grantor WHERE r.rolname='raa_ordinary' AND a.rolname='raa_creator'`).Scan(&admin, &inherit, &set, &grantor)
						if err != nil || !admin || inherit || set || grantor != "stuntgres" {
							t.Errorf("creator edge %v/%v/%v grantor %s: %v", admin, inherit, set, grantor, err)
						}
					}
					run(referenceExec("ALTER USER raa_ordinary LOGIN PASSWORD 'role-attributes-fixture'", 0))
					if targetReplication {
						roleAttributesRun(t, p, b, b.db, prepared, referenceExec("ALTER ROLE raa_ordinary REPLICATION", 0))
					}
					run(referenceExec("ALTER ROLE raa_ordinary NOLOGIN", 0))
					run(referenceExec("ALTER ROLE raa_ordinary PASSWORD NULL", 0))
					for _, q := range []string{"ALTER ROLE raa_other LOGIN", "ALTER ROLE raa_creator LOGIN"} {
						name := "raa_other"
						if q == "ALTER ROLE raa_creator LOGIN" {
							name = "raa_creator"
						}
						run(roleAttributesDenied(q, `Only roles with the CREATEROLE attribute and the ADMIN option on role "`+name+`" may alter this role.`, prepared))
					}
					run(referenceExec("ALTER ROLE CURRENT_USER PASSWORD 'self'", 0))
					run(referenceExec("ALTER USER CURRENT_ROLE PASSWORD NULL", 0))
					run(roleAttributesDenied("ALTER ROLE CURRENT_USER PASSWORD 'self' VALID UNTIL 'infinity'", `Only roles with the CREATEROLE attribute and the ADMIN option on role "raa_creator" may alter this role.`, prepared))
					run(roleAttributesDenied("ALTER ROLE raa_other PASSWORD NULL", "To change another role's password, the current user must have the CREATEROLE attribute and the ADMIN option on the role.", prepared))
					for _, toggle := range []string{"SUPERUSER", "NOSUPERUSER"} {
						run(roleAttributesDenied("ALTER ROLE raa_ordinary "+toggle, "Only roles with the SUPERUSER attribute may change the SUPERUSER attribute.", prepared))
					}
					bootstrap := "postgres"
					if b.pg {
						bootstrap = "stuntgres"
					}
					run(roleAttributesDenied("ALTER ROLE "+bootstrap+" LOGIN", "Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.", prepared))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					// ADMIN recursion is independent of INHERIT and SET.
					for _, q := range []string{"GRANT raa_other TO raa_mid WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "GRANT raa_mid TO raa_creator WITH INHERIT FALSE, SET FALSE"} {
						run(referenceExec(q, 0))
					}
					run(referenceExec("SET SESSION AUTHORIZATION raa_creator", 0))
					run(referenceExec("ALTER ROLE raa_other LOGIN", 0))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
			})
		}
	}
}
