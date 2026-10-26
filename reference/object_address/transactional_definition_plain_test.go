package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionPlain(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("tx_%t/held_%t", explicit, held), func(t *testing.T) {
				definitionScenario(t, []string{"tx_plain_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
					var e fixture.Executor = b.DB
					var tx *sql.Tx
					if explicit {
						var err error
						tx, err = b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						e = tx
					}
					run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					exec := func(q string) {
						s := fixture.Exec(q, 0)
						s.NonContext = true
						if !held {
							run(s)
							return
						}
						source := e.(fixture.NonContextExecutor)
						stmt, err := source.Prepare(q)
						if err != nil {
							t.Fatal("plain quiet Prepare", q, err)
						}
						defer stmt.Close()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, s)
					}
					exec(`CREATE ACCESS METHOD tx_plain_am TYPE INDEX HANDLER bthandler`)
					exec(`CREATE OPERATOR FAMILY public.tx_plain_family USING tx_plain_am`)
					exec(`CREATE OPERATOR CLASS public.tx_plain_class DEFAULT FOR TYPE integer USING tx_plain_am FAMILY public.tx_plain_family AS ` + definitionClassItems)
					run(fixture.Select(`SELECT a.amhandler::oid::bigint,c.opcintype::bigint,c.opcdefault,c.opcmethod=a.oid,c.opcfamily=f.oid FROM pg_opclass c JOIN pg_am a ON a.amname='tx_plain_am' JOIN pg_opfamily f ON f.opfname='tx_plain_family' WHERE c.opcname='tx_plain_class'`, []string{"amhandler", "opcintype", "opcdefault", "?column?", "?column?"}, []string{"INT8", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(330), int64(23), true, true, true}}))
					exec(`ALTER OPERATOR FAMILY public.tx_plain_family USING tx_plain_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`)
					visible := int64(1)
					if b.PG {
						visible = 2
					}
					run(definitionCount(`SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_plain_family'`, visible))
					exec(`ALTER OPERATOR FAMILY public.tx_plain_family USING tx_plain_am DROP FUNCTION 2(integer,integer)`)
					exec(`ALTER OPERATOR CLASS public.tx_plain_class USING tx_plain_am RENAME TO tx_plain_renamed`)
					exec(`ALTER OPERATOR FAMILY public.tx_plain_family USING tx_plain_am RENAME TO tx_plain_family_renamed`)
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_plain_renamed'`, 1))
					exec(`DROP OPERATOR CLASS public.tx_plain_renamed USING tx_plain_am`)
					exec(`DROP OPERATOR FAMILY public.tx_plain_family_renamed USING tx_plain_am`)
					exec(`DROP ACCESS METHOD tx_plain_am`)
					run(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_plain_am'`, 0))
					if explicit {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					}
					s := definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_plain_class','tx_plain_renamed')`, 0)
					s.NonContext = true
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				})
			})
		}
	}
}
