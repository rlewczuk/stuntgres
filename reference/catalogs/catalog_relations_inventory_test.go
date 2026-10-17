package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independent PostgreSQL REL_18_0 contract, not read from engine metadata.
// All implicit BKI_LOOKUP(_OPT) and explicit DECLARE_(ARRAY_)FOREIGN_KEY(_OPT)
// annotations in src/include/catalog/Makefile's 64 CATALOG_HEADERS are included;
// only genbki.pl's synthetic encoding target is omitted. Catalog.pm defines
// oidvector/Oid[] array flags and optional-zero flags. The 219 ordered column
// pairs and multiplicities were independently cross-checked against REL_18_0
// src/test/regress/expected/oidjoins.out NOTICEs. Sources:
// https://github.com/postgres/postgres/tree/REL_18_0/src/include/catalog
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/Catalog.pm
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/genbki.pl
var catalogRelationsRelease219 = [][]any{
	{int64(1255), "{pronamespace}", int64(2615), "{oid}", false, false},                   // pg_proc -> pg_namespace
	{int64(1255), "{proowner}", int64(1260), "{oid}", false, false},                       // pg_proc -> pg_authid
	{int64(1255), "{prolang}", int64(2612), "{oid}", false, false},                        // pg_proc -> pg_language
	{int64(1255), "{provariadic}", int64(1247), "{oid}", false, true},                     // pg_proc -> pg_type
	{int64(1255), "{prosupport}", int64(1255), "{oid}", false, true},                      // pg_proc -> pg_proc
	{int64(1255), "{prorettype}", int64(1247), "{oid}", false, false},                     // pg_proc -> pg_type
	{int64(1255), "{proargtypes}", int64(1247), "{oid}", true, false},                     // pg_proc -> pg_type
	{int64(1255), "{proallargtypes}", int64(1247), "{oid}", true, false},                  // pg_proc -> pg_type
	{int64(1255), "{protrftypes}", int64(1247), "{oid}", true, false},                     // pg_proc -> pg_type
	{int64(1247), "{typnamespace}", int64(2615), "{oid}", false, false},                   // pg_type -> pg_namespace
	{int64(1247), "{typowner}", int64(1260), "{oid}", false, false},                       // pg_type -> pg_authid
	{int64(1247), "{typrelid}", int64(1259), "{oid}", false, true},                        // pg_type -> pg_class
	{int64(1247), "{typsubscript}", int64(1255), "{oid}", false, true},                    // pg_type -> pg_proc
	{int64(1247), "{typelem}", int64(1247), "{oid}", false, true},                         // pg_type -> pg_type
	{int64(1247), "{typarray}", int64(1247), "{oid}", false, true},                        // pg_type -> pg_type
	{int64(1247), "{typinput}", int64(1255), "{oid}", false, false},                       // pg_type -> pg_proc
	{int64(1247), "{typoutput}", int64(1255), "{oid}", false, false},                      // pg_type -> pg_proc
	{int64(1247), "{typreceive}", int64(1255), "{oid}", false, true},                      // pg_type -> pg_proc
	{int64(1247), "{typsend}", int64(1255), "{oid}", false, true},                         // pg_type -> pg_proc
	{int64(1247), "{typmodin}", int64(1255), "{oid}", false, true},                        // pg_type -> pg_proc
	{int64(1247), "{typmodout}", int64(1255), "{oid}", false, true},                       // pg_type -> pg_proc
	{int64(1247), "{typanalyze}", int64(1255), "{oid}", false, true},                      // pg_type -> pg_proc
	{int64(1247), "{typbasetype}", int64(1247), "{oid}", false, true},                     // pg_type -> pg_type
	{int64(1247), "{typcollation}", int64(3456), "{oid}", false, true},                    // pg_type -> pg_collation
	{int64(1249), "{attrelid}", int64(1259), "{oid}", false, false},                       // pg_attribute -> pg_class
	{int64(1249), "{atttypid}", int64(1247), "{oid}", false, true},                        // pg_attribute -> pg_type
	{int64(1249), "{attcollation}", int64(3456), "{oid}", false, true},                    // pg_attribute -> pg_collation
	{int64(1259), "{relnamespace}", int64(2615), "{oid}", false, false},                   // pg_class -> pg_namespace
	{int64(1259), "{reltype}", int64(1247), "{oid}", false, true},                         // pg_class -> pg_type
	{int64(1259), "{reloftype}", int64(1247), "{oid}", false, true},                       // pg_class -> pg_type
	{int64(1259), "{relowner}", int64(1260), "{oid}", false, false},                       // pg_class -> pg_authid
	{int64(1259), "{relam}", int64(2601), "{oid}", false, true},                           // pg_class -> pg_am
	{int64(1259), "{reltablespace}", int64(1213), "{oid}", false, true},                   // pg_class -> pg_tablespace
	{int64(1259), "{reltoastrelid}", int64(1259), "{oid}", false, true},                   // pg_class -> pg_class
	{int64(1259), "{relrewrite}", int64(1259), "{oid}", false, true},                      // pg_class -> pg_class
	{int64(2604), "{adrelid}", int64(1259), "{oid}", false, false},                        // pg_attrdef -> pg_class
	{int64(2604), "{adrelid,adnum}", int64(1249), "{attrelid,attnum}", false, false},      // pg_attrdef -> pg_attribute
	{int64(2606), "{connamespace}", int64(2615), "{oid}", false, false},                   // pg_constraint -> pg_namespace
	{int64(2606), "{conrelid}", int64(1259), "{oid}", false, true},                        // pg_constraint -> pg_class
	{int64(2606), "{contypid}", int64(1247), "{oid}", false, true},                        // pg_constraint -> pg_type
	{int64(2606), "{conindid}", int64(1259), "{oid}", false, true},                        // pg_constraint -> pg_class
	{int64(2606), "{conparentid}", int64(2606), "{oid}", false, true},                     // pg_constraint -> pg_constraint
	{int64(2606), "{confrelid}", int64(1259), "{oid}", false, true},                       // pg_constraint -> pg_class
	{int64(2606), "{conpfeqop}", int64(2617), "{oid}", true, false},                       // pg_constraint -> pg_operator
	{int64(2606), "{conppeqop}", int64(2617), "{oid}", true, false},                       // pg_constraint -> pg_operator
	{int64(2606), "{conffeqop}", int64(2617), "{oid}", true, false},                       // pg_constraint -> pg_operator
	{int64(2606), "{conexclop}", int64(2617), "{oid}", true, false},                       // pg_constraint -> pg_operator
	{int64(2606), "{conrelid,conkey}", int64(1249), "{attrelid,attnum}", true, true},      // pg_constraint -> pg_attribute
	{int64(2606), "{confrelid,confkey}", int64(1249), "{attrelid,attnum}", true, false},   // pg_constraint -> pg_attribute
	{int64(2611), "{inhrelid}", int64(1259), "{oid}", false, false},                       // pg_inherits -> pg_class
	{int64(2611), "{inhparent}", int64(1259), "{oid}", false, false},                      // pg_inherits -> pg_class
	{int64(2610), "{indexrelid}", int64(1259), "{oid}", false, false},                     // pg_index -> pg_class
	{int64(2610), "{indrelid}", int64(1259), "{oid}", false, false},                       // pg_index -> pg_class
	{int64(2610), "{indcollation}", int64(3456), "{oid}", true, true},                     // pg_index -> pg_collation
	{int64(2610), "{indclass}", int64(2616), "{oid}", true, false},                        // pg_index -> pg_opclass
	{int64(2610), "{indrelid,indkey}", int64(1249), "{attrelid,attnum}", true, true},      // pg_index -> pg_attribute
	{int64(2617), "{oprnamespace}", int64(2615), "{oid}", false, false},                   // pg_operator -> pg_namespace
	{int64(2617), "{oprowner}", int64(1260), "{oid}", false, false},                       // pg_operator -> pg_authid
	{int64(2617), "{oprleft}", int64(1247), "{oid}", false, true},                         // pg_operator -> pg_type
	{int64(2617), "{oprright}", int64(1247), "{oid}", false, false},                       // pg_operator -> pg_type
	{int64(2617), "{oprresult}", int64(1247), "{oid}", false, true},                       // pg_operator -> pg_type
	{int64(2617), "{oprcom}", int64(2617), "{oid}", false, true},                          // pg_operator -> pg_operator
	{int64(2617), "{oprnegate}", int64(2617), "{oid}", false, true},                       // pg_operator -> pg_operator
	{int64(2617), "{oprcode}", int64(1255), "{oid}", false, true},                         // pg_operator -> pg_proc
	{int64(2617), "{oprrest}", int64(1255), "{oid}", false, true},                         // pg_operator -> pg_proc
	{int64(2617), "{oprjoin}", int64(1255), "{oid}", false, true},                         // pg_operator -> pg_proc
	{int64(2753), "{opfmethod}", int64(2601), "{oid}", false, false},                      // pg_opfamily -> pg_am
	{int64(2753), "{opfnamespace}", int64(2615), "{oid}", false, false},                   // pg_opfamily -> pg_namespace
	{int64(2753), "{opfowner}", int64(1260), "{oid}", false, false},                       // pg_opfamily -> pg_authid
	{int64(2616), "{opcmethod}", int64(2601), "{oid}", false, false},                      // pg_opclass -> pg_am
	{int64(2616), "{opcnamespace}", int64(2615), "{oid}", false, false},                   // pg_opclass -> pg_namespace
	{int64(2616), "{opcowner}", int64(1260), "{oid}", false, false},                       // pg_opclass -> pg_authid
	{int64(2616), "{opcfamily}", int64(2753), "{oid}", false, false},                      // pg_opclass -> pg_opfamily
	{int64(2616), "{opcintype}", int64(1247), "{oid}", false, false},                      // pg_opclass -> pg_type
	{int64(2616), "{opckeytype}", int64(1247), "{oid}", false, true},                      // pg_opclass -> pg_type
	{int64(2601), "{amhandler}", int64(1255), "{oid}", false, false},                      // pg_am -> pg_proc
	{int64(2602), "{amopfamily}", int64(2753), "{oid}", false, false},                     // pg_amop -> pg_opfamily
	{int64(2602), "{amoplefttype}", int64(1247), "{oid}", false, false},                   // pg_amop -> pg_type
	{int64(2602), "{amoprighttype}", int64(1247), "{oid}", false, false},                  // pg_amop -> pg_type
	{int64(2602), "{amopopr}", int64(2617), "{oid}", false, false},                        // pg_amop -> pg_operator
	{int64(2602), "{amopmethod}", int64(2601), "{oid}", false, false},                     // pg_amop -> pg_am
	{int64(2602), "{amopsortfamily}", int64(2753), "{oid}", false, true},                  // pg_amop -> pg_opfamily
	{int64(2603), "{amprocfamily}", int64(2753), "{oid}", false, false},                   // pg_amproc -> pg_opfamily
	{int64(2603), "{amproclefttype}", int64(1247), "{oid}", false, false},                 // pg_amproc -> pg_type
	{int64(2603), "{amprocrighttype}", int64(1247), "{oid}", false, false},                // pg_amproc -> pg_type
	{int64(2603), "{amproc}", int64(1255), "{oid}", false, false},                         // pg_amproc -> pg_proc
	{int64(2612), "{lanowner}", int64(1260), "{oid}", false, false},                       // pg_language -> pg_authid
	{int64(2612), "{lanplcallfoid}", int64(1255), "{oid}", false, true},                   // pg_language -> pg_proc
	{int64(2612), "{laninline}", int64(1255), "{oid}", false, true},                       // pg_language -> pg_proc
	{int64(2612), "{lanvalidator}", int64(1255), "{oid}", false, true},                    // pg_language -> pg_proc
	{int64(2995), "{lomowner}", int64(1260), "{oid}", false, false},                       // pg_largeobject_metadata -> pg_authid
	{int64(2613), "{loid}", int64(2995), "{oid}", false, false},                           // pg_largeobject -> pg_largeobject_metadata
	{int64(2600), "{aggfnoid}", int64(1255), "{oid}", false, false},                       // pg_aggregate -> pg_proc
	{int64(2600), "{aggtransfn}", int64(1255), "{oid}", false, false},                     // pg_aggregate -> pg_proc
	{int64(2600), "{aggfinalfn}", int64(1255), "{oid}", false, true},                      // pg_aggregate -> pg_proc
	{int64(2600), "{aggcombinefn}", int64(1255), "{oid}", false, true},                    // pg_aggregate -> pg_proc
	{int64(2600), "{aggserialfn}", int64(1255), "{oid}", false, true},                     // pg_aggregate -> pg_proc
	{int64(2600), "{aggdeserialfn}", int64(1255), "{oid}", false, true},                   // pg_aggregate -> pg_proc
	{int64(2600), "{aggmtransfn}", int64(1255), "{oid}", false, true},                     // pg_aggregate -> pg_proc
	{int64(2600), "{aggminvtransfn}", int64(1255), "{oid}", false, true},                  // pg_aggregate -> pg_proc
	{int64(2600), "{aggmfinalfn}", int64(1255), "{oid}", false, true},                     // pg_aggregate -> pg_proc
	{int64(2600), "{aggsortop}", int64(2617), "{oid}", false, true},                       // pg_aggregate -> pg_operator
	{int64(2600), "{aggtranstype}", int64(1247), "{oid}", false, false},                   // pg_aggregate -> pg_type
	{int64(2600), "{aggmtranstype}", int64(1247), "{oid}", false, true},                   // pg_aggregate -> pg_type
	{int64(2619), "{starelid}", int64(1259), "{oid}", false, false},                       // pg_statistic -> pg_class
	{int64(2619), "{staop1}", int64(2617), "{oid}", false, true},                          // pg_statistic -> pg_operator
	{int64(2619), "{staop2}", int64(2617), "{oid}", false, true},                          // pg_statistic -> pg_operator
	{int64(2619), "{staop3}", int64(2617), "{oid}", false, true},                          // pg_statistic -> pg_operator
	{int64(2619), "{staop4}", int64(2617), "{oid}", false, true},                          // pg_statistic -> pg_operator
	{int64(2619), "{staop5}", int64(2617), "{oid}", false, true},                          // pg_statistic -> pg_operator
	{int64(2619), "{stacoll1}", int64(3456), "{oid}", false, true},                        // pg_statistic -> pg_collation
	{int64(2619), "{stacoll2}", int64(3456), "{oid}", false, true},                        // pg_statistic -> pg_collation
	{int64(2619), "{stacoll3}", int64(3456), "{oid}", false, true},                        // pg_statistic -> pg_collation
	{int64(2619), "{stacoll4}", int64(3456), "{oid}", false, true},                        // pg_statistic -> pg_collation
	{int64(2619), "{stacoll5}", int64(3456), "{oid}", false, true},                        // pg_statistic -> pg_collation
	{int64(2619), "{starelid,staattnum}", int64(1249), "{attrelid,attnum}", false, false}, // pg_statistic -> pg_attribute
	{int64(3381), "{stxrelid}", int64(1259), "{oid}", false, false},                       // pg_statistic_ext -> pg_class
	{int64(3381), "{stxnamespace}", int64(2615), "{oid}", false, false},                   // pg_statistic_ext -> pg_namespace
	{int64(3381), "{stxowner}", int64(1260), "{oid}", false, false},                       // pg_statistic_ext -> pg_authid
	{int64(3381), "{stxrelid,stxkeys}", int64(1249), "{attrelid,attnum}", true, false},    // pg_statistic_ext -> pg_attribute
	{int64(3429), "{stxoid}", int64(3381), "{oid}", false, false},                         // pg_statistic_ext_data -> pg_statistic_ext
	{int64(2618), "{ev_class}", int64(1259), "{oid}", false, false},                       // pg_rewrite -> pg_class
	{int64(2620), "{tgrelid}", int64(1259), "{oid}", false, false},                        // pg_trigger -> pg_class
	{int64(2620), "{tgparentid}", int64(2620), "{oid}", false, true},                      // pg_trigger -> pg_trigger
	{int64(2620), "{tgfoid}", int64(1255), "{oid}", false, false},                         // pg_trigger -> pg_proc
	{int64(2620), "{tgconstrrelid}", int64(1259), "{oid}", false, true},                   // pg_trigger -> pg_class
	{int64(2620), "{tgconstrindid}", int64(1259), "{oid}", false, true},                   // pg_trigger -> pg_class
	{int64(2620), "{tgconstraint}", int64(2606), "{oid}", false, true},                    // pg_trigger -> pg_constraint
	{int64(2620), "{tgrelid,tgattr}", int64(1249), "{attrelid,attnum}", true, false},      // pg_trigger -> pg_attribute
	{int64(3466), "{evtowner}", int64(1260), "{oid}", false, false},                       // pg_event_trigger -> pg_authid
	{int64(3466), "{evtfoid}", int64(1255), "{oid}", false, false},                        // pg_event_trigger -> pg_proc
	{int64(2609), "{classoid}", int64(1259), "{oid}", false, false},                       // pg_description -> pg_class
	{int64(2605), "{castsource}", int64(1247), "{oid}", false, false},                     // pg_cast -> pg_type
	{int64(2605), "{casttarget}", int64(1247), "{oid}", false, false},                     // pg_cast -> pg_type
	{int64(2605), "{castfunc}", int64(1255), "{oid}", false, true},                        // pg_cast -> pg_proc
	{int64(3501), "{enumtypid}", int64(1247), "{oid}", false, false},                      // pg_enum -> pg_type
	{int64(2615), "{nspowner}", int64(1260), "{oid}", false, false},                       // pg_namespace -> pg_authid
	{int64(2607), "{connamespace}", int64(2615), "{oid}", false, false},                   // pg_conversion -> pg_namespace
	{int64(2607), "{conowner}", int64(1260), "{oid}", false, false},                       // pg_conversion -> pg_authid
	{int64(2607), "{conproc}", int64(1255), "{oid}", false, false},                        // pg_conversion -> pg_proc
	{int64(2608), "{classid}", int64(1259), "{oid}", false, false},                        // pg_depend -> pg_class
	{int64(2608), "{refclassid}", int64(1259), "{oid}", false, false},                     // pg_depend -> pg_class
	{int64(1262), "{datdba}", int64(1260), "{oid}", false, false},                         // pg_database -> pg_authid
	{int64(1262), "{dattablespace}", int64(1213), "{oid}", false, false},                  // pg_database -> pg_tablespace
	{int64(2964), "{setdatabase}", int64(1262), "{oid}", false, true},                     // pg_db_role_setting -> pg_database
	{int64(2964), "{setrole}", int64(1260), "{oid}", false, true},                         // pg_db_role_setting -> pg_authid
	{int64(1213), "{spcowner}", int64(1260), "{oid}", false, false},                       // pg_tablespace -> pg_authid
	{int64(1261), "{roleid}", int64(1260), "{oid}", false, false},                         // pg_auth_members -> pg_authid
	{int64(1261), "{member}", int64(1260), "{oid}", false, false},                         // pg_auth_members -> pg_authid
	{int64(1261), "{grantor}", int64(1260), "{oid}", false, false},                        // pg_auth_members -> pg_authid
	{int64(1214), "{dbid}", int64(1262), "{oid}", false, true},                            // pg_shdepend -> pg_database
	{int64(1214), "{classid}", int64(1259), "{oid}", false, false},                        // pg_shdepend -> pg_class
	{int64(1214), "{refclassid}", int64(1259), "{oid}", false, false},                     // pg_shdepend -> pg_class
	{int64(2396), "{classoid}", int64(1259), "{oid}", false, false},                       // pg_shdescription -> pg_class
	{int64(3602), "{cfgnamespace}", int64(2615), "{oid}", false, false},                   // pg_ts_config -> pg_namespace
	{int64(3602), "{cfgowner}", int64(1260), "{oid}", false, false},                       // pg_ts_config -> pg_authid
	{int64(3602), "{cfgparser}", int64(3601), "{oid}", false, false},                      // pg_ts_config -> pg_ts_parser
	{int64(3603), "{mapcfg}", int64(3602), "{oid}", false, false},                         // pg_ts_config_map -> pg_ts_config
	{int64(3603), "{mapdict}", int64(3600), "{oid}", false, false},                        // pg_ts_config_map -> pg_ts_dict
	{int64(3600), "{dictnamespace}", int64(2615), "{oid}", false, false},                  // pg_ts_dict -> pg_namespace
	{int64(3600), "{dictowner}", int64(1260), "{oid}", false, false},                      // pg_ts_dict -> pg_authid
	{int64(3600), "{dicttemplate}", int64(3764), "{oid}", false, false},                   // pg_ts_dict -> pg_ts_template
	{int64(3601), "{prsnamespace}", int64(2615), "{oid}", false, false},                   // pg_ts_parser -> pg_namespace
	{int64(3601), "{prsstart}", int64(1255), "{oid}", false, false},                       // pg_ts_parser -> pg_proc
	{int64(3601), "{prstoken}", int64(1255), "{oid}", false, false},                       // pg_ts_parser -> pg_proc
	{int64(3601), "{prsend}", int64(1255), "{oid}", false, false},                         // pg_ts_parser -> pg_proc
	{int64(3601), "{prsheadline}", int64(1255), "{oid}", false, true},                     // pg_ts_parser -> pg_proc
	{int64(3601), "{prslextype}", int64(1255), "{oid}", false, false},                     // pg_ts_parser -> pg_proc
	{int64(3764), "{tmplnamespace}", int64(2615), "{oid}", false, false},                  // pg_ts_template -> pg_namespace
	{int64(3764), "{tmplinit}", int64(1255), "{oid}", false, true},                        // pg_ts_template -> pg_proc
	{int64(3764), "{tmpllexize}", int64(1255), "{oid}", false, false},                     // pg_ts_template -> pg_proc
	{int64(3079), "{extowner}", int64(1260), "{oid}", false, false},                       // pg_extension -> pg_authid
	{int64(3079), "{extnamespace}", int64(2615), "{oid}", false, false},                   // pg_extension -> pg_namespace
	{int64(3079), "{extconfig}", int64(1259), "{oid}", true, false},                       // pg_extension -> pg_class
	{int64(2328), "{fdwowner}", int64(1260), "{oid}", false, false},                       // pg_foreign_data_wrapper -> pg_authid
	{int64(2328), "{fdwhandler}", int64(1255), "{oid}", false, true},                      // pg_foreign_data_wrapper -> pg_proc
	{int64(2328), "{fdwvalidator}", int64(1255), "{oid}", false, true},                    // pg_foreign_data_wrapper -> pg_proc
	{int64(1417), "{srvowner}", int64(1260), "{oid}", false, false},                       // pg_foreign_server -> pg_authid
	{int64(1417), "{srvfdw}", int64(2328), "{oid}", false, false},                         // pg_foreign_server -> pg_foreign_data_wrapper
	{int64(1418), "{umuser}", int64(1260), "{oid}", false, true},                          // pg_user_mapping -> pg_authid
	{int64(1418), "{umserver}", int64(1417), "{oid}", false, false},                       // pg_user_mapping -> pg_foreign_server
	{int64(3118), "{ftrelid}", int64(1259), "{oid}", false, false},                        // pg_foreign_table -> pg_class
	{int64(3118), "{ftserver}", int64(1417), "{oid}", false, false},                       // pg_foreign_table -> pg_foreign_server
	{int64(3256), "{polrelid}", int64(1259), "{oid}", false, false},                       // pg_policy -> pg_class
	{int64(3256), "{polroles}", int64(1260), "{oid}", true, true},                         // pg_policy -> pg_authid
	{int64(826), "{defaclrole}", int64(1260), "{oid}", false, false},                      // pg_default_acl -> pg_authid
	{int64(826), "{defaclnamespace}", int64(2615), "{oid}", false, true},                  // pg_default_acl -> pg_namespace
	{int64(3394), "{classoid}", int64(1259), "{oid}", false, false},                       // pg_init_privs -> pg_class
	{int64(3596), "{classoid}", int64(1259), "{oid}", false, false},                       // pg_seclabel -> pg_class
	{int64(3592), "{classoid}", int64(1259), "{oid}", false, false},                       // pg_shseclabel -> pg_class
	{int64(3456), "{collnamespace}", int64(2615), "{oid}", false, false},                  // pg_collation -> pg_namespace
	{int64(3456), "{collowner}", int64(1260), "{oid}", false, false},                      // pg_collation -> pg_authid
	{int64(3350), "{partrelid}", int64(1259), "{oid}", false, false},                      // pg_partitioned_table -> pg_class
	{int64(3350), "{partdefid}", int64(1259), "{oid}", false, true},                       // pg_partitioned_table -> pg_class
	{int64(3350), "{partclass}", int64(2616), "{oid}", true, false},                       // pg_partitioned_table -> pg_opclass
	{int64(3350), "{partcollation}", int64(3456), "{oid}", true, true},                    // pg_partitioned_table -> pg_collation
	{int64(3350), "{partrelid,partattrs}", int64(1249), "{attrelid,attnum}", true, true},  // pg_partitioned_table -> pg_attribute
	{int64(3541), "{rngtypid}", int64(1247), "{oid}", false, false},                       // pg_range -> pg_type
	{int64(3541), "{rngsubtype}", int64(1247), "{oid}", false, false},                     // pg_range -> pg_type
	{int64(3541), "{rngmultitypid}", int64(1247), "{oid}", false, false},                  // pg_range -> pg_type
	{int64(3541), "{rngcollation}", int64(3456), "{oid}", false, true},                    // pg_range -> pg_collation
	{int64(3541), "{rngsubopc}", int64(2616), "{oid}", false, false},                      // pg_range -> pg_opclass
	{int64(3541), "{rngcanonical}", int64(1255), "{oid}", false, true},                    // pg_range -> pg_proc
	{int64(3541), "{rngsubdiff}", int64(1255), "{oid}", false, true},                      // pg_range -> pg_proc
	{int64(3576), "{trftype}", int64(1247), "{oid}", false, false},                        // pg_transform -> pg_type
	{int64(3576), "{trflang}", int64(2612), "{oid}", false, false},                        // pg_transform -> pg_language
	{int64(3576), "{trffromsql}", int64(1255), "{oid}", false, true},                      // pg_transform -> pg_proc
	{int64(3576), "{trftosql}", int64(1255), "{oid}", false, true},                        // pg_transform -> pg_proc
	{int64(2224), "{seqrelid}", int64(1259), "{oid}", false, false},                       // pg_sequence -> pg_class
	{int64(2224), "{seqtypid}", int64(1247), "{oid}", false, false},                       // pg_sequence -> pg_type
	{int64(6104), "{pubowner}", int64(1260), "{oid}", false, false},                       // pg_publication -> pg_authid
	{int64(6237), "{pnpubid}", int64(6104), "{oid}", false, false},                        // pg_publication_namespace -> pg_publication
	{int64(6237), "{pnnspid}", int64(2615), "{oid}", false, false},                        // pg_publication_namespace -> pg_namespace
	{int64(6106), "{prpubid}", int64(6104), "{oid}", false, false},                        // pg_publication_rel -> pg_publication
	{int64(6106), "{prrelid}", int64(1259), "{oid}", false, false},                        // pg_publication_rel -> pg_class
	{int64(6100), "{subdbid}", int64(1262), "{oid}", false, false},                        // pg_subscription -> pg_database
	{int64(6100), "{subowner}", int64(1260), "{oid}", false, false},                       // pg_subscription -> pg_authid
	{int64(6102), "{srsubid}", int64(6100), "{oid}", false, false},                        // pg_subscription_rel -> pg_subscription
	{int64(6102), "{srrelid}", int64(1259), "{oid}", false, false},                        // pg_subscription_rel -> pg_class
}

const catalogRelationsProjection = "SELECT fktable::oid, fkcols, pktable::oid, pkcols, is_array, is_opt FROM "

func catalogRelationsInventoryStep(source string) fixture.Step {
	s := fixture.Select(catalogRelationsProjection+source,
		[]string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"},
		[]string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, catalogRelationsRelease219)
	s.Bag = true
	return s
}

func catalogRelationsInventory(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, source := range []string{"pg_catalog.pg_get_catalog_foreign_keys()", "pg_get_catalog_foreign_keys()"} {
		for _, prepared := range []bool{false, true} {
			s := catalogRelationsInventoryStep(source)
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		}
	}
}

func TestReferenceCatalogRelationsInventory(t *testing.T) {
	p := fixture.NewScenario(t)
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) {
		catalogRelationsInventory(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE cr_inventory_parent (id integer PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE cr_inventory_child (id integer REFERENCES cr_inventory_parent(id))", 0))
		catalogRelationsInventory(t, p, b)
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		runConn := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		runConn(fixture.Exec("BEGIN", 0))
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		query := "CREATE TABLE cr_inventory_rollback (id integer REFERENCES cr_inventory_parent(id))"
		if b.PG {
			runConn(fixture.Exec(query, 0))
			fixture.PgDescriptionExecutorRows(t, p, b, conn, catalogRelationsInventoryStep("pg_get_catalog_foreign_keys()"))
		} else {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
			s.ErrorStage = "execute"
			runConn(s)
		}
		runConn(fixture.Exec("ROLLBACK", 0))
		catalogRelationsInventory(t, p, b)
		runConn(fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_class WHERE relname='cr_inventory_rollback'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// Shared supported DML rollback also leaves the complete linkage bag
		// unchanged; it does not require adjacent transactional CREATE support.
		runConn(fixture.Exec("BEGIN", 0))
		runConn(fixture.Exec("INSERT INTO cr_inventory_parent VALUES (1)", 1))
		runConn(fixture.Exec("INSERT INTO cr_inventory_child VALUES (1)", 1))
		fixture.PgDescriptionExecutorRows(t, p, b, conn, catalogRelationsInventoryStep("pg_get_catalog_foreign_keys()"))
		runConn(fixture.Exec("ROLLBACK", 0))
		runConn(fixture.Select("SELECT count(*) AS n FROM cr_inventory_child", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		catalogRelationsInventory(t, p, b)
	})
}
