package reference_test

import (
	"sort"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Released catalog/system-view field inventories, reusing the owning family's
// independent vectors. Condensed providers include only genuine installed attrs;
// the oracle query restricts to those names, never inventing omitted native attrs.
func icProviderInventory(c *icCase) {
	c.t.Helper()
	inventory := map[string][]string{
		"pg_aggregate": fixture.PGAggregateColumns, "pg_am": fixture.PGAMColumns,
		"pg_attribute": fixture.PGAttributeColumns, "pg_class": fixture.PGClassColumns,
		"pg_authid":       strings.Fields("oid rolname rolsuper rolinherit rolcreaterole rolcreatedb rolcanlogin rolreplication rolbypassrls rolconnlimit rolpassword rolvaliduntil"),
		"pg_auth_members": strings.Fields("oid roleid member grantor admin_option inherit_option set_option"),
		"pg_collation":    fixture.PGCollationColumns, "pg_constraint": fixture.PGConstraintColumns,
		"pg_database": fixture.PGDatabaseColumns, "pg_db_role_setting": fixture.PGDBRoleSettingColumns,
		"pg_default_acl": fixture.PGDefaultACLColumns, "pg_description": fixture.PGDescriptionColumns,
		"pg_enum": fixture.PGEnumColumns, "pg_extension": fixture.PGExtensionColumns,
		"pg_init_privs": strings.Fields("objoid classoid objsubid privtype initprivs"),
		"pg_namespace":  fixture.PGNamespaceColumns, "pg_type": fixture.PGTypeColumns,
		"pg_group": fixture.PGGroupColumns, "pg_roles": fixture.PGRolesColumns,
		"pg_language": fixture.PGLanguageColumns, "pg_parameter_acl": fixture.PGParameterACLColumns,
		"pg_sequence": fixture.PGSequenceColumns, "pg_sequences": fixture.PGSequencesColumns,
		"pg_shdescription": fixture.PGShDescriptionColumns, "pg_transform": fixture.PGTransformColumns,
		"pg_ts_config": fixture.PGTSConfigColumns, "pg_user": fixture.PGUserColumns,
		"pg_ts_config_map":                       strings.Fields("mapcfg maptokentype mapseqno mapdict"),
		"pg_shadow":                              strings.Fields("usename usesysid usecreatedb usesuper userepl usebypassrls passwd valuntil useconfig"),
		"pg_inherits":                            fixture.PGInheritsColumns,
		"pg_partitioned_table":                   strings.Fields("partrelid partstrat partnatts partdefid partattrs partclass partcollation partexprs"),
		"pg_event_trigger":                       strings.Fields("oid evtname evtevent evtowner evtfoid evtenabled evttags"),
		"pg_attrdef":                             strings.Fields("oid adrelid adnum adbin"),
		"pg_statistic_ext":                       strings.Fields("oid stxrelid stxname stxnamespace stxowner stxkeys stxstattarget stxkind stxexprs"),
		"pg_statistic_ext_data":                  strings.Fields("stxoid stxdinherit stxdndistinct stxddependencies stxdmcv stxdexpr"),
		"pg_publication":                         strings.Fields("oid pubname pubowner puballtables pubinsert pubupdate pubdelete pubtruncate pubviaroot pubgencols"),
		"pg_publication_namespace":               strings.Fields("oid pnpubid pnnspid"),
		"pg_replication_origin":                  strings.Fields("roident roname"),
		"pg_subscription":                        strings.Fields("oid subdbid subskiplsn subname subowner subenabled subbinary substream subtwophasestate subdisableonerr subpasswordrequired subrunasowner subfailover subconninfo subslotname subsynccommit subpublications suborigin"),
		"pg_subscription_rel":                    strings.Fields("srsubid srrelid srsubstate srsublsn"),
		"pg_proc":                                strings.Fields("oid proname pronamespace proowner prolang procost prorows provariadic prosupport prokind prosecdef proleakproof proisstrict proretset provolatile proparallel pronargs pronargdefaults prorettype proargtypes proallargtypes proargmodes proargnames proargdefaults protrftypes prosrc probin prosqlbody proconfig proacl"),
		"pg_index":                               strings.Fields("indexrelid indrelid indnatts indnkeyatts indisunique indnullsnotdistinct indisprimary indisexclusion indimmediate indisclustered indisvalid indcheckxmin indisready indislive indisreplident indkey indcollation indclass indoption indexprs indpred"),
		"pg_rewrite":                             strings.Fields("oid rulename ev_class ev_type ev_enabled is_instead"),
		"pg_trigger":                             strings.Fields("oid tgrelid tgparentid tgname tgfoid tgtype tgenabled tgisinternal tgconstrrelid tgconstrindid tgconstraint tgdeferrable tginitdeferred tgnargs tgattr tgargs tgqual tgoldtable tgnewtable"),
		"pg_policy":                              strings.Fields("oid polname polrelid polcmd polpermissive polroles polqual polwithcheck"),
		"pg_cast":                                strings.Fields("oid castsource casttarget castfunc castcontext castmethod"),
		"pg_depend":                              strings.Fields("classid objid objsubid refclassid refobjid refobjsubid deptype"),
		"pg_conversion":                          strings.Fields("oid conname connamespace conowner conforencoding contoencoding conproc condefault"),
		"pg_operator":                            strings.Fields("oid oprname oprnamespace oprowner oprkind oprcanmerge oprcanhash oprleft oprright oprresult oprcom oprnegate oprcode oprrest oprjoin"),
		"pg_opclass":                             strings.Fields("oid opcmethod opcname opcnamespace opcowner opcfamily opcintype opcdefault opckeytype"),
		"pg_opfamily":                            strings.Fields("oid opfmethod opfname opfnamespace opfowner"),
		"pg_amop":                                strings.Fields("oid amopfamily amoplefttype amoprighttype amopstrategy amoppurpose amopopr amopmethod amopsortfamily"),
		"pg_amproc":                              strings.Fields("oid amprocfamily amproclefttype amprocrighttype amprocnum amproc"),
		"pg_range":                               strings.Fields("rngtypid rngsubtype rngmultitypid rngcollation rngsubopc rngcanonical rngsubdiff"),
		"pg_tablespace":                          strings.Fields("oid spcname spcowner spcacl spcoptions"),
		"pg_largeobject":                         strings.Fields("loid pageno data"),
		"pg_largeobject_metadata":                strings.Fields("oid lomowner lomacl"),
		"pg_ts_template":                         strings.Fields("oid tmplname tmplnamespace tmplinit tmpllexize"),
		"pg_ts_dict":                             strings.Fields("oid dictname dictnamespace dictowner dicttemplate dictinitoption"),
		"pg_ts_parser":                           strings.Fields("oid prsname prsnamespace prsstart prstoken prsend prsheadline prslextype"),
		"pg_foreign_data_wrapper":                strings.Fields("oid fdwname fdwowner fdwhandler fdwvalidator fdwacl fdwoptions"),
		"pg_foreign_server":                      strings.Fields("oid srvname srvowner srvfdw srvtype srvversion srvacl srvoptions"),
		"pg_foreign_table":                       strings.Fields("ftrelid ftserver ftoptions"),
		"pg_user_mapping":                        strings.Fields("oid umuser umserver umoptions"),
		"pg_user_mappings":                       strings.Fields("umid srvid srvname umuser usename umoptions"),
		"pg_views":                               strings.Fields("schemaname viewname viewowner definition"),
		"pg_shmem_allocations":                   strings.Fields("name off size allocated_size"),
		"pg_shmem_allocations_numa":              strings.Fields("name numa_node size"),
		"pg_wait_events":                         strings.Fields("type name description"),
		"pg_policies":                            strings.Fields("schemaname tablename policyname permissive roles cmd qual with_check"),
		"pg_rules":                               strings.Fields("schemaname tablename rulename definition"),
		"pg_tables":                              strings.Fields("schemaname tablename tableowner tablespace hasindexes hasrules hastriggers rowsecurity"),
		"pg_matviews":                            strings.Fields("schemaname matviewname matviewowner tablespace hasindexes ispopulated definition"),
		"pg_indexes":                             strings.Fields("schemaname tablename indexname tablespace indexdef"),
		"pg_prepared_xacts":                      strings.Fields("transaction gid prepared owner database"),
		"pg_prepared_statements":                 strings.Fields("name statement prepare_time parameter_types result_types from_sql generic_plans custom_plans"),
		"pg_cursors":                             strings.Fields("name statement is_holdable is_binary is_scrollable creation_time"),
		"pg_locks":                               strings.Fields("locktype database relation page tuple virtualxid transactionid classid objid objsubid virtualtransaction pid mode granted fastpath waitstart"),
		"pg_timezone_names":                      strings.Fields("name abbrev utc_offset is_dst"),
		"pg_timezone_abbrevs":                    strings.Fields("abbrev utc_offset is_dst"),
		"pg_available_extensions":                strings.Fields("name default_version installed_version comment"),
		"pg_available_extension_versions":        strings.Fields("name version installed superuser trusted relocatable schema requires comment"),
		"pg_settings":                            strings.Fields("name setting unit category short_desc extra_desc context vartype source min_val max_val enumvals boot_val reset_val sourcefile sourceline pending_restart"),
		"information_schema.columns":             icColumns,
		"information_schema.character_sets":      csColumns,
		"information_schema.collations":          []string{"collation_catalog", "collation_schema", "collation_name", "pad_attribute"},
		"information_schema.schemata":            []string{"catalog_name", "schema_name", "schema_owner", "default_character_set_catalog", "default_character_set_schema", "default_character_set_name", "sql_path"},
		"information_schema.column_options":      fixture.COColumns,
		"information_schema.transforms":          trColumns,
		"information_schema.column_column_usage": colcuColumns,
		"information_schema.column_domain_usage": cduColumns,
		"information_schema.check_constraint_routine_usage": ccruColumns,
		"information_schema.check_constraints":              iccColumns,
		"information_schema.constraint_column_usage":        fixture.CCUColumns,
		"information_schema.domain_constraints":             dcColumns,
		"information_schema.referential_constraints":        ircColumns,
		"information_schema.table_constraints":              tcColumns,
		"information_schema.domains":                        idmColumns,
		"information_schema.user_defined_types":             udtColumns,
		"information_schema.enabled_roles":                  strings.Fields("role_name"),
		"information_schema.key_column_usage":               fixture.KCUColumns,
		"information_schema.routine_sequence_usage":         rsuColumns,
		"information_schema.routine_routine_usage":          rruColumns,
		"information_schema.routines":                       irColumns,
		"information_schema.parameters":                     ipColumns,
		"information_schema.role_column_grants":             strings.Fields("grantor grantee table_catalog table_schema table_name column_name privilege_type is_grantable"),
		"information_schema.role_table_grants":              fixture.RTGColumns,
		"information_schema.role_routine_grants":            rrgColumns,
		"information_schema.routine_privileges":             rrgColumns,
		"information_schema.role_udt_grants":                fixture.UGColumns,
		"information_schema.role_usage_grants":              strings.Fields("grantor grantee object_catalog object_schema object_name object_type privilege_type is_grantable"),
		"information_schema.usage_privileges":               strings.Fields("grantor grantee object_catalog object_schema object_name object_type privilege_type is_grantable"),
		"information_schema.sequences":                      isqColumns,
		"information_schema.sql_features":                   fixture.SFColumns,
		"information_schema.sql_sizing":                     fixture.SZColumns,
		"information_schema.sql_implementation_info":        fixture.SIIColumns,
		"information_schema.sql_parts":                      fixture.SPColumns,
		"information_schema.tables":                         itColumns,
		"information_schema.user_mappings":                  fixture.IUMColumns,
		"information_schema.user_mapping_options":           strings.Fields("authorization_identifier foreign_server_catalog foreign_server_name option_name option_value"),
		"information_schema.view_table_usage":               vtuColumns,
		"information_schema.triggers":                       itrColumns,
		"information_schema.views":                          ivColumns,
		"information_schema.table_privileges":               strings.Fields("grantor grantee table_catalog table_schema table_name privilege_type is_grantable with_hierarchy"),
		"information_schema.data_type_privileges":           strings.Fields("object_catalog object_schema object_name object_type dtd_identifier"),
	}
	icMaintenanceInventory(inventory)
	names := make([]string, 0, len(inventory))
	for name := range inventory {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		schema, table := "pg_catalog", name
		if strings.Contains(name, ".") {
			parts := strings.SplitN(name, ".", 2)
			schema, table = parts[0], parts[1]
		}
		var want [][]any
		for i, field := range inventory[name] {
			ordinal := i + 1
			want = append(want, []any{field, int64(ordinal)})
		}
		sort.Slice(want, func(i, j int) bool { return want[i][1].(int64) < want[j][1].(int64) })
		query := "SELECT column_name,ordinal_position FROM information_schema.columns WHERE table_schema='" + schema + "' AND table_name='" + table + "'"
		if c.b.PG {
			query += " AND column_name IN ('" + strings.Join(inventory[name], "','") + "')"
		}
		query += " ORDER BY ordinal_position"
		s := fixture.Select(query, []string{"column_name", "ordinal_position"}, []string{"NAME", "INT4"}, want)
		c.t.Run(name, func(t *testing.T) {
			child := *c
			child.t = t
			child.run(child.conn, s)
			child.run(child.conn, fixture.Prepared(s))
			kind := "r"
			views := strings.Fields("pg_stat_all_tables pg_stat_sys_tables pg_stat_user_tables pg_stat_progress_basebackup pg_stat_progress_copy pg_stat_progress_create_index pg_stat_progress_vacuum pg_roles pg_group pg_user pg_shadow pg_cursors pg_locks pg_timezone_names pg_timezone_abbrevs pg_available_extensions pg_available_extension_versions pg_sequences pg_views pg_shmem_allocations pg_shmem_allocations_numa pg_wait_events pg_policies pg_rules pg_prepared_statements pg_prepared_xacts pg_indexes pg_tables pg_matviews pg_user_mappings pg_settings")
			for _, view := range views {
				if view == table {
					kind = "v"
				}
			}
			if schema == "information_schema" && table != "sql_features" && table != "sql_sizing" && table != "sql_implementation_info" && table != "sql_parts" {
				kind = "v"
			}
			child.run(child.conn, fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE oid=$1::regclass::oid", []string{"relkind"}, []string{"CHAR"}, [][]any{{kind}}, schema+"."+table))
			if table == "pg_authid" {
				// Independently prescribed real-table facts, not merely presence in
				// the provider-name bag. No credential values are read here.
				dataTypes := []string{"oid", "name", "boolean", "boolean", "boolean", "boolean", "boolean", "boolean", "boolean", "integer", "text", "timestamp with time zone"}
				udts := []string{"oid", "name", "bool", "bool", "bool", "bool", "bool", "bool", "bool", "int4", "text", "timestamptz"}
				var rows [][]any
				for i, field := range inventory[table] {
					facts := map[string]any{}
					if i < 10 {
						facts["is_nullable"] = "NO"
					}
					if i == 1 || i == 10 {
						facts["collation_catalog"], facts["collation_schema"], facts["collation_name"] = c.database, "pg_catalog", "C"
					}
					switch i {
					case 9:
						facts["numeric_precision"], facts["numeric_precision_radix"], facts["numeric_scale"] = int64(32), int64(2), int64(0)
					case 10:
						facts["character_octet_length"] = int64(1073741824)
					case 11:
						facts["datetime_precision"] = int64(6)
					}
					rows = append(rows, icRow(c.database, schema, table, field, i+1, dataTypes[i], "pg_catalog", udts[i], facts))
				}
				s := icProjection(c.b.PG, "table_schema='pg_catalog' AND table_name='pg_authid'", rows)
				child.run(child.conn, s)
				child.run(child.conn, fixture.Prepared(s))
				child.run(child.hold(s.Query), s)
			}
			if table == "pg_stat_progress_basebackup" {
				var rows [][]any
				for i, field := range inventory[table] {
					dataType, udt := "bigint", "int8"
					facts := map[string]any{"is_updatable": "NO", "numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}
					if i == 0 {
						dataType, udt = "integer", "int4"
						facts["numeric_precision"] = int64(32)
					}
					if i == 1 {
						dataType, udt = "text", "text"
						facts = map[string]any{"is_updatable": "NO", "character_octet_length": int64(1073741824)}
					}
					rows = append(rows, icRow(c.database, schema, table, field, i+1, dataType, "pg_catalog", udt, facts))
				}
				s := icProjection(c.b.PG, "table_schema='pg_catalog' AND table_name='pg_stat_progress_basebackup'", rows)
				child.run(child.conn, s)
				child.run(child.conn, fixture.Prepared(s))
				child.run(child.hold(s.Query), s)
			}
			if table == "pg_subscription_rel" {
				rows := icSubscriptionRelRows(c.database)
				s := icProjection(c.b.PG, "table_schema='pg_catalog' AND table_name='pg_subscription_rel'", rows)
				child.run(child.conn, s)
				child.run(child.conn, fixture.Prepared(s))
				child.run(child.hold(s.Query), s)
			}
		})
	}
	if !c.b.PG {
		// Unfiltered native relation bag catches extra synthesized inventory rows.
		var want [][]any
		for _, name := range names {
			schema, table := "pg_catalog", name
			if strings.Contains(name, ".") {
				parts := strings.SplitN(name, ".", 2)
				schema, table = parts[0], parts[1]
			}
			want = append(want, []any{schema, table})
		}
		s := fixture.Select("SELECT DISTINCT table_schema,table_name FROM information_schema.columns WHERE table_schema IN ('pg_catalog','information_schema')", []string{"table_schema", "table_name"}, []string{"NAME", "NAME"}, want)
		s.Bag = true
		c.run(c.conn, s)
	}
	// Metadata visibility follows genuine authority, including on a statement
	// prepared by the administrator before switching to an ordinary reader.
	query := "SELECT table_name,column_name,ordinal_position FROM information_schema.columns WHERE table_schema='pg_catalog' AND table_name IN ('pg_authid','pg_shadow') ORDER BY table_name,ordinal_position"
	held := c.hold(query)
	checkProtectedRoles := func(visible bool) {
		c.t.Helper()
		var want [][]any
		if visible {
			for _, table := range []string{"pg_authid", "pg_shadow"} {
				for i, name := range inventory[table] {
					want = append(want, []any{table, name, int64(i + 1)})
				}
			}
		}
		s := fixture.Select(query, []string{"table_name", "column_name", "ordinal_position"}, []string{"NAME", "NAME", "INT4"}, want)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(held, s)
	}
	checkProtectedRoles(true)
	c.exec("CREATE ROLE ic_reader", "SET ROLE ic_reader")
	checkProtectedRoles(false)
	// PUBLIC table SELECT exposes all four exact fields to an ordinary role.
	public := icProjection(c.b.PG, "table_schema='pg_catalog' AND table_name='pg_subscription_rel'", icSubscriptionRelRows(c.database))
	c.run(c.conn, public)
	c.run(c.conn, fixture.Prepared(public))
	c.run(c.hold(public.Query), public)
	c.exec("RESET ROLE", "GRANT pg_read_all_data TO ic_reader", "SET ROLE ic_reader")
	checkProtectedRoles(true)
	c.exec("RESET ROLE", "REVOKE pg_read_all_data FROM ic_reader", "SET ROLE ic_reader")
	checkProtectedRoles(false)
	c.exec("RESET ROLE")
}

func icSubscriptionRelRows(database string) [][]any {
	return [][]any{
		icRow(database, "pg_catalog", "pg_subscription_rel", "srsubid", 1, "oid", "pg_catalog", "oid", map[string]any{"is_nullable": "NO"}),
		icRow(database, "pg_catalog", "pg_subscription_rel", "srrelid", 2, "oid", "pg_catalog", "oid", map[string]any{"is_nullable": "NO"}),
		icRow(database, "pg_catalog", "pg_subscription_rel", "srsubstate", 3, "\"char\"", "pg_catalog", "char", map[string]any{"is_nullable": "NO"}),
		icRow(database, "pg_catalog", "pg_subscription_rel", "srsublsn", 4, "pg_lsn", "pg_catalog", "pg_lsn", nil),
	}
}
