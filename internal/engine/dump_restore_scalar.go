package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// Stored CREATE expressions retain their binder's admission rules. INSERT
// expressions retain the existing Restore whitelist. Both modes reject every
// parameter reached through the shared recursive visitor.
func restoreScalar(raw parser.Expr, stored bool) error {
	switch r := raw.(type) {
	case parser.SQLJSONExpr:
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		if r.Query != nil {
			return restoreSQLJSONQuery(r.Query, stored)
		}
		return nil
	case parser.JSONPredicateExpr:
		return restoreScalar(r.Child, stored)
	case parser.SubqueryExpr:
		if r.Left != nil {
			if err := restoreScalar(r.Left, stored); err != nil {
				return err
			}
		}
		if err := restoreSQLJSONQuery(r.Query, stored); err != nil {
			return err
		}
		if !stored {
			return fmt.Errorf("stuntgres: invalid restore scalar")
		}
		return nil
	case parser.FunctionExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			switch r.Name.Parts[len(r.Name.Parts)-1].Text {
			case "split_part", "string_to_array", "parse_ident":
			case "acldefault", "makeaclitem":
				// These typed scalar constructors are also emitted by the
				// ordinary lossless datum renderer. Their binder owns the
				// signature and value checks; recurse below to reject parameters.
			case "array_position", "array_positions", "array_remove", "array_replace":
			case "array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality":
			case "array_append", "array_prepend", "array_cat", "array_fill", "array_reverse", "array_sort", "trim_array", "array_to_string":
			case "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge":
			case "json_object", "jsonb_object", "jsonb_set", "jsonb_set_lax", "jsonb_insert", "json_strip_nulls", "jsonb_path_query_array", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first":
			case "json_typeof", "jsonb_typeof", "jsonb_pretty", "json_array_length", "jsonb_array_length":
			case "to_json", "to_jsonb", "array_to_json", "row_to_json", "json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object":
			case "btrim", "ltrim", "rtrim", "normalize", "is_normalized", "lower", "upper", "casefold", "initcap", "bit_length", "char_length", "character_length", "length", "octet_length", "position", "strpos", "overlay", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "ascii", "chr", "to_ascii", "unistr", "unicode_assigned":
			case "to_bin", "to_oct", "to_hex":
			case "pg_size_bytes", "pg_size_pretty":
			case "age", "isfinite":
			case "overlaps":
			case "date_trunc", "date_bin":
			case "timezone", "date_add", "date_subtract":
			case "justify_days", "justify_hours", "justify_interval":
			case "make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval":
			case "gen_random_uuid", "uuidv4", "uuidv7":
			case "uuid_extract_timestamp", "uuid_extract_version":
			case "abbrev", "host", "family", "masklen", "inet_same_family", "broadcast", "hostmask", "netmask", "network", "inet_merge", "set_masklen":
			case "format", "concat", "concat_ws", "quote_ident", "quote_literal", "quote_nullable", "pg_client_encoding":
			case "convert", "convert_from", "convert_to", "encode", "decode", "crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512", "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
			case "regexp_count", "regexp_instr", "regexp_substr", "regexp_like", "regexp_match", "regexp_replace", "regexp_split_to_array":
				_, _, _, valid := functions.RegexpSignature(r.Name.Parts[len(r.Name.Parts)-1].Text, len(r.Args), true)
				if !valid || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "like_escape":
				if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) != 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "similar_to_escape":
				if r.Syntax != parser.SimilarEscapeFunctionSyntax || len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || len(r.Args) < 1 || len(r.Args) > 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "left", "right":
				if len(r.Args) != 2 || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			case "substring", "substr":
				if (len(r.Args) != 2 && len(r.Args) != 3) || r.Variadic {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			default:
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
		}
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.CaseExpr:
		if r.Test != nil {
			if err := restoreScalar(r.Test, stored); err != nil {
				return err
			}
		}
		for _, arm := range r.Arms {
			if err := restoreScalar(arm.Condition, stored); err != nil {
				return err
			}
			if err := restoreScalar(arm.Result, stored); err != nil {
				return err
			}
		}
		if r.Else != nil {
			return restoreScalar(r.Else, stored)
		}
		return nil
	case parser.ConditionalExpr:
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.LogicalExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		if r.Right != nil {
			return restoreScalar(r.Right, stored)
		}
		return nil
	case parser.PredicateExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		if r.Right != nil {
			return restoreScalar(r.Right, stored)
		}
		return nil
	case parser.CollateExpr:
		return restoreScalar(r.Child, stored)
	case parser.CastExpr:
		return restoreScalar(r.Child, stored)
	case parser.RegclassExpr:
		return restoreScalar(r.Child, stored)
	case parser.FieldExpr:
		return restoreScalar(r.Base, stored)
	case parser.RowExpr:
		for _, field := range r.Fields {
			if err := restoreScalar(field, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.InListExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		for _, item := range r.Items {
			if err := restoreScalar(item, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.ArrayComparisonExpr:
		if err := restoreScalar(r.Left, stored); err != nil {
			return err
		}
		return restoreScalar(r.Right, stored)
	case parser.ArrayExpr:
		for _, element := range r.Elements {
			if err := restoreScalar(element, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range r.Children(&storage) {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	case parser.TimeZoneExpr:
		if err := restoreScalar(r.Value, stored); err != nil {
			return err
		}
		if !r.Local {
			return restoreScalar(r.Zone, stored)
		}
		return nil
	case parser.ParameterExpr:
		return expr.ErrorAt("42P02", "parameters are not allowed in restore", r.Pos)
	case parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.DefaultExpr:
		return nil
	case parser.OperatorExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			spelling := r.Name.Parts[len(r.Name.Parts)-1].Text
			if r.SQLSimilar {
				if (spelling != "~" && spelling != "!~") || len(r.Args) != 2 {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
				normalizer, ok := r.Args[1].(parser.FunctionExpr)
				if !ok || normalizer.Syntax != parser.SimilarEscapeFunctionSyntax {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			} else if functions.PosixName(spelling) && len(r.Args) != 2 && (spelling != "~" || len(r.Args) != 1) {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
		}
		for _, child := range r.Args {
			if err := restoreScalar(child, stored); err != nil {
				return err
			}
		}
		return nil
	default:
		if stored {
			return nil
		} // Existing stored binder owns unsupported syntax.
		return fmt.Errorf("stuntgres: invalid restore scalar")
	}
}

func restoreSQLJSONQuery(query *parser.Select, stored bool) error {
	return parser.VisitQueryExpressions(query, func(raw parser.Expr) error {
		if parameter, ok := raw.(parser.ParameterExpr); ok {
			return expr.ErrorAt("42P02", "parameters are not allowed in restore", parameter.Pos)
		}
		return nil
	})
}
