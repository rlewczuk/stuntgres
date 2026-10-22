package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
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
	NamedCall NamedCallShape
}

// The static table is the registry: no init() registration. Names must not
// overlap any family that is still dispatched by the engine's name chain.
var definitions = [...]Definition{
	{Names: []string{"tsvector_concat", "tsquery_and", "tsquery_or", "tsquery_not", "tsquery_phrase", "tsq_mcontains", "tsq_mcontained"}, ExactSpelling: true, SharedGates: true, Bind: bindFTS},
	{Names: []string{"box", "bound_box", "circle", "line", "lseg", "path", "point"}, ExactSpelling: true, SharedGates: true, Bind: bindGeometryConversion},
	{Names: []string{"line_eq"}, ExactSpelling: true, SharedGates: true, Bind: bindLineEquality},
	{Names: []string{"macaddr8_eq", "macaddr8_ne", "macaddr8_lt", "macaddr8_le", "macaddr8_gt", "macaddr8_ge"}, ExactSpelling: true, SharedGates: true, Bind: bindMACADDR8Comparison},
	{Names: []string{"radius", "circle_eq", "circle_ne", "circle_lt", "circle_gt", "circle_le", "circle_ge", "circle_same", "circle_left", "circle_right", "circle_overleft", "circle_overright", "circle_below", "circle_above", "circle_overbelow", "circle_overabove", "circle_contain", "circle_contained", "circle_overlap", "circle_distance"}, ExactSpelling: true, SharedGates: true, Bind: bindCircle},
	{Names: []string{"point_eq", "point_ne", "point_left", "point_right", "point_below", "point_above", "point_horiz", "point_vert"}, ExactSpelling: true, SharedGates: true, Bind: bindPoint},
	{Names: []string{"poly_same", "poly_contain_pt", "pt_contained_poly"}, ExactSpelling: true, SharedGates: true, Bind: bindPolygon},
	{Names: []string{"network_sub", "network_subeq", "network_sup", "network_supeq", "network_overlap"}, ExactSpelling: true, SharedGates: true, Bind: bindNetworkSubnet},
	{Names: []string{"btint4cmp", "hashint4", "hashint4extended"}, ExactSpelling: true, SharedGates: true, Bind: bindIntegerIndexSupport},
	{Names: []string{"lo_create", "lo_creat", "lo_from_bytea", "lo_get", "lo_put", "lo_unlink"}, ExactSpelling: true, SharedGates: true, Bind: bindLargeObject},
	{Names: []string{"mxid_age", "pg_current_xact_id", "pg_current_xact_id_if_assigned", "pg_xact_status", "pg_get_multixact_members"}, Claims: parser.TransactionIdentityFunctionName, Bind: bindTransactionIdentity, NamedCall: namedTransactionIdentity},
	{Names: []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin", "pg_last_committed_xact"}, Claims: parser.CommitTimestampFunctionName, Bind: bindCommitTimestamp, NamedCall: namedCommitTimestamp},
	{Names: []string{"pg_get_constraintdef", "pg_get_expr", "pg_get_indexdef", "pg_get_viewdef", "pg_get_serial_sequence"}, ExactSpelling: true, SharedGates: true, Bind: bindDeparse},
	{Names: []string{"col_description", "obj_description", "shobj_description"}, ExactSpelling: true, SharedGates: true, Bind: bindCommentInquiry},
	{Names: []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "pg_collation_actual_version", "pg_database_collation_actual_version", "pg_import_system_collations"}, ExactSpelling: true, SharedGates: true, Bind: bindVisibilityInquiry},
	{Names: []string{"pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property"}, ExactSpelling: true, SharedGates: true, Bind: bindIndexProperty},
	{Names: []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"}, Claims: parser.ObjectAddressFunctionName, Bind: bindObjectAddress, NamedCall: namedObjectAddress},
	{Names: []string{"pg_get_userbyid", "pg_get_catalog_foreign_keys"}, Bind: bindCatalogRelation},
	{Names: []string{"pg_tablespace_location", "pg_tablespace_databases"}, Bind: bindTablespaceInfo},
	{Names: []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"}, Bind: bindPartitionInfo, NamedCall: namedPartitionInfo},
	{Names: []string{"uuid_extract_timestamp", "uuid_extract_version"}, Bind: bindUUIDExtract},
	{Names: jsonCallNames[:], Claims: jsonCallClaims, Bind: bindJSONCall, ProbeCall: probeJSONCall, NamedCall: namedJSONCall},
	{Names: mathNames[:], ExactSpelling: true, SharedGates: true, Bind: bindMath, ProbeCall: probeThresholdBucket},
	{Names: []string{"uuidv4"}, ExactSpelling: true, SharedGates: true, Bind: bindUUIDGeneration},
	{Names: []string{"uuidv7"}, ExactSpelling: true, SharedGates: true, OwnsLabels: true, Bind: bindUUIDGeneration, NamedCall: namedUUIDGeneration},
	{Names: []string{"format"}, SharedGates: true, Bind: bindFormat, ProbeCall: probeFormat},
	{Names: []string{"uuid_generate_v4", "uuid_nil", "uuid_ns_dns", "uuid_ns_url", "uuid_ns_oid", "uuid_ns_x500", "uuid_generate_v1", "uuid_generate_v1mc", "uuid_generate_v3", "uuid_generate_v5"}, Bind: bindUUIDOssp},
	{Names: arrayInspectNames[:], Bind: bindArrayInspect, ProbeCall: probeArrayInput},
	{Names: arrayRandomNames[:], Bind: bindArrayRandom, ProbeCall: probeArrayInput},
	{Names: arrayTransformNames[:], Bind: bindArrayTransform, ProbeCall: probeArrayTransform, NamedCall: namedArrayTransform},
	{Names: arraySearchNames[:], Bind: bindArraySearch, ProbeCall: probeArraySearch},
	{Names: rangeInspectNames[:], Bind: bindRangeInspect, ProbeCall: probeRangeInspect},
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
	{Names: []string{"lower", "upper"}, Bind: bindLowerUpper, ProbeCall: probeRangeInspect},
	{Names: binaryNames[:], ExactSpelling: true, SharedGates: true, Bind: bindBinary},
	{Names: []string{"random", "random_normal", "setseed"}, ExactSpelling: true, SharedGates: true, Bind: bindRandom},
	{Names: []string{"pg_sleep", "pg_sleep_for", "pg_sleep_until"}, ExactSpelling: true, SharedGates: true, Bind: bindDatetimeSleep},
	{Names: []string{"now", "transaction_timestamp", "statement_timestamp", "clock_timestamp", "timeofday"}, Bind: bindTransactionTime},
	{Names: []string{"int_array_aggregate", "int_array_enum"}, Bind: bindIntagg},
	{Names: []string{"pgrowlocks"}, Bind: bindPgrowlocks, NamedCall: namedPgrowlocks},
	{Names: pgcryptoNames[:], Bind: bindPgcrypto},
	{Names: fuzzystrmatchNames[:], Bind: bindFuzzystrmatch},
	{Names: []string{"unaccent", "daitch_mokotoff"}, Bind: bindInstalledText},
	{Names: []string{"acldefault", "makeaclitem", "aclexplode"}, Bind: bindACLValue, NamedCall: namedACLValue},
	{Names: []string{"pg_get_acl"}, Bind: bindObjectACL, NamedCall: namedObjectACL},
	{Names: []string{"pg_has_role", "row_security_active"}, Bind: bindAccessInquiry},
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
