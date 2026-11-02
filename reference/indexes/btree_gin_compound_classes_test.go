package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Release expectations: REL_18_6 btree_gin.control, btree_gin--1.0.sql and
// btree_gin--1.1--1.2.sql. Allocated identifiers are checked relationally.
func TestReferenceBtreeGINCompoundClasses(t *testing.T) {
	for _, schema := range []string{"public", "compound_classes_alt"} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE TYPE compound_class_enum AS ENUM('z','a','m')", 0))
				run(fixture.Exec("CREATE TYPE compound_other_enum AS ENUM('a','z')", 0))
				run(fixture.Exec("CREATE TABLE compound_classes(id integer,b bit(3),v varbit,e compound_class_enum,m money,a integer[],t tsvector)", 0))
				for _, tc := range []struct{ column, display string }{{"b", "bit"}, {"v", "bit varying"}, {"e", "compound_class_enum"}, {"m", "money"}} {
					d := fixture.ControlsReferenceDiagnostic("42704", `data type `+tc.display+` has no default operator class for access method "gin"`)
					d.Hint = compoundMissingDefaultHint
					ginUtilityError(t, p, b, conn, "CREATE INDEX compound_class_retry ON compound_classes USING gin("+tc.column+")", d)
					compoundIndexAbsent(run, "compound_classes", "compound_class_retry", 0)
				}
				if schema != "public" {
					run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				}
				run(fixture.Prepared(fixture.Exec("CREATE EXTENSION btree_gin WITH SCHEMA "+schema+" VERSION '1.3'", 0)))
				classRoutes(t, p, b, conn, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment,e.oid<>0 AS allocated FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"extversion", "extrelocatable", "nspname", "comment", "allocated"}, []string{"TEXT", "BOOL", "NAME", "TEXT", "BOOL"}, [][]any{{"1.3", true, schema, "support for indexing common datatypes in GIN", true}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT name,version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='btree_gin' AND version='1.3'", []string{"name", "version", "superuser", "trusted", "relocatable"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"btree_gin", "1.3", true, true, true}}))
				run(fixture.Exec("CREATE TABLE compound_classes_empty(id integer,b bit(3),v varbit,e compound_class_enum,m money)", 0))
				run(fixture.Exec("INSERT INTO compound_classes VALUES(1,'100','0','a','0.01','{2}','cat:1'),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
				for _, tc := range []struct {
					name, column, suffix, comparator string
					input, comparatorOID             int64
				}{
					{"bit_ops", "b", "bit", "bitcmp", 1560, 1596},
					{"varbit_ops", "v", "varbit", "varbitcmp", 1562, 1672},
					{"enum_ops", "e", "anyenum", "gin_enum_cmp", 3500, 0},
					{"money_ops", "m", "money", "cash_cmp", 790, 377},
				} {
					q := "SELECT c.opcname,c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,f.opfname,f.opfmethod::bigint,c.opcnamespace=f.opfnamespace AS same_schema,c.opcowner=f.opfowner AS same_owner,c.oid<>0 AND f.oid<>0 AND c.oid<>f.oid AS allocated FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2"
					classRoutes(t, p, b, conn, fixture.Select(q, []string{"opcname", "opcmethod", "opcintype", "opcdefault", "opckeytype", "opfname", "opfmethod", "same_schema", "same_owner", "allocated"}, []string{"NAME", "INT8", "INT8", "BOOL", "INT8", "NAME", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.name, int64(2742), tc.input, true, int64(0), tc.name, int64(2742), true, true, true}}, schema, tc.name))
					run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_opclass c ON c.oid=d.objid WHERE d.classid='pg_opclass'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e' AND c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2", 1, schema, tc.name))
					run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_opfamily f ON f.oid=d.objid WHERE d.classid='pg_opfamily'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e' AND f.opfmethod=2742 AND f.opfnamespace=$1::regnamespace AND f.opfname=$2", 1, schema, tc.name))
					var operators [][]any
					for i, op := range []string{"<", "<=", "=", ">=", ">"} {
						operators = append(operators, []any{int64(i + 1), "s", tc.input, tc.input, int64(2742), int64(0), op, true, true, true})
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT a.amopstrategy,a.amoppurpose,a.amoplefttype::bigint,a.amoprighttype::bigint,a.amopmethod::bigint,a.amopsortfamily::bigint,o.oprname,a.amoplefttype=o.oprleft AS left_link,a.amoprighttype=o.oprright AS right_link,o.oprresult=16 AS boolean_result FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily JOIN pg_operator o ON o.oid=a.amopopr WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amopstrategy", []string{"amopstrategy", "amoppurpose", "amoplefttype", "amoprighttype", "amopmethod", "amopsortfamily", "oprname", "left_link", "right_link", "boolean_result"}, []string{"INT2", "CHAR", "INT8", "INT8", "INT8", "INT8", "NAME", "BOOL", "BOOL", "BOOL"}, operators, schema, tc.name))
					operatorIDs := map[int64][]int64{1560: {1786, 1788, 1784, 1789, 1787}, 1562: {1806, 1808, 1804, 1809, 1807}, 3500: {3518, 3520, 3516, 3521, 3519}, 790: {902, 904, 900, 905, 903}}
					var identities [][]any
					for i, oid := range operatorIDs[tc.input] {
						identities = append(identities, []any{int64(i + 1), oid, int64(11)})
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT a.amopstrategy,a.amopopr::bigint,o.oprnamespace::bigint FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily JOIN pg_operator o ON o.oid=a.amopopr WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amopstrategy", []string{"amopstrategy", "amopopr", "oprnamespace"}, []string{"INT2", "INT8", "INT8"}, identities, schema, tc.name))
					supports := [][]any{
						{int64(1), tc.input, tc.input, tc.comparator, int64(23), fmt.Sprintf("%d %d", tc.input, tc.input)},
						{int64(2), tc.input, tc.input, "gin_extract_value_" + tc.suffix, int64(2281), fmt.Sprintf("%d 2281", tc.input)},
						{int64(3), tc.input, tc.input, "gin_extract_query_" + tc.suffix, int64(2281), fmt.Sprintf("%d 2281 21 2281 2281", tc.input)},
						{int64(4), tc.input, tc.input, "gin_btree_consistent", int64(16), "2281 21 2283 23 2281 2281"},
						{int64(5), tc.input, tc.input, "gin_compare_prefix_" + tc.suffix, int64(23), fmt.Sprintf("%d %d 21 2281", tc.input, tc.input)},
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT a.amprocnum,a.amproclefttype::bigint,a.amprocrighttype::bigint,p.proname,p.prorettype::bigint,p.proargtypes::text FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily JOIN pg_proc p ON p.oid=a.amproc WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 ORDER BY a.amprocnum", []string{"amprocnum", "amproclefttype", "amprocrighttype", "proname", "prorettype", "proargtypes"}, []string{"INT2", "INT8", "INT8", "NAME", "INT8", "TEXT"}, supports, schema, tc.name))
					if tc.comparatorOID != 0 {
						run(fixture.Select("SELECT a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname=$2 AND a.amprocnum=1", []string{"amproc"}, []string{"INT8"}, [][]any{{tc.comparatorOID}}, schema, tc.name))
					}
					// Empty and populated builds, explicit and default, all resolve to the
					// actual installed class rather than a copied fixed allocator number.
					for _, phase := range []string{"empty", "populated"} {
						table := "compound_classes"
						if phase == "empty" {
							table = "compound_classes_empty"
						}
						for _, explicit := range []bool{false, true} {
							name := fmt.Sprintf("compound_%s_%s_%t", tc.column, phase, explicit)
							key := tc.column
							if explicit {
								key += " " + schema + "." + tc.name
							}
							run(fixture.Exec("CREATE INDEX "+name+" ON "+table+" USING gin("+key+")", 0))
							classRoutes(t, p, b, conn, fixture.Select("SELECT i.indclass[0]=c.oid AS class_link,r.relam::bigint,pg_get_indexdef(r.oid,1,true) AS key FROM pg_index i JOIN pg_class r ON r.oid=i.indexrelid JOIN pg_opclass c ON c.oid=i.indclass[0] WHERE r.oid=$1::regclass AND c.opcmethod=2742 AND c.opcname=$2 AND c.opcnamespace=$3::regnamespace", []string{"class_link", "relam", "key"}, []string{"BOOL", "INT8", "TEXT"}, [][]any{{true, int64(2742), tc.column}}, name, tc.name, schema))
						}
					}
				}
				// Installed helper routines, not builtin comparators, have the release C
				// ABI metadata. Shared slot4 must resolve to one installed function.
				classRoutes(t, p, b, conn, fixture.Select("SELECT DISTINCT p.proname,l.lanname,p.probin,p.prosrc=p.proname::text AS source_link,p.provolatile,p.proisstrict,p.proparallel,p.pronamespace=$1::regnamespace AS schema_link FROM pg_opclass c JOIN pg_amproc a ON a.amprocfamily=c.opcfamily JOIN pg_proc p ON p.oid=a.amproc JOIN pg_language l ON l.oid=p.prolang WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname IN "+compoundClasses+" AND (a.amprocnum<>1 OR c.opcname='enum_ops') ORDER BY p.proname", []string{"proname", "lanname", "probin", "source_link", "provolatile", "proisstrict", "proparallel", "schema_link"}, []string{"NAME", "NAME", "TEXT", "BOOL", "CHAR", "BOOL", "CHAR", "BOOL"}, compoundHelperRows(), schema))
				run(amCount("SELECT count(DISTINCT a.amproc::oid) FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname IN "+compoundClasses+" AND a.amprocnum=4", 1, schema))
				run(amCount("SELECT count(DISTINCT p.oid) FROM pg_proc p JOIN pg_amproc a ON a.amproc=p.oid JOIN pg_opclass c ON c.opcfamily=a.amprocfamily JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname IN "+compoundClasses+" AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin') AND d.deptype='e'", 14, schema))
				for _, q := range []string{
					"CREATE DOMAIN compound_bit_domain AS bit(3)", "CREATE DOMAIN compound_varbit_domain AS varbit",
					"CREATE DOMAIN compound_enum_domain AS compound_class_enum",
					"CREATE TABLE compound_cross(b compound_bit_domain,v compound_varbit_domain,e compound_enum_domain,o compound_other_enum)",
					"CREATE INDEX compound_cross_i ON compound_cross USING gin(b " + schema + ".varbit_ops,v " + schema + ".bit_ops,e " + schema + ".enum_ops,o " + schema + ".enum_ops)",
					"CREATE INDEX compound_class_retry ON compound_classes USING gin(b " + schema + ".bit_ops,v " + schema + ".varbit_ops,e " + schema + ".enum_ops,m " + schema + ".money_ops,a,t)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Prepared(fixture.Exec("INSERT INTO compound_cross VALUES($1::compound_bit_domain,$2::compound_varbit_domain,$3::compound_enum_domain,$4::compound_other_enum)", 1, "100", "00", "a", "a")))
				cross := fixture.Select("SELECT b,v,e,o FROM compound_cross", []string{"b", "v", "e", "o"}, []string{"BIT", "VARBIT", "", ""}, [][]any{{"100", "00", "a", "a"}})
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, cross)
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, fixture.Prepared(cross))
				run(ginIDs("SELECT id FROM compound_classes WHERE b='100'::bit(3) AND v>=''::varbit AND e>='z'::compound_class_enum AND m>'0'::money AND a @> '{2}'::integer[] AND t @@ 'cat'::tsquery ORDER BY id", []int64{1}))
			})
		})
	}
	t.Run("transactional_install", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE TYPE compound_tx_enum AS ENUM('z','a','m')", 0))
			run(fixture.Exec("CREATE TABLE compound_tx(id integer,b bit(3),v varbit,e compound_tx_enum,m money)", 0))
			create := func(x fixture.Executor) {
				for _, q := range []string{
					"CREATE EXTENSION btree_gin VERSION '1.3'",
					"CREATE INDEX compound_tx_i ON compound_tx USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops)",
				} {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec("INSERT INTO compound_tx VALUES(1,'100','0','a','0.01')", 1))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, ginIDs("SELECT id FROM compound_tx WHERE b='100'::bit(3) AND v>=''::varbit AND e>='z'::compound_tx_enum AND m>'0'::money ORDER BY id", []int64{1}))
			}
			absent := func() {
				run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
				run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN "+compoundClasses, 0))
				run(amCount("SELECT count(*) FROM pg_proc WHERE proname='gin_btree_consistent' AND pronamespace='public'::regnamespace", 0))
				compoundIndexAbsent(run, "compound_tx", "compound_tx_i", 0)
				run(ginIDs("SELECT id FROM compound_tx ORDER BY id", nil))
			}
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			create(tx)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN "+compoundClasses, 0))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			absent()
			run(fixture.Exec("BEGIN", 0))
			create(conn)
			run(fixture.Exec("ROLLBACK", 0))
			absent()
			run(fixture.Exec("BEGIN", 0))
			create(conn)
			run(fixture.Exec("COMMIT", 0))
			classRoutes(t, p, b, conn, ginIDs("SELECT id FROM compound_tx WHERE m='0.01'::money ORDER BY id", []int64{1}))
			run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 1))
			run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='compound_tx'::regclass", 1))
		})
	})
}

func compoundHelperRows() [][]any {
	var rows [][]any
	for _, name := range []string{"gin_btree_consistent", "gin_compare_prefix_anyenum", "gin_compare_prefix_bit", "gin_compare_prefix_money", "gin_compare_prefix_varbit", "gin_enum_cmp", "gin_extract_query_anyenum", "gin_extract_query_bit", "gin_extract_query_money", "gin_extract_query_varbit", "gin_extract_value_anyenum", "gin_extract_value_bit", "gin_extract_value_money", "gin_extract_value_varbit"} {
		rows = append(rows, []any{name, "c", "$libdir/btree_gin", true, "i", true, "u", true})
	}
	return rows
}
