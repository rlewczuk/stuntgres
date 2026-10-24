package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionSchemaAuthority(t *testing.T) {
	for _, kind := range []string{"FAMILY", "CLASS"} {
		for _, mode := range []string{"autocommit", "sql", "go"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"tx_schema_actor"})
				fixture.SchemaReferenceCleanup(t, "tx_schema_dest")
				definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := definitionRun(t, p, b, c)
					peer := definitionRun(t, p, b, b.DB)
					for _, q := range []string{`CREATE ROLE tx_schema_actor`, `CREATE SCHEMA tx_schema_dest`, `GRANT USAGE,CREATE ON SCHEMA public,tx_schema_dest TO tx_schema_actor`} {
						run(fixture.Exec(q, 0))
					}
					create := `CREATE OPERATOR ` + kind + ` public.tx_schema_definition`
					if kind == "CLASS" {
						create += ` FOR TYPE integer`
					}
					create += ` USING btree`
					if kind == "CLASS" {
						create += ` AS STORAGE integer`
					}
					run(fixture.Exec(create, 0))
					run(fixture.Exec(`ALTER OPERATOR `+kind+` public.tx_schema_definition USING btree OWNER TO tx_schema_actor`, 0))
					table, col, nsp := "pg_opfamily", "opfname", "opfnamespace"
					if kind == "CLASS" {
						table = "pg_opclass"
						col = "opcname"
						nsp = "opcnamespace"
					}
					id := definitionID(t, p, c, `SELECT oid::bigint FROM `+table+` WHERE `+col+`='tx_schema_definition'`)
					run(fixture.Exec(`SET ROLE tx_schema_actor`, 0))
					q := `ALTER OPERATOR ` + kind + ` public.tx_schema_definition USING btree SET SCHEMA tx_schema_dest`
					held := definitionHeld(t, p, c, q)
					r := definitionRoute{mode: mode, commit: true}
					e, end := r.begin(t, p, b, c)
					execute := definitionRun(t, p, b, e)
					if mode != "autocommit" {
						execute(fixture.Exec(`SAVEPOINT privilege`, 0))
					}
					peer(fixture.Exec(`REVOKE CREATE ON SCHEMA tx_schema_dest FROM tx_schema_actor`, 0))
					failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: `permission denied for schema tx_schema_dest`})
					failure.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
					if mode != "autocommit" {
						execute(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
						execute(fixture.Exec(`ROLLBACK TO privilege`, 0))
					}
					execute(fixture.Select(`SELECT oid::bigint,`+nsp+`='public'::regnamespace FROM `+table+` WHERE `+col+`='tx_schema_definition'`, []string{"oid", "?column?"}, []string{"INT8", "BOOL"}, [][]any{{id, true}}))
					peer(fixture.Exec(`GRANT CREATE ON SCHEMA tx_schema_dest TO tx_schema_actor`, 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
					execute(fixture.Select(`SELECT oid::bigint,`+nsp+`='tx_schema_dest'::regnamespace FROM `+table+` WHERE `+col+`='tx_schema_definition'`, []string{"oid", "?column?"}, []string{"INT8", "BOOL"}, [][]any{{id, true}}))
					end()
					run(fixture.Exec(`RESET ROLE`, 0))
				})
			})
		}
	}
}
