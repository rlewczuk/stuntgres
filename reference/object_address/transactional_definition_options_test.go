package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionOptions(t *testing.T) {
	definitionRoutes(t, func(t *testing.T, r definitionRoute) {
		definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queries := []string{
				`CREATE OPERATOR FAMILY public.tx_option_family USING btree`,
				`CREATE OPERATOR FAMILY public.tx_option_family USING gist`,
				`ALTER OPERATOR FAMILY public.tx_option_family USING gist ADD OPERATOR 1 pg_catalog.<(integer,integer) FOR ORDER BY public.tx_option_family, OPERATOR 2 pg_catalog.=(integer,integer) FOR SEARCH, FUNCTION 1 (integer,integer) pg_catalog.int4mi(integer,integer)`,
				`CREATE OPERATOR CLASS public.tx_option_gist FOR TYPE inet USING gist AS STORAGE bigint`,
				`CREATE OPERATOR CLASS public.tx_option_hash FOR TYPE integer USING hash AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer), FUNCTION 2 (integer,integer) pg_catalog.hashint4extended(integer,bigint)`,
				`ALTER OPERATOR FAMILY public.tx_option_family USING gist OWNER TO CURRENT_USER`,
				`ALTER OPERATOR CLASS public.tx_option_hash USING hash OWNER TO CURRENT_USER`,
				`ALTER OPERATOR FAMILY public.tx_option_family USING gist DROP OPERATOR 1(integer,integer), OPERATOR 2(integer), FUNCTION 1(integer)`,
				`DROP OPERATOR FAMILY public.tx_option_family USING gist RESTRICT`,
			}
			command := r.commands(t, p, b, c, queries)
			e, end := r.begin(t, p, b, c)
			run := definitionRun(t, p, b, e)
			for _, q := range queries[:7] {
				command(e, q)
			}
			sort := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_option_family' AND opfmethod=403`)
			run(fixture.Select(`SELECT a.amopstrategy,a.amoppurpose,a.amopmethod::bigint,a.amopsortfamily::bigint,a.amopopr::bigint FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='tx_option_family' AND f.opfmethod=783 ORDER BY a.amopstrategy`, []string{"amopstrategy", "amoppurpose", "amopmethod", "amopsortfamily", "amopopr"}, []string{"INT2", "CHAR", "INT8", "INT8", "INT8"}, [][]any{{int64(1), "o", int64(783), sort, int64(97)}, {int64(2), "s", int64(783), int64(0), int64(96)}}))
			run(fixture.Select(`SELECT opcname,opcmethod::bigint,opcintype::bigint,opckeytype::bigint,opcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) FROM pg_opclass WHERE opcname IN ('tx_option_gist','tx_option_hash') ORDER BY opcname`, []string{"opcname", "opcmethod", "opcintype", "opckeytype", "?column?"}, []string{"NAME", "INT8", "INT8", "INT8", "BOOL"}, [][]any{{"tx_option_gist", int64(783), int64(869), int64(20), true}, {"tx_option_hash", int64(405), int64(23), int64(0), true}}))
			run(fixture.Select(`SELECT a.amprocnum,a.amproclefttype::bigint,a.amprocrighttype::bigint,a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcname='tx_option_hash' ORDER BY a.amprocnum`, []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(23), int64(23), int64(450)}, {int64(2), int64(23), int64(23), int64(425)}}))
			if r.mode != "autocommit" {
				run(fixture.Exec(`SAVEPOINT required_member`, 0))
			}
			want := fixture.Diagnostic{Code: "2BP01", Message: `cannot drop function 1 (integer, integer) of operator family tx_option_family for access method gist: int4mi(integer,integer) because operator family tx_option_family for access method gist requires it`, Hint: `You can drop operator family tx_option_family for access method gist instead.`}
			failure := fixture.ImplicitError(queries[7], want)
			failure.ErrorStage = "execute"
			if r.held {
				fixture.RunStep(t, p.Ctx, definitionHeld(t, p, e, queries[7]), b.Recorder, failure)
			} else {
				run(failure)
			}
			if r.mode != "autocommit" {
				run(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
				run(fixture.Exec(`ROLLBACK TO required_member`, 0))
				run(fixture.Exec(`RELEASE required_member`, 0))
			}
			run(definitionCount(`SELECT count(*) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='tx_option_family' AND f.opfmethod=783`, 2))
			run(definitionCount(`SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_option_family' AND f.opfmethod=783`, 1))
			gistFamily := definitionID(t, p, e, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_option_family' AND opfmethod=783`)
			run(fixture.Select(`SELECT d.refclassid::bigint,d.refobjid::bigint,d.deptype FROM pg_depend d JOIN pg_amproc a ON d.classid=2603 AND d.objid=a.oid WHERE a.amprocfamily=$1::oid AND d.deptype='i'`, []string{"refclassid", "refobjid", "deptype"}, []string{"INT8", "INT8", "CHAR"}, [][]any{{int64(2753), gistFamily, "i"}}, gistFamily))
			command(e, queries[8])
			run(definitionCount(`SELECT count(*) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='tx_option_family' AND f.opfmethod=783`, 0))
			run(definitionCount(`SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_option_family' AND f.opfmethod=783`, 0))
			if r.mode != "autocommit" {
				definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_option_gist','tx_option_hash')`, 0))
			}
			end()
			n := int64(0)
			if r.commit {
				n = 2
			}
			definitionRun(t, p, b, c)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_option_gist','tx_option_hash')`, n))
		})
	})
}
