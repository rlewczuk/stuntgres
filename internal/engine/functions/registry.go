package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Definition registers one builtin family with the engine's function
// dispatch. Names are claimed case-insensitively unless ExactSpelling is set;
// Bind owns exact-spelling resolution, argument analysis and every diagnostic
// for a claimed call.
type Definition struct {
	Names []string
	// ExactSpelling claims only the exact (parser-folded) names, so other
	// spellings keep the engine's existing missing-function path.
	ExactSpelling bool
	// SharedGates asks the engine to apply its shared call gates before Bind:
	// argument labels are rejected, then ResolutionOnly calls get the
	// missing-signature diagnostics. Other spellings of a case-insensitively
	// claimed name resolve only unless OwnsSpelling retains the family's boundary.
	SharedGates bool
	// OwnsLabels exempts the definition from the shared label gate; Bind then
	// owns every argument-label diagnostic. It requires SharedGates.
	OwnsLabels bool
	// OwnsSpelling retains a claimed family's own exact-spelling diagnostic
	// boundary after child transformation and namespace lookup. It requires
	// SharedGates; labels and ResolutionOnly still pass those shared gates.
	OwnsSpelling bool
	// Claims, when set, is the engine chain's admission predicate for the
	// family and replaces Names matching; Names still lists every claimed
	// name. It keeps the chain's exact, folded or lowercased comparison.
	Claims func(name string) bool
	Bind   func(b Binder, call parser.FunctionExpr) (expr.Expr, error)
	// ProbeCall is pure family-owned applicability for pseudo signatures.
	// It shares the provider's target-selection helpers; it never calls Bind.
	ProbeCall CallShape
	// ResultCall resolves a selected pseudo return through the family's existing
	// pure type rule. Targets have already passed ProbeCall; no provider runs.
	ResultCall func(CallTypeContext, parser.FunctionExpr, []sqltype.Usage) (sqltype.Usage, bool)
	NamedCall  NamedCallShape
}

// The static table is the registry: no init() registration. Names must not
// overlap any family that is still dispatched by the engine's name chain.
var definitions = [...]Definition{
	{Names: []string{"xmltext", "xmlcomment"}, ExactSpelling: true, SharedGates: true, Bind: bindXMLTextCall},
	{Names: []string{"xml_is_well_formed", "xml_is_well_formed_document", "xml_is_well_formed_content", "xmlexists"}, ExactSpelling: true, SharedGates: true, Bind: bindXMLPredicateCall},
	{Names: []string{"suppress_redundant_updates_trigger", "tsvector_update_trigger", "tsvector_update_trigger_column"}, ExactSpelling: true, SharedGates: true, Bind: bindTriggerBuiltin},
	{Names: []string{"triggered_change_notification"}, ExactSpelling: true, Bind: bindTcn},
	{Names: []string{"autoinc"}, ExactSpelling: true, SharedGates: true, Bind: bindAutoinc},
	{Names: []string{"pg_trigger_depth"}, ExactSpelling: true, SharedGates: true, Bind: bindTriggerDepth},
	{Names: eventTriggerRewriteNames, ExactSpelling: true, SharedGates: true, Bind: bindEventTriggerRewrite},
	{Names: sessionActivityNames[:], ExactSpelling: true, SharedGates: true, Bind: bindSessionActivity},
	{Names: sessionEndpointNames[:], ExactSpelling: true, SharedGates: true, Bind: bindSessionEndpoint},
	{Names: statisticsIONames, ExactSpelling: true, SharedGates: true, Bind: bindStatisticsIO},
	{Names: []string{"ordered_set_transition_multi", "rank_final", "dense_rank_final", "percent_rank_final", "cume_dist_final"}, ExactSpelling: true, SharedGates: true, Bind: bindAggregateContextSupport},
	{Names: []string{"int4larger"}, ExactSpelling: true, SharedGates: true, Bind: bindInt4Larger},
	{Names: []string{"tsvector_concat", "tsquery_and", "tsquery_or", "tsquery_not", "tsquery_phrase", "tsq_mcontains", "tsq_mcontained", "ts_match_vq", "ts_match_qv", "ts_match_tt", "ts_match_tq"}, ExactSpelling: true, SharedGates: true, Bind: bindFTS},
	{Names: []string{"numnode", "querytree", "ts_rewrite"}, ExactSpelling: true, SharedGates: true, Bind: bindFTSQuery},
	{Names: []string{"setweight", "strip", "ts_delete", "ts_filter", "tsvector_to_array"}, ExactSpelling: true, SharedGates: true, Bind: bindFTSVector},
	{Names: []string{"ts_parse", "ts_token_type"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindFTSParser, NamedCall: namedFTSParser},
	{Names: []string{"ts_debug", "ts_stat"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindFTSInspection, NamedCall: namedFTSInspection},
	{Names: []string{"ts_lexize"}, ExactSpelling: true, SharedGates: true, Bind: bindDictionary},
	{Names: []string{"to_tsvector", "json_to_tsvector", "jsonb_to_tsvector", "to_tsquery", "plainto_tsquery", "phraseto_tsquery", "websearch_to_tsquery"}, ExactSpelling: true, SharedGates: true, Bind: bindFTSConversion},
	{Names: []string{"box", "bound_box", "circle", "line", "lseg", "path", "point", "polygon"}, ExactSpelling: true, SharedGates: true, Bind: bindGeometryConversion},
	{Names: []string{"area", "center", "diameter", "width", "height", "diagonal", "slope", "isclosed", "isopen", "npoints", "pclose", "popen"}, ExactSpelling: true, SharedGates: true, Bind: bindGeometryMeasure},
	{Names: []string{"line_eq"}, ExactSpelling: true, SharedGates: true, Bind: bindLineEquality},
	{Names: []string{"macaddr8_eq", "macaddr8_ne", "macaddr8_lt", "macaddr8_le", "macaddr8_gt", "macaddr8_ge"}, ExactSpelling: true, SharedGates: true, Bind: bindMACADDR8Comparison},
	{Names: []string{"text_pattern_lt", "text_pattern_le", "text_pattern_ge", "text_pattern_gt"}, ExactSpelling: true, SharedGates: true, Bind: bindTextPatternComparison},
	{Names: []string{"macaddr8_set7bit"}, ExactSpelling: true, SharedGates: true, Bind: bindMACSet7Bit},
	{Names: []string{"radius", "circle_eq", "circle_ne", "circle_lt", "circle_gt", "circle_le", "circle_ge", "circle_same", "circle_left", "circle_right", "circle_overleft", "circle_overright", "circle_below", "circle_above", "circle_overbelow", "circle_overabove", "circle_contain", "circle_contained", "circle_overlap", "circle_distance"}, ExactSpelling: true, SharedGates: true, Bind: bindCircle},
	{Names: []string{"point_eq", "point_ne", "point_left", "point_right", "point_below", "point_above", "point_horiz", "point_vert"}, ExactSpelling: true, SharedGates: true, Bind: bindPoint},
	{Names: []string{"poly_same", "poly_contain_pt", "pt_contained_poly"}, ExactSpelling: true, SharedGates: true, Bind: bindPolygon},
	{Names: []string{"network_sub", "network_subeq", "network_sup", "network_supeq", "network_overlap"}, ExactSpelling: true, SharedGates: true, Bind: bindNetworkSubnet},
	{Names: []string{"btint4cmp", "hashint4", "hashint4extended"}, ExactSpelling: true, SharedGates: true, Bind: bindIntegerIndexSupport},
	{Names: []string{"lo_create", "lo_creat", "lo_from_bytea", "lo_get", "lo_put", "lo_unlink"}, ExactSpelling: true, SharedGates: true, Bind: bindLargeObject},
	{Names: []string{"mxid_age", "pg_current_xact_id", "pg_current_xact_id_if_assigned", "pg_xact_status", "pg_get_multixact_members", "txid_current", "txid_current_if_assigned", "txid_status"}, Claims: parser.TransactionIdentityFunctionName, Bind: bindTransactionIdentity, NamedCall: namedTransactionIdentity},
	{Names: []string{"pg_current_snapshot", "pg_snapshot_xmin", "pg_snapshot_xmax", "pg_snapshot_xip", "pg_visible_in_snapshot", "txid_current_snapshot", "txid_snapshot_xmin", "txid_snapshot_xmax", "txid_snapshot_xip", "txid_visible_in_snapshot"}, Claims: parser.SnapshotFunctionName, Bind: bindSnapshot},
	{Names: []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin", "pg_last_committed_xact"}, Claims: parser.CommitTimestampFunctionName, Bind: bindCommitTimestamp, NamedCall: namedCommitTimestamp},
	{Names: []string{"pg_get_constraintdef", "pg_get_expr", "pg_get_indexdef", "pg_get_viewdef", "pg_get_serial_sequence", "pg_get_triggerdef", "pg_get_ruledef", "pg_get_functiondef", "pg_get_function_arguments", "pg_get_function_identity_arguments", "pg_get_function_result", "pg_get_partition_constraintdef", "pg_get_partkeydef"}, ExactSpelling: true, SharedGates: true, Bind: bindDeparse},
	{Names: []string{"col_description", "obj_description", "shobj_description"}, ExactSpelling: true, SharedGates: true, Bind: bindCommentInquiry},
	{Names: []string{"format_type", "pg_basetype", "pg_typeof"}, ExactSpelling: true, Bind: bindTypeInquiry, ProbeCall: probeTypeInquiry},
	{Names: []string{"pg_column_compression"}, ExactSpelling: true, Bind: bindColumnCompression},
	{Names: []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_ts_config_is_visible", "pg_ts_dict_is_visible", "pg_ts_parser_is_visible", "pg_ts_template_is_visible", "pg_statistics_obj_is_visible", "pg_collation_actual_version", "pg_database_collation_actual_version", "pg_import_system_collations", "pg_function_is_visible", "pg_operator_is_visible", "pg_opclass_is_visible", "pg_opfamily_is_visible"}, ExactSpelling: true, SharedGates: true, Bind: bindVisibilityInquiry},
	{Names: []string{"pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property"}, ExactSpelling: true, SharedGates: true, Bind: bindIndexProperty},
	{Names: []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"}, Claims: parser.ObjectAddressFunctionName, Bind: bindObjectAddress, NamedCall: namedObjectAddress},
	{Names: []string{"pg_get_userbyid", "pg_get_catalog_foreign_keys"}, Bind: bindCatalogRelation},
	{Names: []string{"pg_char_to_encoding", "pg_encoding_to_char", "pg_get_keywords"}, Bind: bindEncodingKeywords},
	{Names: []string{"pg_input_is_valid", "pg_input_error_info"}, Bind: bindInputValidity, NamedCall: namedInputValidity},
	{Names: []string{"pg_tablespace_location", "pg_tablespace_databases"}, Bind: bindTablespaceInfo},
	{Names: sizeNames[:], Bind: bindRelationSize},
	{Names: databaseFilesNames[:], ExactSpelling: true, Bind: bindDatabaseFiles},
	{Names: genericFileListNames[:], ExactSpelling: true, Bind: bindGenericFileList, NamedCall: namedGenericFileList},
	{Names: genericFileReadNames[:], ExactSpelling: true, Bind: bindGenericFileRead, NamedCall: namedGenericFileRead},
	{Names: logicalDecodingNames[:], ExactSpelling: true, Bind: bindLogicalDecoding, NamedCall: namedLogicalDecoding},
	{Names: replicationSlotNames[:], ExactSpelling: true, Bind: bindReplicationSlot, NamedCall: namedReplicationSlot},
	{Names: attributeStatisticsNames, ExactSpelling: true, Bind: bindAttributeStatistics, NamedCall: namedAttributeStatistics},
	{Names: relationStatisticsNames, ExactSpelling: true, Bind: bindRelationStatistics, NamedCall: namedRelationStatistics},
	{Names: []string{"pg_listening_channels", "pg_notification_queue_usage"}, Bind: bindNotificationInfo},
	{Names: recoveryInfoNames[:], ExactSpelling: true, Bind: bindRecoveryInfo},
	{Names: recoveryControlNames[:], ExactSpelling: true, Bind: bindRecoveryControl, NamedCall: namedRecoveryControl},
	{Names: walSummaryNames[:], ExactSpelling: true, Bind: bindWALSummary, NamedCall: namedWALSummary},
	{Names: controlDataNames[:], ExactSpelling: true, Bind: bindControlData},
	{Names: runtimeInfoNames[:], ExactSpelling: true, Bind: bindRuntimeInfo},
	{Names: []string{"pg_event_trigger_dropped_objects"}, ExactSpelling: true, Bind: bindEventTriggerDroppedObjects},
	{Names: []string{"pg_event_trigger_ddl_commands"}, ExactSpelling: true, Bind: bindEventTriggerDDLCommands},
	{Names: []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"}, Bind: bindPartitionInfo, NamedCall: namedPartitionInfo},
	{Names: []string{"pg_relation_is_publishable", "pg_get_publication_tables"}, ExactSpelling: true, Bind: bindPublicationInfo, NamedCall: namedPublicationInfo},
	{Names: []string{"uuid_extract_timestamp", "uuid_extract_version"}, Bind: bindUUIDExtract},
	{Names: jsonCallNames[:], Claims: jsonCallClaims, Bind: bindJSONCall, ProbeCall: probeJSONCall, ResultCall: jsonRecordCallResult, NamedCall: namedJSONCall},
	{Names: mathNames[:], ExactSpelling: true, SharedGates: true, Bind: bindMath, ProbeCall: probeThresholdBucket},
	{Names: []string{"uuidv4"}, ExactSpelling: true, SharedGates: true, Bind: bindUUIDGeneration},
	{Names: []string{"uuidv7"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindUUIDGeneration, NamedCall: namedUUIDGeneration},
	{Names: []string{"format"}, SharedGates: true, Bind: bindFormat, ProbeCall: probeFormat},
	{Names: []string{"uuid_generate_v4", "uuid_nil", "uuid_ns_dns", "uuid_ns_url", "uuid_ns_oid", "uuid_ns_x500", "uuid_generate_v1", "uuid_generate_v1mc", "uuid_generate_v3", "uuid_generate_v5"}, Bind: bindUUIDOssp},
	{Names: arrayInspectNames[:], Bind: bindArrayInspect, ProbeCall: probeArrayInput},
	{Names: arrayRandomNames[:], Bind: bindArrayRandom, ProbeCall: probeArrayInput, ResultCall: arrayInputCallResult},
	{Names: arrayTransformNames[:], Bind: bindArrayTransform, ProbeCall: probeArrayTransform, ResultCall: arrayTransformCallResult, NamedCall: namedArrayTransform},
	{Names: arraySearchNames[:], Bind: bindArraySearch, ProbeCall: probeArraySearch, ResultCall: arraySearchCallResult},
	{Names: rangeInspectNames[:], Bind: bindRangeInspect, ProbeCall: probeRangeInspect, ResultCall: rangeInspectCallResult},
	{Names: textObjectNames[:], ExactSpelling: true, SharedGates: true, Bind: bindTextObject},
	{Names: datetimeNames[:], ExactSpelling: true, SharedGates: true, Bind: bindDatetime},
	{Names: datetimeMakeNames[:], ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindDatetime, NamedCall: namedDatetimeMake},
	{Names: regexpNames[:], ExactSpelling: true, Bind: bindRegexp, NamedCall: namedRegexp},
	{Names: []string{"to_number"}, ExactSpelling: true, SharedGates: true, Bind: bindNumericFormatting},
	{Names: []string{"like_escape"}, ExactSpelling: true, SharedGates: true, Bind: bindLikeEscape},
	{Names: stringNames[:], ExactSpelling: true, SharedGates: true, Bind: bindStringName},
	{Names: []string{"string_to_table"}, ExactSpelling: true, SharedGates: true, Bind: bindStringToTable},
	{Names: stringVariadicNames[:], Claims: StringVariadicFunctionName, Bind: bindStringVariadic, ProbeCall: probeStringVariadic},
	{Names: []string{"parse_ident"}, Bind: bindParseIdent, NamedCall: namedStringIdent},
	{Names: []string{"lower", "upper"}, Bind: bindLowerUpper, ProbeCall: probeRangeInspect, ResultCall: rangeInspectCallResult},
	{Names: binaryNames[:], ExactSpelling: true, SharedGates: true, Bind: bindBinary},
	{Names: []string{"random", "random_normal", "setseed"}, ExactSpelling: true, SharedGates: true, Bind: bindRandom},
	{Names: []string{"pg_sleep", "pg_sleep_for", "pg_sleep_until"}, ExactSpelling: true, SharedGates: true, Bind: bindDatetimeSleep},
	{Names: []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"}, Bind: bindTransactionTime},
	{Names: []string{"int_array_aggregate", "int_array_enum"}, Bind: bindIntagg},
	{Names: []string{"pgrowlocks"}, Bind: bindPgrowlocks, NamedCall: namedPgrowlocks},
	{Names: pageinspectNames[:], Bind: bindPageinspect, NamedCall: namedPageinspect},
	{Names: []string{"normal_rand"}, Bind: bindTablefuncNormal},
	{Names: []string{"connectby"}, Bind: bindConnectby},
	{Names: []string{"crosstab", "crosstab2", "crosstab3", "crosstab4"}, Bind: bindTablefuncCrosstab},
	{Names: []string{"verify_heapam"}, Bind: bindVerifyHeapam, NamedCall: namedVerifyHeapam},
	{Names: []string{"bt_index_check", "bt_index_parent_check"}, Bind: bindAmcheck, NamedCall: namedAmcheck},
	{Names: pgcryptoNames[:], Bind: bindPgcrypto},
	{Names: cubeSpatialNames[:], Bind: bindCubeSpatial},
	{Names: fuzzystrmatchNames[:], Bind: bindFuzzystrmatch},
	{Names: cubeFunctionNames[:], Bind: bindCube},
	{Names: earthCoordinateNames[:], Bind: bindEarthCoordinate},
	{Names: []string{"geo_distance"}, Bind: bindEarthPoint},
	{Names: []string{"unaccent", "daitch_mokotoff"}, Bind: bindInstalledText},
	{Names: []string{"acldefault", "makeaclitem", "aclexplode"}, Bind: bindACLValue, NamedCall: namedACLValue},
	{Names: []string{"pg_get_acl"}, Bind: bindObjectACL, NamedCall: namedObjectACL},
	{Names: []string{"pg_has_role", "row_security_active", "has_function_privilege", "has_language_privilege", "has_foreign_data_wrapper_privilege", "has_server_privilege", "has_database_privilege", "has_schema_privilege", "has_type_privilege", "has_tablespace_privilege", "has_table_privilege", "has_sequence_privilege", "has_column_privilege", "has_any_column_privilege", "has_largeobject_privilege", "has_parameter_privilege"}, Bind: bindAccessInquiry},
	{Names: []string{"current_setting", "set_config", "pg_settings_get_flags"}, SharedGates: true, Bind: bindSetting},
	{Names: []string{"pg_options_to_table"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindOptionsToTable, NamedCall: namedOptionsToTable},
	{Names: []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user", "current_schema"}, Claims: parser.IdentityFunctionName, SharedGates: true, Bind: bindIdentityFunction},
	{Names: []string{"current_schemas"}, ExactSpelling: true, SharedGates: true, Bind: bindCurrentSchemas},
	{Names: []string{"pg_my_temp_schema", "pg_is_other_temp_schema"}, Claims: parser.NamespaceInquiryFunctionName, SharedGates: true, OwnsSpelling: true, Bind: bindNamespaceInquiry},
	{Names: []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared", "pg_advisory_unlock_all", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_xact_lock", "pg_advisory_xact_lock_shared", "pg_try_advisory_xact_lock", "pg_try_advisory_xact_lock_shared"}, SharedGates: true, OwnsLabels: true, Bind: bindAdvisory},
	{Names: []string{"pg_blocking_pids", "pg_safe_snapshot_blocking_pids"}, Bind: bindBlockingInquiry},
}

// Lookup returns the definition that claims an unqualified function name.
func Lookup(name string) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Claims != nil {
			if definition.Claims(name) {
				return definition, true
			}
			continue
		}
		for _, candidate := range definition.Names {
			if candidate == name || !definition.ExactSpelling && strings.EqualFold(candidate, name) {
				return definition, true
			}
		}
	}
	return Definition{}, false
}
