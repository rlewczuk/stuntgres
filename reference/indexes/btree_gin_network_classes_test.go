package reference_test

import (
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINNetworkClasses(t *testing.T) {
	for _, schema := range []string{"public", "network_classes_alt"} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN "+networkClasses, 0))
				run(fixture.Exec("CREATE TABLE network_classes(id integer,v inet)", 0))
				d := fixture.ControlsReferenceDiagnostic("42704", `data type inet has no default operator class for access method "gin"`)
				d.Hint = compoundMissingDefaultHint
				ginUtilityError(t, p, b, conn, "CREATE INDEX network_class_retry ON network_classes USING gin(v)", d)
				compoundIndexAbsent(run, "network_classes", "network_class_retry", 0)
				if schema != "public" {
					run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				}
				run(fixture.Prepared(fixture.Exec("CREATE EXTENSION btree_gin WITH SCHEMA "+schema+" VERSION '1.3'", 0)))
				classRoutes(t, p, b, conn, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"extversion", "extrelocatable", "nspname", "comment"}, []string{"TEXT", "BOOL", "NAME", "TEXT"}, [][]any{{"1.3", true, schema, "support for indexing common datatypes in GIN"}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT name,version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='btree_gin' AND version='1.3'", []string{"name", "version", "superuser", "trusted", "relocatable"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"btree_gin", "1.3", true, true, true}}))
				networkGraph(t, p, b, conn, schema)
				run(fixture.Exec("CREATE INDEX network_class_retry ON network_classes USING gin(v "+schema+".inet_ops)", 0))
			})
		})
	}
	t.Run("genuine_macaddr8_cmp", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, _ := classConn(t, p, b)
			classRoutes(t, p, b, conn, fixture.Select("SELECT oid::bigint,proname,pronamespace::bigint,prolang::bigint,prorettype::bigint,proargtypes::text,provolatile,proisstrict,proparallel,proleakproof,prosrc FROM pg_proc WHERE oid=4119", []string{"oid", "proname", "pronamespace", "prolang", "prorettype", "proargtypes", "provolatile", "proisstrict", "proparallel", "proleakproof", "prosrc"}, []string{"INT8", "NAME", "INT8", "INT8", "INT8", "TEXT", "CHAR", "BOOL", "CHAR", "BOOL", "TEXT"}, [][]any{{int64(4119), "macaddr8_cmp", int64(11), int64(12), int64(23), "774 774", "i", true, "s", true, "macaddr8_cmp"}}))
			tc := networkCases[3]
			q := "SELECT macaddr8_cmp($1::macaddr8,$2::macaddr8)<0 AS lt,macaddr8_cmp($1::macaddr8,$2::macaddr8)=0 AS eq,macaddr8_cmp($1::macaddr8,$2::macaddr8)>0 AS gt"
			held := classHeld(t, p, conn, q)
			for _, pair := range []struct {
				a, b any
				want []any
			}{{tc.lower, tc.boundary, []any{true, false, false}}, {tc.boundary, tc.lower, []any{false, false, true}}, {tc.upper, tc.upper, []any{false, true, false}}, {nil, tc.lower, []any{nil, nil, nil}}, {tc.lower, nil, []any{nil, nil, nil}}} {
				s := fixture.Select(q, []string{"lt", "eq", "gt"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{pair.want}, pair.a, pair.b)
				classRoutes(t, p, b, conn, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}

// Verify associations separately from the actual procedure formals. In particular
// CIDR's slot1 association is 650/650 but network_cmp's formals are 869/869.
func networkGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, schema string, presentation ...map[string]string) {
	t.Helper()
	ginInstalledGraph(t, p, b, conn, schema, networkCases, "(869,650,829,774,2950,26)", presentation...)
}

// The case table is independently specified by each family, never the provider.
func ginInstalledGraph(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, schema string, cases []networkCase, inputs string, presentation ...map[string]string) {
	t.Helper()
	run := func(s fixture.Step) { classRoutes(t, p, b, conn, s) }
	for _, tc := range cases {
		name, familyName := tc.typ+"_ops", tc.typ+"_ops"
		if len(presentation) > 0 {
			if n := presentation[0]["class:"+tc.typ]; n != "" {
				name = n
			}
			if n := presentation[0]["family:"+tc.typ]; n != "" {
				familyName = n
			}
		}
		run(fixture.Select("SELECT c.opcname,c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,f.opfname,f.opfmethod::bigint,c.opcnamespace=f.opfnamespace AS same_schema,c.opcowner=f.opfowner AS same_owner,c.oid<>0 AND f.oid<>0 AND c.oid<>f.oid AS allocated FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2", []string{"opcname", "opcmethod", "opcintype", "opcdefault", "opckeytype", "opfname", "opfmethod", "same_schema", "same_owner", "allocated"}, []string{"NAME", "INT8", "INT8", "BOOL", "INT8", "NAME", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{name, int64(2742), tc.input, true, int64(0), familyName, int64(2742), true, true, true}}, schema, name))
		var operators [][]any
		for i, op := range []string{"<", "<=", "=", ">=", ">"} {
			operators = append(operators, []any{int64(i + 1), "s", tc.search, tc.search, int64(2742), int64(0), tc.operators[i], op, true, true, true, true})
		}
		run(fixture.Select("SELECT a.amopstrategy,a.amoppurpose,a.amoplefttype::bigint,a.amoprighttype::bigint,a.amopmethod::bigint,a.amopsortfamily::bigint,a.amopopr::bigint,o.oprname,a.amoplefttype=o.oprleft AS left_link,a.amoprighttype=o.oprright AS right_link,o.oprresult=16 AS boolean_result,p.oid=o.oprcode::oid AS procedure_link FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily JOIN pg_operator o ON o.oid=a.amopopr JOIN pg_proc p ON p.oid=o.oprcode::oid WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amopstrategy", []string{"amopstrategy", "amoppurpose", "amoplefttype", "amoprighttype", "amopmethod", "amopsortfamily", "amopopr", "oprname", "left_link", "right_link", "boolean_result", "procedure_link"}, []string{"INT2", "CHAR", "INT8", "INT8", "INT8", "INT8", "INT8", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, operators, schema, name))
		formal := tc.input
		if tc.typ == "cidr" {
			formal = 869
		}
		supports := [][]any{
			{int64(1), tc.input, tc.input, tc.comparator, int64(23), fmt.Sprintf("%d %d", formal, formal)},
			{int64(2), tc.input, tc.input, "gin_extract_value_" + tc.typ, int64(2281), fmt.Sprintf("%d 2281", tc.input)},
			{int64(3), tc.input, tc.input, "gin_extract_query_" + tc.typ, int64(2281), fmt.Sprintf("%d 2281 21 2281 2281", tc.input)},
			{int64(4), tc.input, tc.input, "gin_btree_consistent", int64(16), "2281 21 2283 23 2281 2281"},
			{int64(5), tc.input, tc.input, "gin_compare_prefix_" + tc.typ, int64(23), fmt.Sprintf("%d %d 21 2281", tc.input, tc.input)},
		}
		run(fixture.Select("SELECT a.amprocnum,a.amproclefttype::bigint,a.amprocrighttype::bigint,p.proname,p.prorettype::bigint,p.proargtypes::text FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily JOIN pg_proc p ON p.oid=a.amproc WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amprocnum", []string{"amprocnum", "amproclefttype", "amprocrighttype", "proname", "prorettype", "proargtypes"}, []string{"INT2", "INT8", "INT8", "NAME", "INT8", "TEXT"}, supports, schema, name))
		run(fixture.Select("SELECT a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 AND a.amprocnum=1", []string{"amproc"}, []string{"INT8"}, [][]any{{tc.comparatorOID}}, schema, name))
		for _, pair := range []struct{ catalog, prefix string }{{"pg_opclass", "opc"}, {"pg_opfamily", "opf"}} {
			objectName := name
			if pair.catalog == "pg_opfamily" {
				objectName = familyName
			}
			run(amCount("SELECT count(*) FROM pg_depend d JOIN "+pair.catalog+" c ON c.oid=d.objid WHERE d.classid='"+pair.catalog+"'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e' AND c."+pair.prefix+"method=2742 AND c."+pair.prefix+"namespace=$1::regnamespace AND c."+pair.prefix+"name=$2", 1, schema, objectName))
		}
		// REL_18_6 ginadjustmembers overrides generic CREATE OPCLASS ownership:
		// all search members and optional supports belong softly to the family;
		// only required extraction supports 2/3 belong internally to the class.
		var searchDependencies, supportDependencies [][]any
		for number := range 5 {
			searchDependencies = append(searchDependencies, []any{int64(number + 1), "a", true})
			kind, owner := "a", "family"
			if number == 1 || number == 2 {
				kind, owner = "i", "class"
			}
			supportDependencies = append(supportDependencies, []any{int64(number + 1), kind, owner, true})
		}
		run(fixture.Select("SELECT a.amopstrategy,d.deptype,d.refobjid=c.opcfamily AS owner_link FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily JOIN pg_depend d ON d.objid=a.oid AND d.classid='pg_amop'::regclass WHERE d.refclassid IN('pg_opclass'::regclass,'pg_opfamily'::regclass) AND c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amopstrategy", []string{"amopstrategy", "deptype", "owner_link"}, []string{"INT2", "CHAR", "BOOL"}, searchDependencies, schema, name))
		run(fixture.Select("SELECT a.amprocnum,d.deptype,CASE WHEN d.refclassid='pg_opclass'::regclass THEN 'class' ELSE 'family' END AS owner_kind,CASE WHEN d.refclassid='pg_opclass'::regclass THEN d.refobjid=c.oid ELSE d.refobjid=c.opcfamily END AS owner_link FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily JOIN pg_depend d ON d.objid=a.oid AND d.classid='pg_amproc'::regclass WHERE d.refclassid IN('pg_opclass'::regclass,'pg_opfamily'::regclass) AND c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amprocnum", []string{"amprocnum", "deptype", "owner_kind", "owner_link"}, []string{"INT2", "CHAR", "TEXT", "BOOL"}, supportDependencies, schema, name))
	}
	var names []string
	for _, tc := range cases {
		for _, prefix := range []string{"gin_extract_value_", "gin_extract_query_", "gin_compare_prefix_"} {
			names = append(names, prefix+tc.typ)
		}
	}
	names = append(names, "gin_btree_consistent")
	sort.Strings(names)
	var helpers [][]any
	for _, name := range names {
		helpers = append(helpers, []any{name, "c", "$libdir/btree_gin", true, "i", true, "u", true})
	}
	run(fixture.Select("SELECT DISTINCT p.proname,l.lanname,p.probin,p.prosrc=p.proname::text AS source_link,p.provolatile,p.proisstrict,p.proparallel,p.pronamespace=$1::regnamespace AS schema_link FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily JOIN pg_proc p ON p.oid=a.amproc JOIN pg_language l ON l.oid=p.prolang WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcintype IN"+inputs+" AND a.amprocnum<>1 ORDER BY p.proname", []string{"proname", "lanname", "probin", "source_link", "provolatile", "proisstrict", "proparallel", "schema_link"}, []string{"NAME", "NAME", "TEXT", "BOOL", "CHAR", "BOOL", "CHAR", "BOOL"}, helpers, schema))
	run(amCount("SELECT count(DISTINCT a.amproc::oid) FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND (c.opcintype IN"+inputs+" OR c.opcintype IN(1560,1562,3500,790)) AND a.amprocnum=4", 1, schema))
	run(amCount("SELECT count(DISTINCT p.oid) FROM pg_proc p JOIN pg_amproc a ON a.amproc=p.oid JOIN pg_opclass c ON c.opcfamily=a.amprocfamily JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcintype IN"+inputs+" AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e'", int64(3*len(cases)+1), schema))
}
