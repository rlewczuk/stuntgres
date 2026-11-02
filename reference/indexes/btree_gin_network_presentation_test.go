package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINNetworkPresentation(t *testing.T) {
	for _, kind := range []string{"CLASS", "FAMILY", "EXTENSION"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"network_present_owner"})
			fixture.SchemaReferenceCleanup(t, "network_present_schema")
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				networkControlSeed(run)
				run(fixture.Exec("CREATE SCHEMA network_present_schema", 0))
				run(fixture.Exec("CREATE ROLE network_present_owner", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public,network_present_schema TO network_present_owner", 0))
				type identity struct {
					name, schema string
					oid, family  int64
				}
				var identities []identity
				type predicate struct {
					query    string
					held     fixture.HeldStatement
					ids      []int64
					boundary string
				}
				var predicates []predicate
				for _, tc := range networkCases {
					c := identity{name: tc.typ + "_ops", schema: "public"}
					if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint,opcfamily::bigint FROM pg_opclass WHERE opcmethod=2742 AND opcnamespace='public'::regnamespace AND opcname=$1", c.name).Scan(&c.oid, &c.family); err != nil {
						t.Fatal(err)
					}
					identities = append(identities, c)
					for i, op := range []string{"<", "<=", "=", ">=", ">"} {
						q := "SELECT id FROM compound_saved WHERE " + tc.typ + op + "$1::" + tc.typ + " ORDER BY id"
						want := [][]int64{{2}, {1, 2, 4}, {1, 4}, {1, 4}, nil}
						predicates = append(predicates, predicate{q, classHeld(t, p, conn, q), want[i], tc.boundary})
					}
				}
				check := func() {
					t.Helper()
					networkControlCheck(b, run)
					for _, pred := range predicates {
						s := ginIDs(pred.query, pred.ids, pred.boundary)
						classRoutes(t, p, b, conn, s)
						fixture.RunStep(t, p.Ctx, pred.held, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, pred.held, b.Recorder, ginIDs(pred.query, nil, nil))
					}
					for _, c := range identities {
						relation, prefix, oid := "pg_opclass", "opc", c.oid
						if kind == "FAMILY" {
							relation, prefix, oid = "pg_opfamily", "opf", c.family
						}
						classRoutes(t, p, b, conn, fixture.Select("SELECT o."+prefix+"name,n.nspname,o."+prefix+"method::bigint FROM "+relation+" o JOIN pg_namespace n ON n.oid=o."+prefix+"namespace WHERE o.oid=$1::oid", []string{prefix + "name", "nspname", prefix + "method"}, []string{"NAME", "NAME", "INT8"}, [][]any{{c.name, c.schema, int64(2742)}}, oid))
					}
				}
				check()
				if kind == "EXTENSION" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA network_present_schema", 0))
					for i := range identities {
						identities[i].schema = "network_present_schema"
					}
					check()
					networkControlDML(t, p, b, conn, run)
					run(fixture.Exec("ROLLBACK", 0))
					for i := range identities {
						identities[i].schema = "public"
					}
					check()
					run(fixture.Exec("ALTER EXTENSION btree_gin SET SCHEMA network_present_schema", 0))
					for i := range identities {
						identities[i].schema = "network_present_schema"
					}
					check()
					networkControlDML(t, p, b, conn, run)
				} else {
					for _, action := range []string{"rename", "owner", "schema"} {
						for i := range identities {
							c := &identities[i]
							clause := ""
							switch action {
							case "rename":
								clause = " RENAME TO " + fmt.Sprintf("network_current_%d", i)
							case "owner":
								clause = " OWNER TO network_present_owner"
							case "schema":
								clause = " SET SCHEMA network_present_schema"
							}
							q := "ALTER OPERATOR " + kind + " " + c.schema + "." + c.name + " USING gin" + clause
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec(q, 0))
							run(fixture.Exec("INSERT INTO compound_saved(id,inet) VALUES(6,'10.0.0.0/24')", 1))
							run(ginIDs("SELECT id FROM compound_saved WHERE id=6 ORDER BY id", []int64{6}))
							run(fixture.Exec("ROLLBACK", 0))
							check()
							run(fixture.Exec(q, 0))
							if action == "rename" {
								c.name = fmt.Sprintf("network_current_%d", i)
							}
							if action == "schema" {
								c.schema = "network_present_schema"
							}
							if action == "owner" {
								relation, prefix, oid := "pg_opclass", "opc", c.oid
								if kind == "FAMILY" {
									relation, prefix, oid = "pg_opfamily", "opf", c.family
								}
								run(fixture.Select("SELECT "+prefix+"owner=(SELECT oid FROM pg_roles WHERE rolname='network_present_owner') AS owned FROM "+relation+" WHERE oid=$1::oid", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							check()
							networkControlDML(t, p, b, conn, run)
						}
					}
				}
				// A real helper owner change refreshes captures without changing execution.
				schema := "public"
				if kind == "EXTENSION" {
					schema = "network_present_schema"
				}
				for _, tc := range networkCases {
					run(fixture.Exec("ALTER FUNCTION "+schema+".gin_extract_value_"+tc.typ+"("+tc.typ+",internal) OWNER TO network_present_owner", 0))
					check()
				}
			})
		})
	}
}
