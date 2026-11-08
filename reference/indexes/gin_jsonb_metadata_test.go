package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceGINJSONBMetadata pins the PostgreSQL 18.6 catalog identities for
// the default jsonb_ops class and the explicitly named jsonb_path_ops class.
func TestReferenceGINJSONBMetadata(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE pjm(id integer,labels jsonb)", 0))
		run(fixture.Exec(`INSERT INTO pjm VALUES(1,'{"a":"b"}')`, 1))
		run(fixture.Exec("CREATE INDEX pjm_def ON pjm USING GIN (labels)", 0))
		run(fixture.Exec("CREATE INDEX pjm_exp ON pjm USING GIN (labels pg_catalog.jsonb_ops)", 0))
		run(fixture.Exec("CREATE INDEX pjm_path ON pjm USING GIN (labels jsonb_path_ops)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT schemaname,tablename,indexname,tablespace,indexdef FROM pg_indexes WHERE tablename='pjm' ORDER BY indexname", []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}, []string{"NAME", "NAME", "NAME", "NAME", "TEXT"}, [][]any{
			{"public", "pjm", "pjm_def", nil, "CREATE INDEX pjm_def ON public.pjm USING gin (labels)"},
			{"public", "pjm", "pjm_exp", nil, "CREATE INDEX pjm_exp ON public.pjm USING gin (labels)"},
			{"public", "pjm", "pjm_path", nil, "CREATE INDEX pjm_path ON public.pjm USING gin (labels jsonb_path_ops)"},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.relname,pg_get_indexdef(c.oid,1,true) AS key FROM pg_class c WHERE c.oid IN ('pjm_def'::regclass,'pjm_exp'::regclass,'pjm_path'::regclass) ORDER BY c.relname", []string{"relname", "key"}, []string{"NAME", "TEXT"}, [][]any{
			{"pjm_def", "labels"}, {"pjm_exp", "labels"}, {"pjm_path", "labels"},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.relname,c.relam::bigint,c.relkind FROM pg_class c WHERE c.oid IN ('pjm_def'::regclass,'pjm_exp'::regclass,'pjm_path'::regclass) ORDER BY c.relname", []string{"relname", "relam", "relkind"}, []string{"NAME", "INT8", "CHAR"}, [][]any{
			{"pjm_def", int64(2742), "i"}, {"pjm_exp", int64(2742), "i"}, {"pjm_path", int64(2742), "i"},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT c.relname,i.indnatts,i.indnkeyatts,i.indisunique,i.indisvalid,i.indisready,i.indislive,i.indkey::text,i.indcollation::text,i.indoption::text,i.indclass::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='pjm'::regclass ORDER BY c.relname", []string{"relname", "indnatts", "indnkeyatts", "indisunique", "indisvalid", "indisready", "indislive", "indkey", "indcollation", "indoption", "indclass"}, []string{"NAME", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
			{"pjm_def", int64(1), int64(1), false, true, true, true, "2", "0", "0", "10090"},
			{"pjm_exp", int64(1), int64(1), false, true, true, true, "2", "0", "0", "10090"},
			{"pjm_path", int64(1), int64(1), false, true, true, true, "2", "0", "0", "10091"},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,opcname,opcmethod::bigint,opcnamespace::bigint,opcowner::bigint,opcfamily::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE opcmethod=2742 AND oid IN (10090,10091) ORDER BY oid", []string{"oid", "opcname", "opcmethod", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL", "INT8"}, [][]any{
			{int64(10090), "jsonb_ops", int64(2742), int64(11), int64(10), int64(4036), int64(3802), true, int64(25)},
			{int64(10091), "jsonb_path_ops", int64(2742), int64(11), int64(10), int64(4037), int64(3802), false, int64(23)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,opfname,opfmethod::bigint,opfnamespace::bigint,opfowner::bigint FROM pg_opfamily WHERE oid IN (4036,4037) ORDER BY oid", []string{"oid", "opfname", "opfmethod", "opfnamespace", "opfowner"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{
			{int64(4036), "jsonb_ops", int64(2742), int64(11), int64(10)},
			{int64(4037), "jsonb_path_ops", int64(2742), int64(11), int64(10)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT pg_indexam_has_property(2742,'can_multi_col') AS property", []string{"property"}, []string{"BOOL"}, [][]any{{true}}))
		for _, name := range []string{"pjm_def", "pjm_exp", "pjm_path"} {
			run(fixture.Exec("DROP INDEX "+name, 0))
		}
		run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='pjm'::regclass", 0))
	})
}