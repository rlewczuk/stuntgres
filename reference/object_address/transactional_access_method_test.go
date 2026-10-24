package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalAccessMethodDefinitions(t *testing.T) {
	for _, tc := range []struct {
		kind, handler, amtype string
		oid                   int64
	}{{"INDEX", "bthandler", "i", 330}, {"INDEX", "hashhandler", "i", 331}, {"INDEX", "gisthandler", "i", 332}, {"TABLE", "heap_tableam_handler", "t", 3}} {
		t.Run(tc.handler, func(t *testing.T) {
			definitionRoutes(t, func(t *testing.T, r definitionRoute) {
				definitionScenario(t, []string{"tx_definition_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					kind, handler, amtype, handlerID := tc.kind, tc.handler, tc.amtype, tc.oid
					create := `CREATE ACCESS METHOD tx_definition_am TYPE ` + kind + ` HANDLER ` + handler
					drop := `DROP ACCESS METHOD tx_definition_am RESTRICT`
					command := r.commands(t, p, b, c, []string{create, drop})
					run := definitionRun(t, p, b, c)
					absent := definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_definition_am'`, 0)
					run(absent)
					e, end := r.begin(t, p, b, c)
					command(e, create)
					id := definitionID(t, p, e, `SELECT oid::bigint FROM pg_am WHERE amname='tx_definition_am'`)
					if id == 403 || id == 405 || id == 783 || id == 2 {
						t.Fatal("alias reused builtin SQL identity", id)
					}
					check := fixture.Select(`SELECT amhandler::oid::bigint,amtype FROM pg_am WHERE amname='tx_definition_am'`, []string{"amhandler", "amtype"}, []string{"INT8", "CHAR"}, [][]any{{handlerID, amtype}})
					definitionRun(t, p, b, e)(check)
					definitionRun(t, p, b, e)(fixture.Select(`SELECT type,schema,name,identity FROM pg_identify_object('pg_am'::regclass,$1::oid,0)`, []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"access method", nil, "tx_definition_am", "tx_definition_am"}}, id))
					if r.mode != "autocommit" {
						definitionRun(t, p, b, b.DB)(absent)
					}
					end()
					present := int64(0)
					if r.commit {
						present = 1
					}
					definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_definition_am'`, present))
					if !r.commit {
						run(fixture.Exec(create, 0))
						id = definitionID(t, p, c, `SELECT oid::bigint FROM pg_am WHERE amname='tx_definition_am'`)
					}
					e, end = r.begin(t, p, b, c)
					command(e, drop)
					definitionRun(t, p, b, e)(absent)
					if r.mode != "autocommit" {
						definitionRun(t, p, b, b.DB)(check)
					}
					end()
					if !r.commit {
						if got := definitionID(t, p, c, `SELECT oid::bigint FROM pg_am WHERE amname='tx_definition_am'`); got != id {
							t.Fatal("rollback changed method identity", id, got)
						}
						run(fixture.Exec(drop, 0))
					}
					run(absent)
					command(c, create)
					fresh := definitionID(t, p, c, `SELECT oid::bigint FROM pg_am WHERE amname='tx_definition_am'`)
					if fresh == id {
						t.Fatal("drop/recreate reused method identity", id)
					}
					command(c, drop)
				})
			})
		})
	}
}
