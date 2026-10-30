package dump

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// RestoreFunctionRecipe recognizes only this occurrence's incumbent static
// recipe shape. Child admission is owned by RestoreScalar's recursive visitor.
// A false result can still admit a genuine qualified type-owned constructor.
func RestoreFunctionRecipe(r parser.FunctionExpr) bool {
	if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
		return false
	}
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	switch name {
	case "circle":
		return functions.CircleConstructorReplay(r)
	case "split_part", "string_to_array", "parse_ident":
	case "cube", "cube_dim", "cube_is_point":
	case "acldefault", "makeaclitem":
	case "array_position", "array_positions", "array_remove", "array_replace":
	case "array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality":
	case "array_append", "array_prepend", "array_cat", "array_fill", "array_reverse", "array_sort", "trim_array", "array_to_string":
	case "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge":
	case "tsvector_concat", "tsquery_and", "tsquery_or", "tsquery_not", "tsquery_phrase", "tsq_mcontains", "tsq_mcontained":
	case "json_object", "jsonb_object", "jsonb_set", "jsonb_set_lax", "jsonb_insert", "json_strip_nulls", "jsonb_path_query_array", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first":
	case "jsonb_path_query_array_tz", "jsonb_path_exists_tz", "jsonb_path_match_tz", "jsonb_path_query_first_tz":
	case "json_typeof", "jsonb_typeof", "jsonb_pretty", "json_array_length", "jsonb_array_length":
	case "to_json", "to_jsonb", "array_to_json", "row_to_json", "json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object":
	case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "position", "strpos", "overlay", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned":
	case "to_bin", "to_oct", "to_hex":
	case "pg_size_bytes", "pg_size_pretty":
	case "age", "isfinite", "overlaps", "date_trunc", "date_bin", "timezone", "date_add", "date_subtract":
	case "justify_days", "justify_hours", "justify_interval":
	case "make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval":
	case "gen_random_uuid", "uuidv4", "uuidv7", "uuid_extract_timestamp", "uuid_extract_version":
	case "abbrev", "host", "family", "masklen", "inet_same_family", "broadcast", "hostmask", "netmask", "network", "inet_merge", "set_masklen":
	case "format", "concat", "concat_ws", "quote_ident", "quote_literal", "quote_nullable", "pg_client_encoding":
	case "convert", "convert_from", "convert_to", "encode", "decode", "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
	case "regexp_count", "regexp_instr", "regexp_substr", "regexp_like", "regexp_match", "regexp_replace", "regexp_split_to_array":
		_, _, _, valid := functions.RegexpSignature(name, len(r.Args), true)
		return valid && !r.Variadic
	case "like_escape":
		return len(r.Name.Parts) == 2 && r.Name.Parts[0].Text == "pg_catalog" && len(r.Args) == 2 && !r.Variadic
	case "similar_to_escape":
		return r.Syntax == parser.SimilarEscapeFunctionSyntax && len(r.Name.Parts) == 2 && r.Name.Parts[0].Text == "pg_catalog" && len(r.Args) >= 1 && len(r.Args) <= 2 && !r.Variadic
	case "populate_record":
		return len(r.Args) == 2 && len(r.ArgNames) == 0 && !r.Variadic
	case "left", "right", "xmlexists":
		return len(r.Args) == 2 && !r.Variadic
	case "substring", "substr":
		return (len(r.Args) == 2 || len(r.Args) == 3) && !r.Variadic
	default:
		return false
	}
	return true
}
