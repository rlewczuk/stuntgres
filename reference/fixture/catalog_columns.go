package fixture

var PGAggregateColumns = []string{"aggfnoid", "aggkind", "aggnumdirectargs", "aggtransfn", "aggfinalfn", "aggcombinefn", "aggserialfn", "aggdeserialfn", "aggmtransfn", "aggminvtransfn", "aggmfinalfn", "aggfinalextra", "aggmfinalextra", "aggfinalmodify", "aggmfinalmodify", "aggsortop", "aggtranstype", "aggtransspace", "aggmtranstype", "aggmtransspace", "agginitval", "aggminitval"}

var PGAMColumns = []string{"oid", "amname", "amhandler", "amtype"}

var PGAttributeColumns = []string{"attrelid", "attname", "atttypid", "attlen", "attnum", "atttypmod", "attndims", "attbyval", "attalign", "attstorage", "attcompression", "attnotnull", "atthasdef", "atthasmissing", "attidentity", "attgenerated", "attisdropped", "attislocal", "attinhcount", "attcollation", "attstattarget", "attacl", "attoptions", "attfdwoptions", "attmissingval"}

var PGClassColumns = []string{"oid", "relname", "relnamespace", "reltype", "reloftype", "relowner", "relam", "relfilenode", "reltablespace", "relpages", "reltuples", "relallvisible", "relallfrozen", "reltoastrelid", "relhasindex", "relisshared", "relpersistence", "relkind", "relnatts", "relchecks", "relhasrules", "relhastriggers", "relhassubclass", "relrowsecurity", "relforcerowsecurity", "relispopulated", "relreplident", "relispartition", "relrewrite", "relfrozenxid", "relminmxid", "relacl", "reloptions", "relpartbound"}

var PGCollationColumns = []string{"oid", "collname", "collnamespace", "collowner", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "colllocale", "collicurules", "collversion"}

var PGConstraintColumns = []string{"oid", "conname", "connamespace", "contype", "condeferrable", "condeferred", "conenforced", "convalidated", "conrelid", "contypid", "conindid", "conparentid", "confrelid", "confupdtype", "confdeltype", "confmatchtype", "conislocal", "coninhcount", "connoinherit", "conperiod", "conkey", "confkey", "conpfeqop", "conppeqop", "conffeqop", "confdelsetcols", "conexclop", "conbin"}

var PGDatabaseColumns = []string{"oid", "datname", "datdba", "encoding", "datlocprovider", "datistemplate", "datallowconn", "dathasloginevt", "datconnlimit", "datfrozenxid", "datminmxid", "dattablespace", "datcollate", "datctype", "datlocale", "daticurules", "datcollversion", "datacl"}

var PGDBRoleSettingColumns = []string{"setdatabase", "setrole", "setconfig"}

var PGDefaultACLColumns = []string{"oid", "defaclrole", "defaclnamespace", "defaclobjtype", "defaclacl"}

var PGDescriptionColumns = []string{"objoid", "classoid", "objsubid", "description"}

var PGEnumColumns = []string{"oid", "enumtypid", "enumsortorder", "enumlabel"}

var PGExtensionColumns = []string{"oid", "extname", "extowner", "extnamespace", "extrelocatable", "extversion", "extconfig", "extcondition"}

var PGGroupColumns = []string{"groname", "grosysid", "grolist"}

var PGInheritsColumns = []string{"inhrelid", "inhparent", "inhseqno", "inhdetachpending"}

var PGLanguageColumns = []string{"oid", "lanname", "lanowner", "lanispl", "lanpltrusted", "lanplcallfoid", "laninline", "lanvalidator", "lanacl"}

var PGNamespaceColumns = []string{"oid", "nspname", "nspowner", "nspacl"}

var PGParameterACLColumns = []string{"oid", "parname", "paracl"}

var PGRolesColumns = []string{"rolname", "rolsuper", "rolinherit", "rolcreaterole", "rolcreatedb", "rolcanlogin", "rolreplication", "rolconnlimit", "rolpassword", "rolvaliduntil", "rolbypassrls", "rolconfig", "oid"}

var PGSequenceColumns = []string{"seqrelid", "seqtypid", "seqstart", "seqincrement", "seqmax", "seqmin", "seqcache", "seqcycle"}

var PGSequencesColumns = []string{"schemaname", "sequencename", "sequenceowner", "data_type", "start_value", "min_value", "max_value", "increment_by", "cycle", "cache_size", "last_value"}

var PGShDescriptionColumns = []string{"objoid", "classoid", "description"}

var PGTransformColumns = []string{"oid", "trftype", "trflang", "trffromsql", "trftosql"}

var PGTSConfigColumns = []string{"oid", "cfgname", "cfgnamespace", "cfgowner", "cfgparser"}

var PGTypeColumns = []string{"oid", "typname", "typnamespace", "typowner", "typlen", "typbyval", "typtype", "typcategory", "typispreferred", "typisdefined", "typdelim", "typrelid", "typsubscript", "typelem", "typarray", "typinput", "typoutput", "typreceive", "typsend", "typmodin", "typmodout", "typanalyze", "typalign", "typstorage", "typnotnull", "typbasetype", "typtypmod", "typndims", "typcollation", "typdefaultbin", "typdefault", "typacl"}

var PGUserColumns = []string{"usename", "usesysid", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "valuntil", "useconfig"}

var CCUColumns = []string{"table_catalog", "table_schema", "table_name", "column_name", "constraint_catalog", "constraint_schema", "constraint_name"}

var KCUColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog", "table_schema", "table_name", "column_name", "ordinal_position", "position_in_unique_constraint"}

var RTGColumns = []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}

var UGColumns = []string{"grantor", "grantee", "udt_catalog", "udt_schema", "udt_name", "privilege_type", "is_grantable"}

var SFColumns = []string{"feature_id", "feature_name", "sub_feature_id", "sub_feature_name", "is_supported", "is_verified_by", "comments"}

var SIIColumns = []string{"implementation_info_id", "implementation_info_name", "integer_value", "character_value", "comments"}

var SPColumns = []string{"feature_id", "feature_name", "is_supported", "is_verified_by", "comments"}

var SZColumns = []string{"sizing_id", "sizing_name", "supported_value", "comments"}

var IUMColumns = []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name"}
