package reference_test

import (
	"fmt"
	"testing"
)

func roleAttributesAuthorityQuestions(t *testing.T, prepared bool) {
	for _, ownsReplication := range []bool{false, true} {
		for _, replicationTarget := range []bool{false, true} {
			for _, option := range []string{"LOGIN", "NOLOGIN", "PASSWORD 'x'", "CREATEDB", "NOCREATEDB", "REPLICATION", "NOREPLICATION"} {
				t.Run(fmt.Sprintf("replicationTarget/%v/actorReplication%v/%s/prepared%v", replicationTarget, ownsReplication, option, prepared), func(t *testing.T) {
					roleReferenceCleanup(t, []string{"raq_actor", "raq_target"})
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						c, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
						run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
						actorOptions := "CREATEROLE CREATEDB"
						if ownsReplication {
							actorOptions += " REPLICATION"
						}
						run(referenceExec("CREATE ROLE raq_actor "+actorOptions, 0))
						run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
						run(referenceExec("CREATE ROLE raq_target", 0))
						run(referenceExec("RESET SESSION AUTHORIZATION", 0))
						if replicationTarget {
							run(referenceExec("ALTER ROLE raq_target REPLICATION", 0))
						}
						run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
						query := "ALTER ROLE raq_target " + option
						if !ownsReplication && (option == "REPLICATION" || option == "NOREPLICATION") {
							run(roleAttributesDenied(query, "Only roles with the REPLICATION attribute may change the REPLICATION attribute.", prepared))
						} else {
							run(referenceExec(query, 0))
						}
					}
				})
			}
		}
	}
	for _, label := range []string{"admin-only", "createrole-only", "effective-self", "superuser-not-inherited", "inherit-nonretroactive"} {
		t.Run(fmt.Sprintf("authorityQuestion/%s/prepared%v", label, prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"raq_actor", "raq_target", "raq_super", "raq_denied"})
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				c, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
				run := func(s referenceStep) { roleAttributesRun(t, p, b, c, prepared, s) }
				actorOptions := ""
				if label == "createrole-only" {
					actorOptions = " CREATEROLE"
				}
				run(referenceExec("CREATE ROLE raq_actor"+actorOptions, 0))
				run(referenceExec("CREATE ROLE raq_target", 0))
				run(referenceExec("CREATE ROLE raq_super SUPERUSER", 0))
				if label == "admin-only" {
					run(referenceExec("GRANT raq_target TO raq_actor WITH ADMIN TRUE", 0))
				}
				if label == "superuser-not-inherited" {
					run(referenceExec("GRANT raq_super TO raq_actor WITH INHERIT TRUE", 0))
				}
				if label == "effective-self" {
					run(referenceExec("SET ROLE raq_actor", 0))
					run(referenceExec("ALTER ROLE CURRENT_USER PASSWORD 'self-effective'", 0))
					run(referenceExec("ALTER USER CURRENT_ROLE PASSWORD NULL", 0))
					run(roleAttributesDenied("ALTER ROLE SESSION_USER PASSWORD NULL", "Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.", prepared))
					run(referenceExec("RESET ROLE", 0))
				} else if label == "inherit-nonretroactive" {
					if b.pg {
						if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO raq_target; GRANT USAGE ON SCHEMA public TO raq_actor"); err != nil {
							t.Fatal(err)
						}
					}
					for _, q := range []string{
						"SET SESSION AUTHORIZATION raq_target",
						"CREATE TABLE raq_old_edge(id INT)",
						"INSERT INTO raq_old_edge VALUES(7)",
						"RESET SESSION AUTHORIZATION",
						"SET SESSION AUTHORIZATION raq_super",
						"CREATE TABLE raq_new_edge(id INT)",
						"INSERT INTO raq_new_edge VALUES(9)",
						"RESET SESSION AUTHORIZATION",
					} {
						count := int64(0)
						if q == "INSERT INTO raq_old_edge VALUES(7)" || q == "INSERT INTO raq_new_edge VALUES(9)" {
							count = 1
						}
						run(referenceExec(q, count))
					}
					run(referenceExec("GRANT raq_target TO raq_actor", 0))
					oldRead := referenceSelect("SELECT id FROM raq_old_edge", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
					newRead := referenceSelect("SELECT id FROM raq_new_edge", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
					newDenied := roleReferenceError("SELECT id FROM raq_new_edge", "42501", "permission denied for table raq_new_edge", "", prepared, "execute")
					newDenied.selectRows = true
					run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
					run(oldRead)
					run(newDenied)
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceExec("ALTER ROLE raq_actor NOINHERIT", 0))
					run(referenceExec("GRANT raq_super TO raq_actor", 0))
					if b.pg {
						var inherit bool
						if err := referenceAdmin.QueryRowContext(p.ctx, `SELECT m.inherit_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles a ON a.oid=m.member WHERE r.rolname='raq_target' AND a.rolname='raq_actor'`).Scan(&inherit); err != nil || !inherit {
							t.Errorf("role INHERIT rewrote existing edge: %v %v", inherit, err)
						}
						if err := referenceAdmin.QueryRowContext(p.ctx, `SELECT m.inherit_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles a ON a.oid=m.member WHERE r.rolname='raq_super' AND a.rolname='raq_actor'`).Scan(&inherit); err != nil || inherit {
							t.Errorf("new edge ignored NOINHERIT: %v %v", inherit, err)
						}
					}
					run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
					run(oldRead)   // Changing the attribute retained the old edge.
					run(newDenied) // The new edge used the member's NOINHERIT default.
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceExec("ALTER ROLE raq_actor INHERIT", 0))
					run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
					run(oldRead)
					run(newDenied) // Restoring INHERIT does not rewrite that edge either.
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceExec("GRANT raq_super TO raq_actor WITH INHERIT TRUE", 0))
					run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
					run(newRead) // Explicit edge change, not the SUPERUSER attribute.
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				} else {
					run(referenceExec("SET SESSION AUTHORIZATION raq_actor", 0))
					run(roleAttributesDenied("ALTER ROLE raq_target LOGIN", `Only roles with the CREATEROLE attribute and the ADMIN option on role "raq_target" may alter this role.`, prepared))
					if label != "createrole-only" {
						run(roleReferenceError("CREATE ROLE raq_denied", "42501", "permission denied to create role", "Only roles with the CREATEROLE attribute may create roles.", prepared, "execute"))
					}
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				}
			}
		})
	}
}
