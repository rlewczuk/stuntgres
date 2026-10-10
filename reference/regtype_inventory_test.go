package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Independent REL_18_0 source facts, not native-derived expectations.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat
// Associated arrays are declared array_type_oid, not arithmetic guesses.
var regtypeReleaseIdentities = []struct {
	name       string
	oid, array int64
	output     string
}{
	{"bool", 16, 1000, "boolean"},
	{"bytea", 17, 1001, "bytea"},
	{"char", 18, 1002, "\"char\""},
	{"name", 19, 1003, "name"},
	{"int8", 20, 1016, "bigint"},
	{"int2", 21, 1005, "smallint"},
	{"int2vector", 22, 1006, "int2vector"},
	{"int4", 23, 1007, "integer"},
	{"regproc", 24, 1008, "regproc"},
	{"text", 25, 1009, "text"},
	{"oid", 26, 1028, "oid"},
	{"tid", 27, 1010, "tid"},
	{"xid", 28, 1011, "xid"},
	{"cid", 29, 1012, "cid"},
	{"oidvector", 30, 1013, "oidvector"},
	{"pg_type", 71, 210, "pg_type"},
	{"pg_attribute", 75, 270, "pg_attribute"},
	{"pg_proc", 81, 272, "pg_proc"},
	{"pg_class", 83, 273, "pg_class"},
	{"json", 114, 199, "json"},
	{"xml", 142, 143, "xml"},
	{"pg_node_tree", 194, 0, "pg_node_tree"},
	{"pg_ndistinct", 3361, 0, "pg_ndistinct"},
	{"pg_dependencies", 3402, 0, "pg_dependencies"},
	{"pg_mcv_list", 5017, 0, "pg_mcv_list"},
	{"pg_ddl_command", 32, 0, "pg_ddl_command"},
	{"xid8", 5069, 271, "xid8"},
	{"point", 600, 1017, "point"},
	{"lseg", 601, 1018, "lseg"},
	{"path", 602, 1019, "path"},
	{"box", 603, 1020, "box"},
	{"polygon", 604, 1027, "polygon"},
	{"line", 628, 629, "line"},
	{"float4", 700, 1021, "real"},
	{"float8", 701, 1022, "double precision"},
	{"unknown", 705, 0, "unknown"},
	{"circle", 718, 719, "circle"},
	{"money", 790, 791, "money"},
	{"macaddr", 829, 1040, "macaddr"},
	{"inet", 869, 1041, "inet"},
	{"cidr", 650, 651, "cidr"},
	{"macaddr8", 774, 775, "macaddr8"},
	{"aclitem", 1033, 1034, "aclitem"},
	{"bpchar", 1042, 1014, "character"},
	{"varchar", 1043, 1015, "character varying"},
	{"date", 1082, 1182, "date"},
	{"time", 1083, 1183, "time without time zone"},
	{"timestamp", 1114, 1115, "timestamp without time zone"},
	{"timestamptz", 1184, 1185, "timestamp with time zone"},
	{"interval", 1186, 1187, "interval"},
	{"timetz", 1266, 1270, "time with time zone"},
	{"bit", 1560, 1561, "bit"},
	{"varbit", 1562, 1563, "bit varying"},
	{"numeric", 1700, 1231, "numeric"},
	{"refcursor", 1790, 2201, "refcursor"},
	{"regprocedure", 2202, 2207, "regprocedure"},
	{"regoper", 2203, 2208, "regoper"},
	{"regoperator", 2204, 2209, "regoperator"},
	{"regclass", 2205, 2210, "regclass"},
	{"regcollation", 4191, 4192, "regcollation"},
	{"regtype", 2206, 2211, "regtype"},
	{"regrole", 4096, 4097, "regrole"},
	{"regnamespace", 4089, 4090, "regnamespace"},
	{"uuid", 2950, 2951, "uuid"},
	{"pg_lsn", 3220, 3221, "pg_lsn"},
	{"tsvector", 3614, 3643, "tsvector"},
	{"gtsvector", 3642, 3644, "gtsvector"},
	{"tsquery", 3615, 3645, "tsquery"},
	{"regconfig", 3734, 3735, "regconfig"},
	{"regdictionary", 3769, 3770, "regdictionary"},
	{"jsonb", 3802, 3807, "jsonb"},
	{"jsonpath", 4072, 4073, "jsonpath"},
	{"txid_snapshot", 2970, 2949, "txid_snapshot"},
	{"pg_snapshot", 5038, 5039, "pg_snapshot"},
	{"int4range", 3904, 3905, "int4range"},
	{"numrange", 3906, 3907, "numrange"},
	{"tsrange", 3908, 3909, "tsrange"},
	{"tstzrange", 3910, 3911, "tstzrange"},
	{"daterange", 3912, 3913, "daterange"},
	{"int8range", 3926, 3927, "int8range"},
	{"int4multirange", 4451, 6150, "int4multirange"},
	{"nummultirange", 4532, 6151, "nummultirange"},
	{"tsmultirange", 4533, 6152, "tsmultirange"},
	{"tstzmultirange", 4534, 6153, "tstzmultirange"},
	{"datemultirange", 4535, 6155, "datemultirange"},
	{"int8multirange", 4536, 6157, "int8multirange"},
	{"record", 2249, 2287, "record"},
	{"cstring", 2275, 1263, "cstring"},
	{"any", 2276, 0, "\"any\""},
	{"anyarray", 2277, 0, "anyarray"},
	{"void", 2278, 0, "void"},
	{"trigger", 2279, 0, "trigger"},
	{"event_trigger", 3838, 0, "event_trigger"},
	{"language_handler", 2280, 0, "language_handler"},
	{"internal", 2281, 0, "internal"},
	{"anyelement", 2283, 0, "anyelement"},
	{"anynonarray", 2776, 0, "anynonarray"},
	{"anyenum", 3500, 0, "anyenum"},
	{"fdw_handler", 3115, 0, "fdw_handler"},
	{"index_am_handler", 325, 0, "index_am_handler"},
	{"tsm_handler", 3310, 0, "tsm_handler"},
	{"table_am_handler", 269, 0, "table_am_handler"},
	{"anyrange", 3831, 0, "anyrange"},
	{"anycompatible", 5077, 0, "anycompatible"},
	{"anycompatiblearray", 5078, 0, "anycompatiblearray"},
	{"anycompatiblenonarray", 5079, 0, "anycompatiblenonarray"},
	{"anycompatiblerange", 5080, 0, "anycompatiblerange"},
	{"anymultirange", 4537, 0, "anymultirange"},
	{"anycompatiblemultirange", 4538, 0, "anycompatiblemultirange"},
	{"pg_brin_bloom_summary", 4600, 0, "pg_brin_bloom_summary"},
	{"pg_brin_minmax_multi_summary", 4601, 0, "pg_brin_minmax_multi_summary"},
}

// All CATALOG_HEADERS, every system_views CREATE VIEW, and every
// information_schema CREATE DOMAIN/TABLE/VIEW. IDs absent from these sources
// are deliberately NOT invented: Stage1 captures them from pristine initdb.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/Makefile
var regtypeCatalogRows = []string{
	"pg_proc",
	"pg_type",
	"pg_attribute",
	"pg_class",
	"pg_attrdef",
	"pg_constraint",
	"pg_inherits",
	"pg_index",
	"pg_operator",
	"pg_opfamily",
	"pg_opclass",
	"pg_am",
	"pg_amop",
	"pg_amproc",
	"pg_language",
	"pg_largeobject_metadata",
	"pg_largeobject",
	"pg_aggregate",
	"pg_statistic",
	"pg_statistic_ext",
	"pg_statistic_ext_data",
	"pg_rewrite",
	"pg_trigger",
	"pg_event_trigger",
	"pg_description",
	"pg_cast",
	"pg_enum",
	"pg_namespace",
	"pg_conversion",
	"pg_depend",
	"pg_database",
	"pg_db_role_setting",
	"pg_tablespace",
	"pg_authid",
	"pg_auth_members",
	"pg_shdepend",
	"pg_shdescription",
	"pg_ts_config",
	"pg_ts_config_map",
	"pg_ts_dict",
	"pg_ts_parser",
	"pg_ts_template",
	"pg_extension",
	"pg_foreign_data_wrapper",
	"pg_foreign_server",
	"pg_user_mapping",
	"pg_foreign_table",
	"pg_policy",
	"pg_replication_origin",
	"pg_default_acl",
	"pg_init_privs",
	"pg_seclabel",
	"pg_shseclabel",
	"pg_collation",
	"pg_parameter_acl",
	"pg_partitioned_table",
	"pg_range",
	"pg_transform",
	"pg_sequence",
	"pg_publication",
	"pg_publication_namespace",
	"pg_publication_rel",
	"pg_subscription",
	"pg_subscription_rel",
}

// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/system_views.sql
var regtypeSystemViewRows = []string{
	"pg_roles",
	"pg_shadow",
	"pg_group",
	"pg_user",
	"pg_policies",
	"pg_rules",
	"pg_views",
	"pg_tables",
	"pg_matviews",
	"pg_indexes",
	"pg_sequences",
	"pg_stats",
	"pg_stats_ext",
	"pg_stats_ext_exprs",
	"pg_publication_tables",
	"pg_locks",
	"pg_cursors",
	"pg_available_extensions",
	"pg_available_extension_versions",
	"pg_prepared_xacts",
	"pg_prepared_statements",
	"pg_seclabels",
	"pg_settings",
	"pg_file_settings",
	"pg_hba_file_rules",
	"pg_ident_file_mappings",
	"pg_timezone_abbrevs",
	"pg_timezone_names",
	"pg_config",
	"pg_shmem_allocations",
	"pg_shmem_allocations_numa",
	"pg_backend_memory_contexts",
	"pg_stat_all_tables",
	"pg_stat_xact_all_tables",
	"pg_stat_sys_tables",
	"pg_stat_xact_sys_tables",
	"pg_stat_user_tables",
	"pg_stat_xact_user_tables",
	"pg_statio_all_tables",
	"pg_statio_sys_tables",
	"pg_statio_user_tables",
	"pg_stat_all_indexes",
	"pg_stat_sys_indexes",
	"pg_stat_user_indexes",
	"pg_statio_all_indexes",
	"pg_statio_sys_indexes",
	"pg_statio_user_indexes",
	"pg_statio_all_sequences",
	"pg_statio_sys_sequences",
	"pg_statio_user_sequences",
	"pg_stat_activity",
	"pg_stat_replication",
	"pg_stat_slru",
	"pg_stat_wal_receiver",
	"pg_stat_recovery_prefetch",
	"pg_stat_subscription",
	"pg_stat_ssl",
	"pg_stat_gssapi",
	"pg_replication_slots",
	"pg_stat_replication_slots",
	"pg_stat_database",
	"pg_stat_database_conflicts",
	"pg_stat_user_functions",
	"pg_stat_xact_user_functions",
	"pg_stat_archiver",
	"pg_stat_bgwriter",
	"pg_stat_checkpointer",
	"pg_stat_io",
	"pg_stat_wal",
	"pg_stat_progress_analyze",
	"pg_stat_progress_vacuum",
	"pg_stat_progress_cluster",
	"pg_stat_progress_create_index",
	"pg_stat_progress_basebackup",
	"pg_stat_progress_copy",
	"pg_user_mappings",
	"pg_replication_origin_status",
	"pg_stat_subscription_stats",
	"pg_wait_events",
	"pg_aios",
}

// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/information_schema.sql
var regtypeInformationNames = []string{
	"cardinal_number",
	"character_data",
	"sql_identifier",
	"information_schema_catalog_name",
	"time_stamp",
	"yes_or_no",
	"applicable_roles",
	"administrable_role_authorizations",
	"attributes",
	"character_sets",
	"check_constraint_routine_usage",
	"check_constraints",
	"collations",
	"collation_character_set_applicability",
	"column_column_usage",
	"column_domain_usage",
	"column_privileges",
	"column_udt_usage",
	"columns",
	"constraint_column_usage",
	"constraint_table_usage",
	"domain_constraints",
	"domain_udt_usage",
	"domains",
	"enabled_roles",
	"key_column_usage",
	"parameters",
	"referential_constraints",
	"role_column_grants",
	"routine_column_usage",
	"routine_privileges",
	"role_routine_grants",
	"routine_routine_usage",
	"routine_sequence_usage",
	"routine_table_usage",
	"routines",
	"schemata",
	"sequences",
	"sql_features",
	"sql_implementation_info",
	"sql_parts",
	"sql_sizing",
	"table_constraints",
	"table_privileges",
	"role_table_grants",
	"tables",
	"transforms",
	"triggered_update_columns",
	"triggers",
	"udt_privileges",
	"role_udt_grants",
	"usage_privileges",
	"role_usage_grants",
	"user_defined_types",
	"view_column_usage",
	"view_routine_usage",
	"view_table_usage",
	"views",
	"data_type_privileges",
	"element_types",
	"_pg_foreign_table_columns",
	"column_options",
	"_pg_foreign_data_wrappers",
	"foreign_data_wrapper_options",
	"foreign_data_wrappers",
	"_pg_foreign_servers",
	"foreign_server_options",
	"foreign_servers",
	"_pg_foreign_tables",
	"foreign_table_options",
	"foreign_tables",
	"_pg_user_mappings",
	"user_mapping_options",
	"user_mappings",
}

func TestReferenceRegtypeInitializedInventory(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		// Every discovered identity remains a shared regtype witness. No pg_type
		// projection or cross-backend comparison of allocated initdb IDs is needed.
		for _, fact := range regtypeInitializedIdentities {
			t.Run(fact.schema+"."+fact.name, func(t *testing.T) {
				input := fact.schema + "." + regtypeIdentifier(fact.name)
				var oid int64
				if err := conn.QueryRowContext(p.ctx, "SELECT "+regtypeLiteral(input)+"::regtype::bigint").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				if fact.fixedOID != 0 && oid != fact.fixedOID {
					t.Fatalf("fixed release type OID %d want %d", oid, fact.fixedOID)
				}
				if oid == 0 {
					t.Fatal("initialized type has zero identity")
				}
				query := "SELECT " + regtypeLiteral(input) + "::regtype AS a,$1::oid::regtype AS b,(" + regtypeLiteral(input) + "::regtype::oid=$1::oid) AS same_identity"
				regtypeBoth(t, p, conn, recorder, referenceSelect(query, []string{"a", "b", "same_identity"}, []string{"REGTYPE", "REGTYPE", "BOOL"}, [][]any{{fact.output, fact.output, true}}, oid))
			})
		}
	})
}

func TestReferenceRegtypeReleaseIdentities(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, fact := range regtypeReleaseIdentities {
			t.Run(fact.name, func(t *testing.T) {
				input := "pg_catalog." + regtypeIdentifier(fact.name)
				query := "SELECT " + regtypeLiteral(input) + "::regtype::bigint AS o," + fmt.Sprint(fact.oid) + "::oid::regtype AS r"
				regtypeBoth(t, p, conn, recorder, referenceSelect(query, []string{"o", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{fact.oid, fact.output}}))
				if fact.array != 0 {
					query = "SELECT " + regtypeLiteral(input+"[]") + "::regtype::bigint AS o," + fmt.Sprint(fact.array) + "::oid::regtype AS r," + regtypeLiteral("pg_catalog."+regtypeIdentifier("_"+fact.name)) + "::regtype AS a"
					regtypeBoth(t, p, conn, recorder, referenceSelect(query, []string{"o", "r", "a"}, []string{"INT8", "REGTYPE", "REGTYPE"}, [][]any{{fact.array, fact.output + "[]", fact.output + "[]"}}))
				}
			})
		}
	})
}

func TestReferenceRegtypeInitializedNames(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, group := range []struct {
			schema string
			names  []string
		}{{"pg_catalog", regtypeCatalogRows}, {"pg_catalog", regtypeSystemViewRows}, {"information_schema", regtypeInformationNames}} {
			for _, name := range group.names {
				output := name
				if group.schema != "pg_catalog" {
					output = group.schema + "." + name
				}
				input := group.schema + "." + regtypeIdentifier(name)
				query := "SELECT " + regtypeLiteral(input) + "::regtype AS a," + regtypeLiteral(input+"[]") + "::regtype AS b,(" + regtypeLiteral(input+"[]") + "::regtype::oid=" + regtypeLiteral(group.schema+"."+regtypeIdentifier("_"+name)) + "::regtype::oid) AS linked"
				regtypeBoth(t, p, conn, recorder, referenceSelect(query, []string{"a", "b", "linked"}, []string{"REGTYPE", "REGTYPE", "BOOL"}, [][]any{{output, output + "[]", true}}))
			}
		}
	})
}
