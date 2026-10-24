package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionProviders(t *testing.T) {
	definitionRoutes(t, func(t *testing.T, r definitionRoute) {
		definitionScenario(t, []string{"tx_provider_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := definitionRun(t, p, b, c)
			run(fixture.Exec(`CREATE TABLE tx_provider_rows(n integer)`, 0))
			run(fixture.Exec(`INSERT INTO tx_provider_rows VALUES(7),(NULL)`, 2))
			queries := []string{`CREATE ACCESS METHOD tx_provider_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.tx_provider_family USING tx_provider_am`, `CREATE OPERATOR CLASS public.tx_provider_class DEFAULT FOR TYPE integer USING tx_provider_am FAMILY public.tx_provider_family AS ` + definitionClassItems, `CREATE INDEX tx_provider_index ON tx_provider_rows USING tx_provider_am(n)`}
			command := r.commands(t, p, b, c, queries)
			e, end := r.begin(t, p, b, c)
			execute := definitionRun(t, p, b, e)
			for _, q := range queries {
				command(e, q)
			}
			method := definitionID(t, p, e, `SELECT oid::bigint FROM pg_am WHERE amname='tx_provider_am'`)
			class := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_provider_class'`)
			family := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_provider_family'`)
			index := definitionID(t, p, e, `SELECT oid::bigint FROM pg_class WHERE relname='tx_provider_index'`)
			execute(definitionIndexProvider(index, class, method))
			execute(fixture.Select(`SELECT amproc::oid::bigint,amproclefttype::bigint,amprocrighttype::bigint FROM pg_amproc WHERE amprocfamily=$1::oid AND amprocnum=1`, []string{"amproc", "amproclefttype", "amprocrighttype"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(351), int64(23), int64(23)}}, family))
			execute(fixture.Select(`SELECT pg_indexam_has_property($1::oid,'can_order') AS can_order`, []string{"can_order"}, []string{"BOOL"}, [][]any{{true}}, method))
			execute(fixture.Exec(`INSERT INTO tx_provider_rows VALUES(3)`, 1))
			execute(fixture.Select(`SELECT n FROM tx_provider_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(7)}, {nil}}))
			if r.mode != "autocommit" {
				definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_provider_index'`, 0))
			}
			end()
			count := int64(0)
			if r.commit {
				count = 1
			}
			run(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_provider_index'`, count))
			if r.commit {
				// A real committed consumer is a NORMAL dependency, unlike empty AUTO trees.
				s := fixture.ImplicitError(`DROP OPERATOR CLASS public.tx_provider_class USING tx_provider_am RESTRICT`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator class tx_provider_class for access method tx_provider_am because other objects depend on it`, Detail: `index tx_provider_index depends on operator class tx_provider_class for access method tx_provider_am`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec(`DROP ACCESS METHOD tx_provider_am CASCADE`, 0))
				run(definitionCount(`SELECT count(*) FROM pg_class WHERE relname='tx_provider_index'`, 0))
				run(fixture.Select(`SELECT n FROM tx_provider_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(7)}, {nil}}))
			} else {
				run(fixture.Select(`SELECT n FROM tx_provider_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			}
		})
	})
	t.Run("late_class_failure", func(t *testing.T) {
		definitionScenario(t, []string{"tx_atomic_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := definitionRun(t, p, b, c)
			run(fixture.Exec(`CREATE ACCESS METHOD tx_atomic_am TYPE INDEX HANDLER bthandler`, 0))
			bad := `CREATE OPERATOR CLASS public.tx_atomic_class DEFAULT FOR TYPE integer USING tx_atomic_am AS OPERATOR 1 pg_catalog.<, FUNCTION 1 pg_catalog.btint4cmp(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
			good := `CREATE OPERATOR CLASS public.tx_atomic_class DEFAULT FOR TYPE integer USING tx_atomic_am AS ` + definitionClassItems
			for _, mode := range []string{"autocommit", "sql", "go"} {
				for _, held := range []bool{false, true} {
					r := definitionRoute{mode: mode, commit: false}
					h := definitionHeld(t, p, c, bad)
					e, end := r.begin(t, p, b, c)
					execute := definitionRun(t, p, b, e)
					if mode != "autocommit" {
						execute(fixture.Exec(`SAVEPOINT atomic`, 0))
					}
					s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42P17", Message: `function number 1 for (integer,integer) appears more than once`})
					s.ErrorStage = "execute"
					if held {
						fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
					} else {
						execute(s)
					}
					if mode != "autocommit" {
						execute(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
						execute(fixture.Exec(`ROLLBACK TO atomic`, 0))
					}
					execute(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_atomic_class'`, 0))
					execute(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_atomic_class'`, 0))
					execute(fixture.Exec(good, 0))
					execute(fixture.Exec(`DROP OPERATOR FAMILY public.tx_atomic_class USING tx_atomic_am RESTRICT`, 0))
					end()
				}
			}
		})
	})
}
