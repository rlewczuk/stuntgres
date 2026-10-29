package ddl

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CreateDatabaseOptions are the parsed CREATE DATABASE options. Capability
// names the first valid option the engine does not simulate.
type CreateDatabaseOptions struct {
	Owner            string
	Template         string
	Encoding         int
	Collate, CType   string
	Capability       string
	AllowConnections bool
	ConnectionLimit  int32
}

func databaseOptionError(option parser.DatabaseOption, code, message string) error {
	position := ""
	if option.Position > 0 {
		position = strconv.Itoa(option.Position)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
}

// ParseCreateDatabaseOptions validates the option list before any authority
// check: duplicates, unknown names and malformed values.
func ParseCreateDatabaseOptions(options []parser.DatabaseOption) (CreateDatabaseOptions, error) {
	result := CreateDatabaseOptions{Template: "template1", Encoding: 6, Collate: "C", CType: "C", AllowConnections: true, ConnectionLimit: -1}
	seen := make(map[string]parser.DatabaseOption, len(options))
	for _, option := range options {
		name := option.Name
		if _, exists := seen[name]; exists {
			return result, databaseOptionError(option, "42601", "conflicting or redundant options")
		}
		switch name {
		case "owner", "template", "encoding", "lc_collate", "lc_ctype", "locale", "locale_provider", "strategy", "tablespace", "is_template", "allow_connections", "connection_limit", "icu_locale", "icu_rules", "builtin_locale", "collation_version", "oid":
		default:
			return result, databaseOptionError(option, "42601", fmt.Sprintf(`option "%s" not recognized`, option.Name))
		}
		seen[name] = option
	}
	if option, ok := seen["encoding"]; ok && !option.Default {
		if option.Numeric {
			id, err := strconv.Atoi(option.Value)
			if err != nil || sqltype.EncodingName(id) == "" {
				return result, databaseOptionError(option, "42704", option.Value+" is not a valid encoding code")
			}
			result.Encoding = id
		} else {
			result.Encoding = sqltype.EncodingID(option.Value)
			if result.Encoding < 0 {
				return result, databaseOptionError(option, "42704", option.Value+" is not a valid encoding name")
			}
		}
	}
	if option, ok := seen["owner"]; ok && !option.Default {
		result.Owner = option.Value
	}
	if option, ok := seen["template"]; ok && !option.Default {
		result.Template = option.Value
	}
	if option, ok := seen["locale"]; ok && !option.Default {
		result.Collate, result.CType = option.Value, option.Value
	}
	if option, ok := seen["lc_collate"]; ok && !option.Default {
		result.Collate = option.Value
	}
	if option, ok := seen["lc_ctype"]; ok && !option.Default {
		result.CType = option.Value
	}
	if option, ok := seen["locale_provider"]; ok && !option.Default {
		if !strings.EqualFold(option.Value, "builtin") && !strings.EqualFold(option.Value, "icu") && !strings.EqualFold(option.Value, "libc") {
			return result, sqlError("42P17", "unrecognized locale provider: "+option.Value)
		}
	}
	// Boolean syntax and limit validation precede actor/owner authorization.
	for _, name := range []string{"is_template", "allow_connections"} {
		if option, ok := seen[name]; ok && !option.Default {
			switch strings.ToLower(option.Value) {
			case "true", "false", "on", "off", "yes", "no", "1", "0":
			default:
				return result, databaseOptionError(option, "22023", fmt.Sprintf("%s requires a Boolean value", name))
			}
			if name == "allow_connections" {
				value := strings.ToLower(option.Value)
				result.AllowConnections = value == "true" || value == "on" || value == "yes" || value == "1"
			}
		}
	}
	if option, ok := seen["connection_limit"]; ok && !option.Default {
		limit, err := strconv.ParseInt(option.Value, 10, 32)
		if err != nil {
			return result, databaseOptionError(option, "22P02", fmt.Sprintf(`invalid input syntax for type integer: "%s"`, option.Value))
		}
		if limit < -1 {
			return result, sqlError("22023", fmt.Sprintf("invalid connection limit: %d", limit))
		}
		result.ConnectionLimit = int32(limit)
	}
	for _, name := range []string{"locale_provider", "strategy", "tablespace", "is_template", "icu_locale", "icu_rules", "builtin_locale", "collation_version", "oid"} {
		if _, ok := seen[name]; ok {
			result.Capability = strings.ToUpper(name)
			break
		}
	}
	if option, ok := seen["owner"]; ok && !option.Default && option.Value == "" {
		return result, sqlError("42704", `role "" does not exist`)
	}
	return result, nil
}

// CreateDatabaseOwner resolves the new database's owner and checks that the
// current user may create a database owned by it.
func CreateDatabaseOwner(roles authorization.Read, current authorization.PrincipalRef, name string) (authorization.PrincipalRef, error) {
	owner := current
	if name != "" {
		descriptor, ok := roles.Lookup(name)
		if !ok {
			return owner, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
		}
		owner = descriptor.Ref
	}
	actor, err := roles.Descriptor(current)
	if err != nil {
		return owner, err
	}
	if !actor.Superuser && !actor.CreateDB {
		return owner, sqlError("42501", "permission denied to create database")
	}
	allowed, err := roles.CanSetRole(actor.Ref, owner)
	if err != nil {
		return owner, err
	}
	if !allowed {
		target, err := roles.Descriptor(owner)
		if err != nil {
			return owner, err
		}
		return owner, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, target.Name))
	}
	return owner, nil
}

// CheckCreateDatabase runs the admission checks in PostgreSQL's order. exists
// answers whether a database name is registered; it is called under Engine.mu.
func CheckCreateDatabase(name string, options CreateDatabaseOptions, exists func(string) bool) error {
	switch options.Template {
	case "template1":
	case "template0":
		return sqlError("0A000", "CREATE DATABASE with TEMPLATE other than template1 is not supported")
	default:
		if !exists(options.Template) {
			return sqlError("3D000", fmt.Sprintf(`template database "%s" does not exist`, options.Template))
		}
		return sqlError("0A000", "CREATE DATABASE with TEMPLATE other than template1 is not supported")
	}
	if options.Encoding != 6 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("new encoding (%s) is incompatible with the encoding of the template database (UTF8)", sqltype.EncodingName(options.Encoding)), Hint: "Use the same encoding as in the template database, or use template0 as template."}
	}
	for _, locale := range []struct{ value, kind string }{{options.Collate, "LC_COLLATE"}, {options.CType, "LC_CTYPE"}} {
		if locale.value != "C" && locale.value != "POSIX" {
			return sqlError("0A000", "CREATE DATABASE with "+locale.kind+" other than C is not supported")
		}
	}
	if options.Capability != "" {
		return sqlError("0A000", "CREATE DATABASE with "+options.Capability+" is not supported")
	}
	if exists(name) {
		return sqlError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
	}
	return nil
}
