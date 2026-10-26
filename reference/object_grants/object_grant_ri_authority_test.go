package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRIRoutineAuthority(t *testing.T) {
	for _, action := range []string{"delete", "update"} {
		for _, ownerOnly := range []bool{true, false} {
			direction := map[bool]string{true: "child-owner-only", false: "initiator-only"}[ownerOnly]
			t.Run(action+"/"+direction, func(t *testing.T) {
				objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
					defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.json_array_length(json)"})
					objectGrantConsumerACL(run)
					run(fixture.Exec("CREATE TABLE rg_parent(id integer PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE rg_child(id integer,pid integer DEFAULT pg_catalog.json_array_length('[1]'::json) CONSTRAINT rg_fk REFERENCES rg_parent(id) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT)", 0))
					objectGrantConsumerOwner(run, "rg_child")
					run(fixture.Exec("GRANT SELECT,DELETE,UPDATE ON rg_parent TO og_object_reader", 0))
					grantee := "og_object_reader"
					if ownerOnly {
						grantee = "og_object_other"
					}
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO "+grantee, 0))
					query := "DELETE FROM rg_parent WHERE id=$1"
					parents := [][]any{{int64(1)}}
					if action == "update" {
						query = "UPDATE rg_parent SET id=3 WHERE id=$1"
						parents = [][]any{{int64(1)}, {int64(3)}}
					}
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					read := func(success bool) {
						wantParents := [][]any{{int64(1)}, {int64(2)}}
						pid := int64(2)
						if success {
							wantParents, pid = parents, 1
						}
						run(fixture.Select("SELECT id FROM rg_parent ORDER BY id", []string{"id"}, []string{"INT4"}, wantParents))
						run(fixture.Select("SELECT id,pid FROM rg_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), pid}}))
					}
					// Each route has an actual matching child and a cold RI action.
					// Drop/recreate the child between ordinary and held execution;
					// otherwise a warmed SPI DEFAULT could hide first-use authority.
					for _, useHeld := range []bool{false, true} {
						run(fixture.Exec("INSERT INTO rg_parent VALUES(1),(2)", 2))
						run(fixture.Exec("INSERT INTO rg_child VALUES(10,2)", 1))
						execute := func(s fixture.Step) {
							fixture.RoleGrantActor(run, "og_object_reader", func() {
								if useHeld {
									objectGrantStep(t, p.Ctx, held, b.Recorder, s)
								} else {
									run(s)
								}
								run(objectGrantConsumerIdentity())
							})
						}
						if !ownerOnly {
							execute(objectGrantConsumerDenied(query, `SQL statement "UPDATE ONLY "public"."rg_child" SET "pid" = DEFAULT WHERE $1 OPERATOR(pg_catalog.=) "pid""`, int64(2)))
							read(false)
							run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO og_object_other", 0))
						}
						execute(fixture.Exec(query, 1, int64(2)))
						read(true)
						run(fixture.Exec("DROP TABLE rg_child", 0))
						run(fixture.Exec("DELETE FROM rg_parent", int64(len(parents))))
						if !useHeld {
							run(fixture.Exec("CREATE TABLE rg_child(id integer,pid integer DEFAULT pg_catalog.json_array_length('[1]'::json) CONSTRAINT rg_fk REFERENCES rg_parent(id) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT)", 0))
							objectGrantConsumerOwner(run, "rg_child")
							if !ownerOnly {
								run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM og_object_other", 0))
							}
						}
					}
				})
			})
		}
	}
}
