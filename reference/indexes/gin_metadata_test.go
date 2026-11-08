package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINMetadata(t *testing.T) {
	amBackends(t, []string{"gin_metadata_custom"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		// Generated OIDs are root-observed on the pinned PostgreSQL 18.6 oracle.
		// Scope these inventories to the selected builtins: array, vector and
		// the two jsonb GIN classes.
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,amname,amhandler::oid::bigint,amtype FROM pg_am WHERE oid=2742", []string{"oid", "amname", "amhandler", "amtype"}, []string{"INT8", "NAME", "INT8", "CHAR"}, [][]any{{int64(2742), "gin", int64(333), "i"}}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,opcname,opcmethod::bigint,opcnamespace::bigint,opcowner::bigint,opcfamily::bigint,opcintype::bigint,opcdefault,opckeytype::bigint FROM pg_opclass WHERE opcmethod=2742 AND opcname IN ('array_ops','tsvector_ops','jsonb_ops','jsonb_path_ops') ORDER BY oid", []string{"oid", "opcname", "opcmethod", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL", "INT8"}, [][]any{
			{int64(10064), "array_ops", int64(2742), int64(11), int64(10), int64(2745), int64(2277), true, int64(2283)},
			{int64(10073), "tsvector_ops", int64(2742), int64(11), int64(10), int64(3659), int64(3614), true, int64(25)},
			{int64(10090), "jsonb_ops", int64(2742), int64(11), int64(10), int64(4036), int64(3802), true, int64(25)},
			{int64(10091), "jsonb_path_ops", int64(2742), int64(11), int64(10), int64(4037), int64(3802), false, int64(23)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,opfname,opfmethod::bigint,opfnamespace::bigint,opfowner::bigint FROM pg_opfamily WHERE oid IN (2745,3659,4036,4037) ORDER BY oid", []string{"oid", "opfname", "opfmethod", "opfnamespace", "opfowner"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8"}, [][]any{
			{int64(2745), "array_ops", int64(2742), int64(11), int64(10)},
			{int64(3659), "tsvector_ops", int64(2742), int64(11), int64(10)},
			{int64(4036), "jsonb_ops", int64(2742), int64(11), int64(10)},
			{int64(4037), "jsonb_path_ops", int64(2742), int64(11), int64(10)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,amopfamily::bigint,amoplefttype::bigint,amoprighttype::bigint,amopstrategy,amoppurpose,amopopr::bigint,amopmethod::bigint,amopsortfamily::bigint FROM pg_amop WHERE amopfamily IN (2745,3659,4036,4037) ORDER BY oid", []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "CHAR", "INT8", "INT8", "INT8"}, [][]any{
			{int64(10349), int64(2745), int64(2277), int64(2277), int64(1), "s", int64(2750), int64(2742), int64(0)},
			{int64(10350), int64(2745), int64(2277), int64(2277), int64(2), "s", int64(2751), int64(2742), int64(0)},
			{int64(10351), int64(2745), int64(2277), int64(2277), int64(3), "s", int64(2752), int64(2742), int64(0)},
			{int64(10352), int64(2745), int64(2277), int64(2277), int64(4), "s", int64(1070), int64(2742), int64(0)},
			{int64(10365), int64(3659), int64(3614), int64(3615), int64(1), "s", int64(3636), int64(2742), int64(0)},
			{int64(10366), int64(3659), int64(3614), int64(3615), int64(2), "s", int64(3660), int64(2742), int64(0)},
			{int64(10456), int64(4036), int64(3802), int64(3802), int64(7), "s", int64(3246), int64(2742), int64(0)},
			{int64(10457), int64(4036), int64(3802), int64(25), int64(9), "s", int64(3247), int64(2742), int64(0)},
			{int64(10458), int64(4036), int64(3802), int64(1009), int64(10), "s", int64(3248), int64(2742), int64(0)},
			{int64(10459), int64(4036), int64(3802), int64(1009), int64(11), "s", int64(3249), int64(2742), int64(0)},
			{int64(10460), int64(4036), int64(3802), int64(4072), int64(15), "s", int64(4012), int64(2742), int64(0)},
			{int64(10461), int64(4036), int64(3802), int64(4072), int64(16), "s", int64(4013), int64(2742), int64(0)},
			{int64(10462), int64(4037), int64(3802), int64(3802), int64(7), "s", int64(3246), int64(2742), int64(0)},
			{int64(10463), int64(4037), int64(3802), int64(4072), int64(15), "s", int64(4012), int64(2742), int64(0)},
			{int64(10464), int64(4037), int64(3802), int64(4072), int64(16), "s", int64(4013), int64(2742), int64(0)},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT a.oid::bigint,a.amprocfamily::bigint,a.amproclefttype::bigint,a.amprocrighttype::bigint,a.amprocnum,a.amproc::oid::bigint,p.proname,p.prorettype::bigint,p.proargtypes::text,p.prosrc,p.provolatile,p.proisstrict,p.proparallel FROM pg_amproc a JOIN pg_proc p ON p.oid=a.amproc WHERE a.amprocfamily IN (2745,3659,4036,4037) ORDER BY a.oid", []string{"oid", "amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc", "proname", "prorettype", "proargtypes", "prosrc", "provolatile", "proisstrict", "proparallel"}, []string{"INT8", "INT8", "INT8", "INT8", "INT2", "INT8", "NAME", "INT8", "TEXT", "TEXT", "CHAR", "BOOL", "CHAR"}, [][]any{
			{int64(10276), int64(2745), int64(2277), int64(2277), int64(2), int64(2743), "ginarrayextract", int64(2281), "2277 2281 2281", "ginarrayextract", "i", true, "s"},
			{int64(10277), int64(2745), int64(2277), int64(2277), int64(3), int64(2774), "ginqueryarrayextract", int64(2281), "2277 2281 21 2281 2281 2281 2281", "ginqueryarrayextract", "i", true, "s"},
			{int64(10278), int64(2745), int64(2277), int64(2277), int64(4), int64(2744), "ginarrayconsistent", int64(16), "2281 21 2277 23 2281 2281 2281 2281", "ginarrayconsistent", "i", true, "s"},
			{int64(10279), int64(2745), int64(2277), int64(2277), int64(6), int64(3920), "ginarraytriconsistent", int64(18), "2281 21 2277 23 2281 2281 2281", "ginarraytriconsistent", "i", true, "s"},
			{int64(10280), int64(3659), int64(3614), int64(3614), int64(1), int64(3724), "gin_cmp_tslexeme", int64(23), "25 25", "gin_cmp_tslexeme", "i", true, "s"},
			{int64(10281), int64(3659), int64(3614), int64(3614), int64(2), int64(3656), "gin_extract_tsvector", int64(2281), "3614 2281 2281", "gin_extract_tsvector", "i", true, "s"},
			{int64(10282), int64(3659), int64(3614), int64(3614), int64(3), int64(3657), "gin_extract_tsquery", int64(2281), "3614 2281 21 2281 2281 2281 2281", "gin_extract_tsquery", "i", true, "s"},
			{int64(10283), int64(3659), int64(3614), int64(3614), int64(4), int64(3658), "gin_tsquery_consistent", int64(16), "2281 21 3614 23 2281 2281 2281 2281", "gin_tsquery_consistent", "i", true, "s"},
			{int64(10284), int64(3659), int64(3614), int64(3614), int64(5), int64(2700), "gin_cmp_prefix", int64(23), "25 25 21 2281", "gin_cmp_prefix", "i", true, "s"},
			{int64(10285), int64(3659), int64(3614), int64(3614), int64(6), int64(3921), "gin_tsquery_triconsistent", int64(18), "2281 21 3614 23 2281 2281 2281", "gin_tsquery_triconsistent", "i", true, "s"},
			{int64(10286), int64(4036), int64(3802), int64(3802), int64(1), int64(3480), "gin_compare_jsonb", int64(23), "25 25", "gin_compare_jsonb", "i", true, "s"},
			{int64(10287), int64(4036), int64(3802), int64(3802), int64(2), int64(3482), "gin_extract_jsonb", int64(2281), "3802 2281 2281", "gin_extract_jsonb", "i", true, "s"},
			{int64(10288), int64(4036), int64(3802), int64(3802), int64(3), int64(3483), "gin_extract_jsonb_query", int64(2281), "3802 2281 21 2281 2281 2281 2281", "gin_extract_jsonb_query", "i", true, "s"},
			{int64(10289), int64(4036), int64(3802), int64(3802), int64(4), int64(3484), "gin_consistent_jsonb", int64(16), "2281 21 3802 23 2281 2281 2281 2281", "gin_consistent_jsonb", "i", true, "s"},
			{int64(10290), int64(4036), int64(3802), int64(3802), int64(6), int64(3488), "gin_triconsistent_jsonb", int64(18), "2281 21 3802 23 2281 2281 2281", "gin_triconsistent_jsonb", "i", true, "s"},
			{int64(10291), int64(4037), int64(3802), int64(3802), int64(1), int64(351), "btint4cmp", int64(23), "23 23", "btint4cmp", "i", true, "s"},
			{int64(10292), int64(4037), int64(3802), int64(3802), int64(2), int64(3485), "gin_extract_jsonb_path", int64(2281), "3802 2281 2281", "gin_extract_jsonb_path", "i", true, "s"},
			{int64(10293), int64(4037), int64(3802), int64(3802), int64(3), int64(3486), "gin_extract_jsonb_query_path", int64(2281), "3802 2281 21 2281 2281 2281 2281", "gin_extract_jsonb_query_path", "i", true, "s"},
			{int64(10294), int64(4037), int64(3802), int64(3802), int64(4), int64(3487), "gin_consistent_jsonb_path", int64(16), "2281 21 3802 23 2281 2281 2281 2281", "gin_consistent_jsonb_path", "i", true, "s"},
			{int64(10295), int64(4037), int64(3802), int64(3802), int64(6), int64(3489), "gin_triconsistent_jsonb_path", int64(18), "2281 21 3802 23 2281 2281 2281", "gin_triconsistent_jsonb_path", "i", true, "s"},
		}))
		classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,proname,pronamespace::bigint,proowner::bigint,prolang::bigint,procost,prorows,prokind,proisstrict,proretset,provolatile,proparallel,prorettype::bigint,proargtypes::text,prosrc,prosecdef,proleakproof,proconfig::text,proacl::text FROM pg_proc WHERE oid=333", []string{"oid", "proname", "pronamespace", "proowner", "prolang", "procost", "prorows", "prokind", "proisstrict", "proretset", "provolatile", "proparallel", "prorettype", "proargtypes", "prosrc", "prosecdef", "proleakproof", "proconfig", "proacl"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8", "FLOAT4", "FLOAT4", "CHAR", "BOOL", "BOOL", "CHAR", "CHAR", "INT8", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(333), "ginhandler", int64(11), int64(10), int64(12), float64(1), float64(0), "f", true, false, "v", "s", int64(325), "2281", "ginhandler", false, false, nil, nil}}))
		for _, prop := range []struct {
			name string
			want bool
		}{{"can_order", false}, {"can_unique", false}, {"can_multi_col", true}, {"can_exclude", false}, {"can_include", false}} {
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_indexam_has_property(2742,$1) AS property", []string{"property"}, []string{"BOOL"}, [][]any{{prop.want}}, prop.name))
		}
		run(fixture.Exec("CREATE TABLE gin_metadata(id integer,a integer[],v tsvector)", 0))
		run(fixture.Exec("CREATE INDEX gin_metadata_i ON gin_metadata USING gin(a pg_catalog.array_ops,v pg_catalog.tsvector_ops)", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT indclass::text,indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='gin_metadata_i'::regclass", []string{"indclass", "indisunique", "indisvalid", "indisready", "indislive"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"10064 10073", false, true, true, true}}))
		for _, prop := range []string{"asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls"} {
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_index_column_has_property('gin_metadata_i'::regclass,1,$1) AS a,pg_index_column_has_property('gin_metadata_i'::regclass,2,$1) AS v", []string{"a", "v"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}, prop))
		}
		for _, prop := range []struct {
			name string
			want bool
		}{{"clusterable", false}, {"index_scan", false}, {"bitmap_scan", true}, {"backward_scan", false}} {
			classRoutes(t, p, b, conn, fixture.Select("SELECT pg_index_has_property('gin_metadata_i'::regclass,$1) AS property", []string{"property"}, []string{"BOOL"}, [][]any{{prop.want}}, prop.name))
		}
		run(fixture.Exec("CREATE ACCESS METHOD gin_metadata_custom TYPE INDEX HANDLER ginhandler", 0))
		classRoutes(t, p, b, conn, fixture.Select("SELECT amname,amhandler::oid::bigint,oid<>2742 AS separate,pg_indexam_has_property(oid,'can_multi_col') AS multicolumn FROM pg_am WHERE amname='gin_metadata_custom'", []string{"amname", "amhandler", "separate", "multicolumn"}, []string{"NAME", "INT8", "BOOL", "BOOL"}, [][]any{{"gin_metadata_custom", int64(333), true, true}}))
		run(fixture.Exec("DROP ACCESS METHOD gin_metadata_custom", 0))
	})
}
