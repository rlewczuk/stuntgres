package ddl

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// AlterDatabaseOptions distinguishes omission from explicit DEFAULT.
type AlterDatabaseOptions struct {
	HasTemplate, HasAllow, HasLimit, HasTablespace bool
	Template, Allow                                bool
	Limit                                          int32
	Tablespace                                     string
}

// ParseAlterDatabaseOptions is effect-free. Its written-order name scan and
// mixed-TABLESPACE check precede the executor's transaction and target checks.
func ParseAlterDatabaseOptions(options []parser.DatabaseOption) (AlterDatabaseOptions, error) {
	result := AlterDatabaseOptions{Allow: true, Limit: -1}
	var template, allow, limit, tablespace *parser.DatabaseOption
	for i := range options {
		option := &options[i]
		var slot **parser.DatabaseOption
		switch option.Name {
		case "is_template":
			slot = &template
		case "allow_connections":
			slot = &allow
		case "connection_limit":
			slot = &limit
		case "tablespace":
			slot = &tablespace
		default:
			return result, databaseOptionError(*option, "42601", fmt.Sprintf(`option "%s" not recognized`, option.Name))
		}
		if *slot != nil {
			return result, databaseOptionError(*option, "42601", "conflicting or redundant options")
		}
		*slot = option
	}
	if tablespace != nil {
		if len(options) != 1 {
			return result, databaseOptionError(*tablespace, "0A000", `option "tablespace" cannot be specified with other options`)
		}
		result.HasTablespace, result.Tablespace = true, tablespace.Value
		return result, nil
	}
	for _, item := range []struct {
		option  *parser.DatabaseOption
		value   *bool
		present *bool
	}{{template, &result.Template, &result.HasTemplate}, {allow, &result.Allow, &result.HasAllow}} {
		if item.option == nil {
			continue
		}
		*item.present = true
		if item.option.Default {
			continue
		}
		if item.option.Numeric {
			integer, err := sqltype.ParseInt8(item.option.Value)
			if err != nil || integer != 0 && integer != 1 {
				return result, sqlError("42601", item.option.Name+" requires a Boolean value")
			}
			*item.value = integer == 1
			continue
		}
		text := strings.ToLower(item.option.Value)
		switch {
		case text == "true" || text == "on":
			*item.value = true
		case text == "false" || text == "off":
			*item.value = false
		default:
			return result, sqlError("42601", item.option.Name+" requires a Boolean value")
		}
	}
	if limit != nil {
		result.HasLimit = true
		if !limit.Default {
			value, err := sqltype.ParseInt8(limit.Value)
			if !limit.Numeric || err != nil || value != int64(int32(value)) {
				return result, sqlError("42601", "connection_limit requires an integer value")
			}
			if value < -1 {
				return result, sqlError("22023", fmt.Sprintf("invalid connection limit: %d", value))
			}
			result.Limit = int32(value)
		}
	}
	return result, nil
}
