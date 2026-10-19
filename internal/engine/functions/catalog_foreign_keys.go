package functions

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqltype"
)

// PostgreSQL REL_18_0 catalog linkage metadata: every implicit BKI_LOOKUP(_OPT)
// and explicit DECLARE_(ARRAY_)FOREIGN_KEY(_OPT) from the 64 CATALOG_HEADERS in
// src/include/catalog/Makefile. Catalog.pm supplies oidvector/Oid[] and optional
// zero semantics; genbki.pl excludes the synthetic encoding target only.
// https://github.com/postgres/postgres/tree/REL_18_0/src/include/catalog
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/Catalog.pm
// This describes catalog metadata, not queryable catalog providers or user FKs.
// Two columns suffice for the release inventory; an empty second name means one.
type catalogForeignKey struct {
	from, to        uint32
	source, target  [2]string
	array, optional bool
}

var catalogForeignKeys = [...]catalogForeignKey{
	{1255, 2615, [2]string{"pronamespace"}, [2]string{"oid"}, false, false},                         // pg_proc -> pg_namespace
	{1255, 1260, [2]string{"proowner"}, [2]string{"oid"}, false, false},                             // pg_proc -> pg_authid
	{1255, 2612, [2]string{"prolang"}, [2]string{"oid"}, false, false},                              // pg_proc -> pg_language
	{1255, 1247, [2]string{"provariadic"}, [2]string{"oid"}, false, true},                           // pg_proc -> pg_type
	{1255, 1255, [2]string{"prosupport"}, [2]string{"oid"}, false, true},                            // pg_proc -> pg_proc
	{1255, 1247, [2]string{"prorettype"}, [2]string{"oid"}, false, false},                           // pg_proc -> pg_type
	{1255, 1247, [2]string{"proargtypes"}, [2]string{"oid"}, true, false},                           // pg_proc -> pg_type
	{1255, 1247, [2]string{"proallargtypes"}, [2]string{"oid"}, true, false},                        // pg_proc -> pg_type
	{1255, 1247, [2]string{"protrftypes"}, [2]string{"oid"}, true, false},                           // pg_proc -> pg_type
	{1247, 2615, [2]string{"typnamespace"}, [2]string{"oid"}, false, false},                         // pg_type -> pg_namespace
	{1247, 1260, [2]string{"typowner"}, [2]string{"oid"}, false, false},                             // pg_type -> pg_authid
	{1247, 1259, [2]string{"typrelid"}, [2]string{"oid"}, false, true},                              // pg_type -> pg_class
	{1247, 1255, [2]string{"typsubscript"}, [2]string{"oid"}, false, true},                          // pg_type -> pg_proc
	{1247, 1247, [2]string{"typelem"}, [2]string{"oid"}, false, true},                               // pg_type -> pg_type
	{1247, 1247, [2]string{"typarray"}, [2]string{"oid"}, false, true},                              // pg_type -> pg_type
	{1247, 1255, [2]string{"typinput"}, [2]string{"oid"}, false, false},                             // pg_type -> pg_proc
	{1247, 1255, [2]string{"typoutput"}, [2]string{"oid"}, false, false},                            // pg_type -> pg_proc
	{1247, 1255, [2]string{"typreceive"}, [2]string{"oid"}, false, true},                            // pg_type -> pg_proc
	{1247, 1255, [2]string{"typsend"}, [2]string{"oid"}, false, true},                               // pg_type -> pg_proc
	{1247, 1255, [2]string{"typmodin"}, [2]string{"oid"}, false, true},                              // pg_type -> pg_proc
	{1247, 1255, [2]string{"typmodout"}, [2]string{"oid"}, false, true},                             // pg_type -> pg_proc
	{1247, 1255, [2]string{"typanalyze"}, [2]string{"oid"}, false, true},                            // pg_type -> pg_proc
	{1247, 1247, [2]string{"typbasetype"}, [2]string{"oid"}, false, true},                           // pg_type -> pg_type
	{1247, 3456, [2]string{"typcollation"}, [2]string{"oid"}, false, true},                          // pg_type -> pg_collation
	{1249, 1259, [2]string{"attrelid"}, [2]string{"oid"}, false, false},                             // pg_attribute -> pg_class
	{1249, 1247, [2]string{"atttypid"}, [2]string{"oid"}, false, true},                              // pg_attribute -> pg_type
	{1249, 3456, [2]string{"attcollation"}, [2]string{"oid"}, false, true},                          // pg_attribute -> pg_collation
	{1259, 2615, [2]string{"relnamespace"}, [2]string{"oid"}, false, false},                         // pg_class -> pg_namespace
	{1259, 1247, [2]string{"reltype"}, [2]string{"oid"}, false, true},                               // pg_class -> pg_type
	{1259, 1247, [2]string{"reloftype"}, [2]string{"oid"}, false, true},                             // pg_class -> pg_type
	{1259, 1260, [2]string{"relowner"}, [2]string{"oid"}, false, false},                             // pg_class -> pg_authid
	{1259, 2601, [2]string{"relam"}, [2]string{"oid"}, false, true},                                 // pg_class -> pg_am
	{1259, 1213, [2]string{"reltablespace"}, [2]string{"oid"}, false, true},                         // pg_class -> pg_tablespace
	{1259, 1259, [2]string{"reltoastrelid"}, [2]string{"oid"}, false, true},                         // pg_class -> pg_class
	{1259, 1259, [2]string{"relrewrite"}, [2]string{"oid"}, false, true},                            // pg_class -> pg_class
	{2604, 1259, [2]string{"adrelid"}, [2]string{"oid"}, false, false},                              // pg_attrdef -> pg_class
	{2604, 1249, [2]string{"adrelid", "adnum"}, [2]string{"attrelid", "attnum"}, false, false},      // pg_attrdef -> pg_attribute
	{2606, 2615, [2]string{"connamespace"}, [2]string{"oid"}, false, false},                         // pg_constraint -> pg_namespace
	{2606, 1259, [2]string{"conrelid"}, [2]string{"oid"}, false, true},                              // pg_constraint -> pg_class
	{2606, 1247, [2]string{"contypid"}, [2]string{"oid"}, false, true},                              // pg_constraint -> pg_type
	{2606, 1259, [2]string{"conindid"}, [2]string{"oid"}, false, true},                              // pg_constraint -> pg_class
	{2606, 2606, [2]string{"conparentid"}, [2]string{"oid"}, false, true},                           // pg_constraint -> pg_constraint
	{2606, 1259, [2]string{"confrelid"}, [2]string{"oid"}, false, true},                             // pg_constraint -> pg_class
	{2606, 2617, [2]string{"conpfeqop"}, [2]string{"oid"}, true, false},                             // pg_constraint -> pg_operator
	{2606, 2617, [2]string{"conppeqop"}, [2]string{"oid"}, true, false},                             // pg_constraint -> pg_operator
	{2606, 2617, [2]string{"conffeqop"}, [2]string{"oid"}, true, false},                             // pg_constraint -> pg_operator
	{2606, 2617, [2]string{"conexclop"}, [2]string{"oid"}, true, false},                             // pg_constraint -> pg_operator
	{2606, 1249, [2]string{"conrelid", "conkey"}, [2]string{"attrelid", "attnum"}, true, true},      // pg_constraint -> pg_attribute
	{2606, 1249, [2]string{"confrelid", "confkey"}, [2]string{"attrelid", "attnum"}, true, false},   // pg_constraint -> pg_attribute
	{2611, 1259, [2]string{"inhrelid"}, [2]string{"oid"}, false, false},                             // pg_inherits -> pg_class
	{2611, 1259, [2]string{"inhparent"}, [2]string{"oid"}, false, false},                            // pg_inherits -> pg_class
	{2610, 1259, [2]string{"indexrelid"}, [2]string{"oid"}, false, false},                           // pg_index -> pg_class
	{2610, 1259, [2]string{"indrelid"}, [2]string{"oid"}, false, false},                             // pg_index -> pg_class
	{2610, 3456, [2]string{"indcollation"}, [2]string{"oid"}, true, true},                           // pg_index -> pg_collation
	{2610, 2616, [2]string{"indclass"}, [2]string{"oid"}, true, false},                              // pg_index -> pg_opclass
	{2610, 1249, [2]string{"indrelid", "indkey"}, [2]string{"attrelid", "attnum"}, true, true},      // pg_index -> pg_attribute
	{2617, 2615, [2]string{"oprnamespace"}, [2]string{"oid"}, false, false},                         // pg_operator -> pg_namespace
	{2617, 1260, [2]string{"oprowner"}, [2]string{"oid"}, false, false},                             // pg_operator -> pg_authid
	{2617, 1247, [2]string{"oprleft"}, [2]string{"oid"}, false, true},                               // pg_operator -> pg_type
	{2617, 1247, [2]string{"oprright"}, [2]string{"oid"}, false, false},                             // pg_operator -> pg_type
	{2617, 1247, [2]string{"oprresult"}, [2]string{"oid"}, false, true},                             // pg_operator -> pg_type
	{2617, 2617, [2]string{"oprcom"}, [2]string{"oid"}, false, true},                                // pg_operator -> pg_operator
	{2617, 2617, [2]string{"oprnegate"}, [2]string{"oid"}, false, true},                             // pg_operator -> pg_operator
	{2617, 1255, [2]string{"oprcode"}, [2]string{"oid"}, false, true},                               // pg_operator -> pg_proc
	{2617, 1255, [2]string{"oprrest"}, [2]string{"oid"}, false, true},                               // pg_operator -> pg_proc
	{2617, 1255, [2]string{"oprjoin"}, [2]string{"oid"}, false, true},                               // pg_operator -> pg_proc
	{2753, 2601, [2]string{"opfmethod"}, [2]string{"oid"}, false, false},                            // pg_opfamily -> pg_am
	{2753, 2615, [2]string{"opfnamespace"}, [2]string{"oid"}, false, false},                         // pg_opfamily -> pg_namespace
	{2753, 1260, [2]string{"opfowner"}, [2]string{"oid"}, false, false},                             // pg_opfamily -> pg_authid
	{2616, 2601, [2]string{"opcmethod"}, [2]string{"oid"}, false, false},                            // pg_opclass -> pg_am
	{2616, 2615, [2]string{"opcnamespace"}, [2]string{"oid"}, false, false},                         // pg_opclass -> pg_namespace
	{2616, 1260, [2]string{"opcowner"}, [2]string{"oid"}, false, false},                             // pg_opclass -> pg_authid
	{2616, 2753, [2]string{"opcfamily"}, [2]string{"oid"}, false, false},                            // pg_opclass -> pg_opfamily
	{2616, 1247, [2]string{"opcintype"}, [2]string{"oid"}, false, false},                            // pg_opclass -> pg_type
	{2616, 1247, [2]string{"opckeytype"}, [2]string{"oid"}, false, true},                            // pg_opclass -> pg_type
	{2601, 1255, [2]string{"amhandler"}, [2]string{"oid"}, false, false},                            // pg_am -> pg_proc
	{2602, 2753, [2]string{"amopfamily"}, [2]string{"oid"}, false, false},                           // pg_amop -> pg_opfamily
	{2602, 1247, [2]string{"amoplefttype"}, [2]string{"oid"}, false, false},                         // pg_amop -> pg_type
	{2602, 1247, [2]string{"amoprighttype"}, [2]string{"oid"}, false, false},                        // pg_amop -> pg_type
	{2602, 2617, [2]string{"amopopr"}, [2]string{"oid"}, false, false},                              // pg_amop -> pg_operator
	{2602, 2601, [2]string{"amopmethod"}, [2]string{"oid"}, false, false},                           // pg_amop -> pg_am
	{2602, 2753, [2]string{"amopsortfamily"}, [2]string{"oid"}, false, true},                        // pg_amop -> pg_opfamily
	{2603, 2753, [2]string{"amprocfamily"}, [2]string{"oid"}, false, false},                         // pg_amproc -> pg_opfamily
	{2603, 1247, [2]string{"amproclefttype"}, [2]string{"oid"}, false, false},                       // pg_amproc -> pg_type
	{2603, 1247, [2]string{"amprocrighttype"}, [2]string{"oid"}, false, false},                      // pg_amproc -> pg_type
	{2603, 1255, [2]string{"amproc"}, [2]string{"oid"}, false, false},                               // pg_amproc -> pg_proc
	{2612, 1260, [2]string{"lanowner"}, [2]string{"oid"}, false, false},                             // pg_language -> pg_authid
	{2612, 1255, [2]string{"lanplcallfoid"}, [2]string{"oid"}, false, true},                         // pg_language -> pg_proc
	{2612, 1255, [2]string{"laninline"}, [2]string{"oid"}, false, true},                             // pg_language -> pg_proc
	{2612, 1255, [2]string{"lanvalidator"}, [2]string{"oid"}, false, true},                          // pg_language -> pg_proc
	{2995, 1260, [2]string{"lomowner"}, [2]string{"oid"}, false, false},                             // pg_largeobject_metadata -> pg_authid
	{2613, 2995, [2]string{"loid"}, [2]string{"oid"}, false, false},                                 // pg_largeobject -> pg_largeobject_metadata
	{2600, 1255, [2]string{"aggfnoid"}, [2]string{"oid"}, false, false},                             // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggtransfn"}, [2]string{"oid"}, false, false},                           // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggfinalfn"}, [2]string{"oid"}, false, true},                            // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggcombinefn"}, [2]string{"oid"}, false, true},                          // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggserialfn"}, [2]string{"oid"}, false, true},                           // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggdeserialfn"}, [2]string{"oid"}, false, true},                         // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggmtransfn"}, [2]string{"oid"}, false, true},                           // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggminvtransfn"}, [2]string{"oid"}, false, true},                        // pg_aggregate -> pg_proc
	{2600, 1255, [2]string{"aggmfinalfn"}, [2]string{"oid"}, false, true},                           // pg_aggregate -> pg_proc
	{2600, 2617, [2]string{"aggsortop"}, [2]string{"oid"}, false, true},                             // pg_aggregate -> pg_operator
	{2600, 1247, [2]string{"aggtranstype"}, [2]string{"oid"}, false, false},                         // pg_aggregate -> pg_type
	{2600, 1247, [2]string{"aggmtranstype"}, [2]string{"oid"}, false, true},                         // pg_aggregate -> pg_type
	{2619, 1259, [2]string{"starelid"}, [2]string{"oid"}, false, false},                             // pg_statistic -> pg_class
	{2619, 2617, [2]string{"staop1"}, [2]string{"oid"}, false, true},                                // pg_statistic -> pg_operator
	{2619, 2617, [2]string{"staop2"}, [2]string{"oid"}, false, true},                                // pg_statistic -> pg_operator
	{2619, 2617, [2]string{"staop3"}, [2]string{"oid"}, false, true},                                // pg_statistic -> pg_operator
	{2619, 2617, [2]string{"staop4"}, [2]string{"oid"}, false, true},                                // pg_statistic -> pg_operator
	{2619, 2617, [2]string{"staop5"}, [2]string{"oid"}, false, true},                                // pg_statistic -> pg_operator
	{2619, 3456, [2]string{"stacoll1"}, [2]string{"oid"}, false, true},                              // pg_statistic -> pg_collation
	{2619, 3456, [2]string{"stacoll2"}, [2]string{"oid"}, false, true},                              // pg_statistic -> pg_collation
	{2619, 3456, [2]string{"stacoll3"}, [2]string{"oid"}, false, true},                              // pg_statistic -> pg_collation
	{2619, 3456, [2]string{"stacoll4"}, [2]string{"oid"}, false, true},                              // pg_statistic -> pg_collation
	{2619, 3456, [2]string{"stacoll5"}, [2]string{"oid"}, false, true},                              // pg_statistic -> pg_collation
	{2619, 1249, [2]string{"starelid", "staattnum"}, [2]string{"attrelid", "attnum"}, false, false}, // pg_statistic -> pg_attribute
	{3381, 1259, [2]string{"stxrelid"}, [2]string{"oid"}, false, false},                             // pg_statistic_ext -> pg_class
	{3381, 2615, [2]string{"stxnamespace"}, [2]string{"oid"}, false, false},                         // pg_statistic_ext -> pg_namespace
	{3381, 1260, [2]string{"stxowner"}, [2]string{"oid"}, false, false},                             // pg_statistic_ext -> pg_authid
	{3381, 1249, [2]string{"stxrelid", "stxkeys"}, [2]string{"attrelid", "attnum"}, true, false},    // pg_statistic_ext -> pg_attribute
	{3429, 3381, [2]string{"stxoid"}, [2]string{"oid"}, false, false},                               // pg_statistic_ext_data -> pg_statistic_ext
	{2618, 1259, [2]string{"ev_class"}, [2]string{"oid"}, false, false},                             // pg_rewrite -> pg_class
	{2620, 1259, [2]string{"tgrelid"}, [2]string{"oid"}, false, false},                              // pg_trigger -> pg_class
	{2620, 2620, [2]string{"tgparentid"}, [2]string{"oid"}, false, true},                            // pg_trigger -> pg_trigger
	{2620, 1255, [2]string{"tgfoid"}, [2]string{"oid"}, false, false},                               // pg_trigger -> pg_proc
	{2620, 1259, [2]string{"tgconstrrelid"}, [2]string{"oid"}, false, true},                         // pg_trigger -> pg_class
	{2620, 1259, [2]string{"tgconstrindid"}, [2]string{"oid"}, false, true},                         // pg_trigger -> pg_class
	{2620, 2606, [2]string{"tgconstraint"}, [2]string{"oid"}, false, true},                          // pg_trigger -> pg_constraint
	{2620, 1249, [2]string{"tgrelid", "tgattr"}, [2]string{"attrelid", "attnum"}, true, false},      // pg_trigger -> pg_attribute
	{3466, 1260, [2]string{"evtowner"}, [2]string{"oid"}, false, false},                             // pg_event_trigger -> pg_authid
	{3466, 1255, [2]string{"evtfoid"}, [2]string{"oid"}, false, false},                              // pg_event_trigger -> pg_proc
	{2609, 1259, [2]string{"classoid"}, [2]string{"oid"}, false, false},                             // pg_description -> pg_class
	{2605, 1247, [2]string{"castsource"}, [2]string{"oid"}, false, false},                           // pg_cast -> pg_type
	{2605, 1247, [2]string{"casttarget"}, [2]string{"oid"}, false, false},                           // pg_cast -> pg_type
	{2605, 1255, [2]string{"castfunc"}, [2]string{"oid"}, false, true},                              // pg_cast -> pg_proc
	{3501, 1247, [2]string{"enumtypid"}, [2]string{"oid"}, false, false},                            // pg_enum -> pg_type
	{2615, 1260, [2]string{"nspowner"}, [2]string{"oid"}, false, false},                             // pg_namespace -> pg_authid
	{2607, 2615, [2]string{"connamespace"}, [2]string{"oid"}, false, false},                         // pg_conversion -> pg_namespace
	{2607, 1260, [2]string{"conowner"}, [2]string{"oid"}, false, false},                             // pg_conversion -> pg_authid
	{2607, 1255, [2]string{"conproc"}, [2]string{"oid"}, false, false},                              // pg_conversion -> pg_proc
	{2608, 1259, [2]string{"classid"}, [2]string{"oid"}, false, false},                              // pg_depend -> pg_class
	{2608, 1259, [2]string{"refclassid"}, [2]string{"oid"}, false, false},                           // pg_depend -> pg_class
	{1262, 1260, [2]string{"datdba"}, [2]string{"oid"}, false, false},                               // pg_database -> pg_authid
	{1262, 1213, [2]string{"dattablespace"}, [2]string{"oid"}, false, false},                        // pg_database -> pg_tablespace
	{2964, 1262, [2]string{"setdatabase"}, [2]string{"oid"}, false, true},                           // pg_db_role_setting -> pg_database
	{2964, 1260, [2]string{"setrole"}, [2]string{"oid"}, false, true},                               // pg_db_role_setting -> pg_authid
	{1213, 1260, [2]string{"spcowner"}, [2]string{"oid"}, false, false},                             // pg_tablespace -> pg_authid
	{1261, 1260, [2]string{"roleid"}, [2]string{"oid"}, false, false},                               // pg_auth_members -> pg_authid
	{1261, 1260, [2]string{"member"}, [2]string{"oid"}, false, false},                               // pg_auth_members -> pg_authid
	{1261, 1260, [2]string{"grantor"}, [2]string{"oid"}, false, false},                              // pg_auth_members -> pg_authid
	{1214, 1262, [2]string{"dbid"}, [2]string{"oid"}, false, true},                                  // pg_shdepend -> pg_database
	{1214, 1259, [2]string{"classid"}, [2]string{"oid"}, false, false},                              // pg_shdepend -> pg_class
	{1214, 1259, [2]string{"refclassid"}, [2]string{"oid"}, false, false},                           // pg_shdepend -> pg_class
	{2396, 1259, [2]string{"classoid"}, [2]string{"oid"}, false, false},                             // pg_shdescription -> pg_class
	{3602, 2615, [2]string{"cfgnamespace"}, [2]string{"oid"}, false, false},                         // pg_ts_config -> pg_namespace
	{3602, 1260, [2]string{"cfgowner"}, [2]string{"oid"}, false, false},                             // pg_ts_config -> pg_authid
	{3602, 3601, [2]string{"cfgparser"}, [2]string{"oid"}, false, false},                            // pg_ts_config -> pg_ts_parser
	{3603, 3602, [2]string{"mapcfg"}, [2]string{"oid"}, false, false},                               // pg_ts_config_map -> pg_ts_config
	{3603, 3600, [2]string{"mapdict"}, [2]string{"oid"}, false, false},                              // pg_ts_config_map -> pg_ts_dict
	{3600, 2615, [2]string{"dictnamespace"}, [2]string{"oid"}, false, false},                        // pg_ts_dict -> pg_namespace
	{3600, 1260, [2]string{"dictowner"}, [2]string{"oid"}, false, false},                            // pg_ts_dict -> pg_authid
	{3600, 3764, [2]string{"dicttemplate"}, [2]string{"oid"}, false, false},                         // pg_ts_dict -> pg_ts_template
	{3601, 2615, [2]string{"prsnamespace"}, [2]string{"oid"}, false, false},                         // pg_ts_parser -> pg_namespace
	{3601, 1255, [2]string{"prsstart"}, [2]string{"oid"}, false, false},                             // pg_ts_parser -> pg_proc
	{3601, 1255, [2]string{"prstoken"}, [2]string{"oid"}, false, false},                             // pg_ts_parser -> pg_proc
	{3601, 1255, [2]string{"prsend"}, [2]string{"oid"}, false, false},                               // pg_ts_parser -> pg_proc
	{3601, 1255, [2]string{"prsheadline"}, [2]string{"oid"}, false, true},                           // pg_ts_parser -> pg_proc
	{3601, 1255, [2]string{"prslextype"}, [2]string{"oid"}, false, false},                           // pg_ts_parser -> pg_proc
	{3764, 2615, [2]string{"tmplnamespace"}, [2]string{"oid"}, false, false},                        // pg_ts_template -> pg_namespace
	{3764, 1255, [2]string{"tmplinit"}, [2]string{"oid"}, false, true},                              // pg_ts_template -> pg_proc
	{3764, 1255, [2]string{"tmpllexize"}, [2]string{"oid"}, false, false},                           // pg_ts_template -> pg_proc
	{3079, 1260, [2]string{"extowner"}, [2]string{"oid"}, false, false},                             // pg_extension -> pg_authid
	{3079, 2615, [2]string{"extnamespace"}, [2]string{"oid"}, false, false},                         // pg_extension -> pg_namespace
	{3079, 1259, [2]string{"extconfig"}, [2]string{"oid"}, true, false},                             // pg_extension -> pg_class
	{2328, 1260, [2]string{"fdwowner"}, [2]string{"oid"}, false, false},                             // pg_foreign_data_wrapper -> pg_authid
	{2328, 1255, [2]string{"fdwhandler"}, [2]string{"oid"}, false, true},                            // pg_foreign_data_wrapper -> pg_proc
	{2328, 1255, [2]string{"fdwvalidator"}, [2]string{"oid"}, false, true},                          // pg_foreign_data_wrapper -> pg_proc
	{1417, 1260, [2]string{"srvowner"}, [2]string{"oid"}, false, false},                             // pg_foreign_server -> pg_authid
	{1417, 2328, [2]string{"srvfdw"}, [2]string{"oid"}, false, false},                               // pg_foreign_server -> pg_foreign_data_wrapper
	{1418, 1260, [2]string{"umuser"}, [2]string{"oid"}, false, true},                                // pg_user_mapping -> pg_authid
	{1418, 1417, [2]string{"umserver"}, [2]string{"oid"}, false, false},                             // pg_user_mapping -> pg_foreign_server
	{3118, 1259, [2]string{"ftrelid"}, [2]string{"oid"}, false, false},                              // pg_foreign_table -> pg_class
	{3118, 1417, [2]string{"ftserver"}, [2]string{"oid"}, false, false},                             // pg_foreign_table -> pg_foreign_server
	{3256, 1259, [2]string{"polrelid"}, [2]string{"oid"}, false, false},                             // pg_policy -> pg_class
	{3256, 1260, [2]string{"polroles"}, [2]string{"oid"}, true, true},                               // pg_policy -> pg_authid
	{826, 1260, [2]string{"defaclrole"}, [2]string{"oid"}, false, false},                            // pg_default_acl -> pg_authid
	{826, 2615, [2]string{"defaclnamespace"}, [2]string{"oid"}, false, true},                        // pg_default_acl -> pg_namespace
	{3394, 1259, [2]string{"classoid"}, [2]string{"oid"}, false, false},                             // pg_init_privs -> pg_class
	{3596, 1259, [2]string{"classoid"}, [2]string{"oid"}, false, false},                             // pg_seclabel -> pg_class
	{3592, 1259, [2]string{"classoid"}, [2]string{"oid"}, false, false},                             // pg_shseclabel -> pg_class
	{3456, 2615, [2]string{"collnamespace"}, [2]string{"oid"}, false, false},                        // pg_collation -> pg_namespace
	{3456, 1260, [2]string{"collowner"}, [2]string{"oid"}, false, false},                            // pg_collation -> pg_authid
	{3350, 1259, [2]string{"partrelid"}, [2]string{"oid"}, false, false},                            // pg_partitioned_table -> pg_class
	{3350, 1259, [2]string{"partdefid"}, [2]string{"oid"}, false, true},                             // pg_partitioned_table -> pg_class
	{3350, 2616, [2]string{"partclass"}, [2]string{"oid"}, true, false},                             // pg_partitioned_table -> pg_opclass
	{3350, 3456, [2]string{"partcollation"}, [2]string{"oid"}, true, true},                          // pg_partitioned_table -> pg_collation
	{3350, 1249, [2]string{"partrelid", "partattrs"}, [2]string{"attrelid", "attnum"}, true, true},  // pg_partitioned_table -> pg_attribute
	{3541, 1247, [2]string{"rngtypid"}, [2]string{"oid"}, false, false},                             // pg_range -> pg_type
	{3541, 1247, [2]string{"rngsubtype"}, [2]string{"oid"}, false, false},                           // pg_range -> pg_type
	{3541, 1247, [2]string{"rngmultitypid"}, [2]string{"oid"}, false, false},                        // pg_range -> pg_type
	{3541, 3456, [2]string{"rngcollation"}, [2]string{"oid"}, false, true},                          // pg_range -> pg_collation
	{3541, 2616, [2]string{"rngsubopc"}, [2]string{"oid"}, false, false},                            // pg_range -> pg_opclass
	{3541, 1255, [2]string{"rngcanonical"}, [2]string{"oid"}, false, true},                          // pg_range -> pg_proc
	{3541, 1255, [2]string{"rngsubdiff"}, [2]string{"oid"}, false, true},                            // pg_range -> pg_proc
	{3576, 1247, [2]string{"trftype"}, [2]string{"oid"}, false, false},                              // pg_transform -> pg_type
	{3576, 2612, [2]string{"trflang"}, [2]string{"oid"}, false, false},                              // pg_transform -> pg_language
	{3576, 1255, [2]string{"trffromsql"}, [2]string{"oid"}, false, true},                            // pg_transform -> pg_proc
	{3576, 1255, [2]string{"trftosql"}, [2]string{"oid"}, false, true},                              // pg_transform -> pg_proc
	{2224, 1259, [2]string{"seqrelid"}, [2]string{"oid"}, false, false},                             // pg_sequence -> pg_class
	{2224, 1247, [2]string{"seqtypid"}, [2]string{"oid"}, false, false},                             // pg_sequence -> pg_type
	{6104, 1260, [2]string{"pubowner"}, [2]string{"oid"}, false, false},                             // pg_publication -> pg_authid
	{6237, 6104, [2]string{"pnpubid"}, [2]string{"oid"}, false, false},                              // pg_publication_namespace -> pg_publication
	{6237, 2615, [2]string{"pnnspid"}, [2]string{"oid"}, false, false},                              // pg_publication_namespace -> pg_namespace
	{6106, 6104, [2]string{"prpubid"}, [2]string{"oid"}, false, false},                              // pg_publication_rel -> pg_publication
	{6106, 1259, [2]string{"prrelid"}, [2]string{"oid"}, false, false},                              // pg_publication_rel -> pg_class
	{6100, 1262, [2]string{"subdbid"}, [2]string{"oid"}, false, false},                              // pg_subscription -> pg_database
	{6100, 1260, [2]string{"subowner"}, [2]string{"oid"}, false, false},                             // pg_subscription -> pg_authid
	{6102, 6100, [2]string{"srsubid"}, [2]string{"oid"}, false, false},                              // pg_subscription_rel -> pg_subscription
	{6102, 1259, [2]string{"srrelid"}, [2]string{"oid"}, false, false},                              // pg_subscription_rel -> pg_class
}

// Immutable descriptor-owned metadata is shared by all invocations of an
// analyzed call. Only the cursor index is invocation-private; neither source
// arrays nor REGCLASS spelling is allocated/recomputed while advancing rows.
type catalogForeignKeyValues struct {
	fields [len(catalogForeignKeys)][4]sqltype.Value
}

func newCatalogForeignKeyValues(shape *sqltype.Descriptor) (*catalogForeignKeyValues, error) {
	if shape == nil || !shape.IsComposite() || shape.CompositeLen() != 6 {
		return nil, fmt.Errorf("stuntgres: invalid foreign-key metadata shape")
	}
	result := new(catalogForeignKeyValues)
	arrayType := shape.CompositeMember(1).Usage.Type
	tableType := shape.CompositeMember(0).Usage.Type
	arrays := make(map[[2]string]sqltype.Value)
	tables := make(map[uint32]sqltype.Value)
	columns := func(names [2]string) (sqltype.Value, error) {
		if value, ok := arrays[names]; ok {
			return value, nil
		}
		count := 1
		if names[1] != "" {
			count = 2
		}
		values := make([]sqltype.Value, count)
		for i := range values {
			values[i] = sqltype.String(arrayType.Element(), names[i])
		}
		value, err := sqltype.OwnedArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: 1, Length: int32(count)}}, values)
		if err == nil {
			arrays[names] = value
		}
		return value, err
	}
	table := func(oid uint32) sqltype.Value {
		if value, ok := tables[oid]; ok {
			return value
		}
		// REGCLASS's existing datum representation is canonical decimal text; the
		// ordinary captured cursor output resolves names, including numeric fallback.
		value := sqltype.String(tableType, strconv.FormatUint(uint64(oid), 10))
		tables[oid] = value
		return value
	}
	for i, link := range catalogForeignKeys {
		source, err := columns(link.source)
		if err != nil {
			return nil, err
		}
		target, err := columns(link.target)
		if err != nil {
			return nil, err
		}
		result.fields[i] = [4]sqltype.Value{table(link.from), source, table(link.to), target}
	}
	return result, nil
}
