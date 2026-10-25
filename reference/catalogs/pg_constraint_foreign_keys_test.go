package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintForeignKeys(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for i, tc := range []struct{ parent, child, pf, pp, ff string }{{"INT", "INT", "96", "96", "96"}, {"INT", "BIGINT", "15", "96", "410"}, {"BIGINT", "INT", "416", "410", "96"}, {"SMALLINT", "INT", "532", "94", "96"}, {"INT", "SMALLINT", "533", "96", "94"}} {
			parent, child := fmt.Sprintf("pc_fk_p%d", i), fmt.Sprintf("pc_fk_c%d", i)
			fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE "+parent+"(id "+tc.parent+" PRIMARY KEY)", "CREATE TABLE "+child+"(id "+tc.child+",CONSTRAINT pc_fk FOREIGN KEY(id) REFERENCES "+parent+"(id))")
			pgConstraintFK(t, p, b, child, parent, "pc_fk", "{1}", "{1}", "{"+tc.pf+"}", "{"+tc.pp+"}", "{"+tc.ff+"}", "a", "a", "s", nil, true)
		}
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc_fk_pair(a INT,b BIGINT,CONSTRAINT pc_fk_pair_u UNIQUE(a,b))", "CREATE TABLE pc_fk_reordered(x BIGINT,y INT,CONSTRAINT pc_pair FOREIGN KEY(y,x) REFERENCES pc_fk_pair(b,a) MATCH FULL ON UPDATE CASCADE ON DELETE SET NULL(x))")
		pgConstraintFK(t, p, b, "pc_fk_reordered", "pc_fk_pair", "pc_pair", "{2,1}", "{2,1}", "{416,15}", "{410,96}", "{96,410}", "c", "n", "f", "{1}", true)
		for i, tc := range []struct{ clause, action string }{{"NO ACTION", "a"}, {"RESTRICT", "r"}, {"CASCADE", "c"}, {"SET NULL", "n"}, {"SET DEFAULT", "d"}} {
			child := fmt.Sprintf("pc_fk_action%d", i)
			fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE "+child+"(id INT)", "ALTER TABLE "+child+" ADD CONSTRAINT pc_action FOREIGN KEY(id) REFERENCES pc_fk_p0 ON UPDATE "+tc.clause+" ON DELETE "+tc.clause+" NOT VALID")
			pgConstraintFK(t, p, b, child, "pc_fk_p0", "pc_action", "{1}", "{1}", "{96}", "{96}", "{96}", tc.action, tc.action, "s", nil, false)
		}
		fixture.PGClassExec(t, p, b, b.DB, "CREATE DOMAIN pc_fk_domain AS INT", "CREATE TYPE pc_fk_enum AS ENUM ('one','two')")
		for i, tc := range []struct{ typ, op string }{{"pc_fk_domain", "96"}, {"VARCHAR(12)", "98"}, {"pc_fk_enum", "3516"}} {
			parent, child := fmt.Sprintf("pc_fk_normp%d", i), fmt.Sprintf("pc_fk_normc%d", i)
			fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE "+parent+"(id "+tc.typ+" PRIMARY KEY)", "CREATE TABLE "+child+"(id "+tc.typ+",CONSTRAINT pc_normalized FOREIGN KEY(id) REFERENCES "+parent+")")
			pgConstraintFK(t, p, b, child, parent, "pc_normalized", "{1}", "{1}", "{"+tc.op+"}", "{"+tc.op+"}", "{"+tc.op+"}", "a", "a", "s", nil, true)
		}
		// Resolve real builtin identities through the existing scalar API rather
		// than requiring the adjacent pg_operator virtual catalog.
		for _, tc := range []struct {
			oid              int64
			input, signature string
		}{
			{15, "pg_catalog.=(pg_catalog.int4,pg_catalog.int8)", "=(integer,bigint)"},
			{96, "pg_catalog.=(pg_catalog.int4,pg_catalog.int4)", "=(integer,integer)"},
			{410, "pg_catalog.=(pg_catalog.int8,pg_catalog.int8)", "=(bigint,bigint)"},
			{3516, "pg_catalog.=(pg_catalog.anyenum,pg_catalog.anyenum)", "=(anyenum,anyenum)"},
		} {
			q := "SELECT $1::oid::regoperator::text AS signature,$2::regoperator::oid=$1::oid AS identity"
			s := fixture.Select(q, []string{"signature", "identity"}, []string{"TEXT", "BOOL"}, [][]any{{tc.signature, true}}, tc.oid, tc.input)
			pgConstraintCheck(t, p, b, b.DB, s)
			pgConstraintCheck(t, p, b, b.DB, fixture.Prepared(s))
		}
	})
}

func pgConstraintFK(t *testing.T, p *fixture.Pair, b fixture.Backend, child, parent, name, key, ref, pf, pp, ff, update, del, match string, subset any, validated bool) {
	t.Helper()
	q := "SELECT conkey::text,confkey::text,conpfeqop::text,conppeqop::text,conffeqop::text,confupdtype,confdeltype,confmatchtype,confdelsetcols::text,convalidated,conenforced,connoinherit,confrelid=$2::regclass::oid AS target,conindid=(SELECT conindid FROM pg_constraint WHERE conrelid=$2::regclass::oid AND contype IN ('p','u')) AS provider FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$3"
	s := fixture.Select(q, []string{"conkey", "confkey", "conpfeqop", "conppeqop", "conffeqop", "confupdtype", "confdeltype", "confmatchtype", "confdelsetcols", "convalidated", "conenforced", "connoinherit", "target", "provider"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "CHAR", "CHAR", "CHAR", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{key, ref, pf, pp, ff, update, del, match, subset, validated, true, true, true, true}}, child, parent, name)
	pgConstraintCheck(t, p, b, b.DB, s)
	pgConstraintCheck(t, p, b, b.DB, fixture.Prepared(s))
}
