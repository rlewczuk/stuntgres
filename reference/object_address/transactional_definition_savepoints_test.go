package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionSavepoints(t *testing.T) {
	for _, mode := range []string{"sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tx_save_dest")
				definitionScenario(t, []string{"tx_save_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := definitionRun(t, p, b, c)
					run(fixture.Exec(`CREATE SCHEMA tx_save_dest`, 0))
					queries := []string{
						`CREATE ACCESS METHOD tx_save_am TYPE INDEX HANDLER bthandler`,
						`CREATE OPERATOR FAMILY public.tx_save_family USING tx_save_am`,
						`CREATE OPERATOR CLASS public.tx_save_class DEFAULT FOR TYPE integer USING tx_save_am FAMILY public.tx_save_family AS ` + definitionClassItems,
						`ALTER OPERATOR FAMILY public.tx_save_family USING tx_save_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
						`ALTER OPERATOR CLASS public.tx_save_class USING tx_save_am RENAME TO tx_save_renamed`,
						`ALTER OPERATOR CLASS public.tx_save_renamed USING tx_save_am SET SCHEMA tx_save_dest`,
						`DROP OPERATOR FAMILY public.tx_save_family USING tx_save_am CASCADE`,
					}
					r := definitionRoute{mode: mode, held: held, commit: false}
					command := r.commands(t, p, b, c, queries)
					e, end := r.begin(t, p, b, c)
					execute := definitionRun(t, p, b, e)
					for _, q := range queries[:3] {
						command(e, q)
					}
					method := definitionID(t, p, e, `SELECT oid::bigint FROM pg_am WHERE amname='tx_save_am'`)
					family := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_save_family'`)
					class := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_save_class'`)
					member := definitionID(t, p, e, `SELECT oid::bigint FROM pg_amproc WHERE amprocfamily=$1::oid AND amprocnum=1`, family)
					check := func() {
						execute(fixture.Select(`SELECT c.oid::bigint,c.opcmethod::bigint,c.opcfamily::bigint,c.opcdefault,c.opcnamespace='public'::regnamespace FROM pg_opclass c WHERE opcname='tx_save_class'`, []string{"oid", "opcmethod", "opcfamily", "opcdefault", "?column?"}, []string{"INT8", "INT8", "INT8", "BOOL", "BOOL"}, [][]any{{class, method, family, true, true}}))
						execute(fixture.Select(`SELECT oid::bigint,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=$1::oid AND amprocnum=1`, []string{"oid", "amproc"}, []string{"INT8", "INT8"}, [][]any{{member, int64(351)}}, family))
						execute(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid AND amprocnum=2`, 0, family))
						execute(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_save_renamed'`, 0))
					}
					execute(fixture.Exec(`SAVEPOINT graph`, 0))
					for _, q := range queries[3:6] {
						command(e, q)
					}
					command(e, queries[6])
					command(e, queries[1])
					command(e, queries[2])
					fresh := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opclass WHERE opcname='tx_save_class'`)
					if fresh == class {
						t.Fatal("recreation reused old class")
					}
					execute(fixture.Exec(`ROLLBACK TO graph`, 0))
					check()
					execute(fixture.Exec(`ROLLBACK TO graph`, 0))
					check()
					// Late failure must retain neither the first valid item nor an implicit family.
					bad := `ALTER OPERATOR FAMILY public.tx_save_family USING tx_save_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
					failure := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42710", Message: `function 1(integer,integer) already exists in operator family "public.tx_save_family"`})
					failure.ErrorStage = "execute"
					if held {
						fixture.RunStep(t, p.Ctx, definitionHeld(t, p, e, bad), b.Recorder, failure)
					} else {
						execute(failure)
					}
					execute(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
					execute(fixture.Exec(`ROLLBACK TO graph`, 0))
					check()
					command(e, queries[3])
					execute(fixture.Exec(`SAVEPOINT drop_atomic`, 0))
					badDrop := `ALTER OPERATOR FAMILY public.tx_save_family USING tx_save_am DROP FUNCTION 2(integer), FUNCTION 6(integer)`
					dropFailure := fixture.ImplicitError(badDrop, fixture.Diagnostic{Code: "42704", Message: `function 6(integer,integer) does not exist in operator family "public.tx_save_family"`})
					dropFailure.ErrorStage = "execute"
					if held {
						fixture.RunStep(t, p.Ctx, definitionHeld(t, p, e, badDrop), b.Recorder, dropFailure)
					} else {
						execute(dropFailure)
					}
					execute(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
					execute(fixture.Exec(`ROLLBACK TO drop_atomic`, 0))
					execute(fixture.Exec(`RELEASE drop_atomic`, 0))
					execute(fixture.Exec(`RELEASE graph`, 0))
					execute(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 2, family))
					definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_save_am'`, 0))
					end()
					run(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_save_am'`, 0))
					run(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_save_family'`, 0))
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_save_class','tx_save_renamed')`, 0))
					for _, q := range queries[:3] {
						command(c, q)
					}
					run(fixture.Exec(`DROP ACCESS METHOD tx_save_am RESTRICT`, 0))
				})
			})
		}
	}
}
