package fdw

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// FilePrivileges is invocation-owned authority, never retained in a declaration
// or a prepared expression. The statement owner captures inherited role access.
type FilePrivileges struct { ReadFiles, ExecutePrograms bool }

var fileOptionNames = [...]string{"filename", "program", "format", "header", "delimiter", "quote", "escape", "null", "default", "encoding", "on_error", "log_verbosity", "reject_limit", "force_not_null", "force_null"}

func fileOptionContext(name string) (catalog.ForeignObjectKind, bool) {
	switch name {
	case "filename", "program", "format", "header", "delimiter", "quote", "escape", "null", "encoding":
		return catalog.ForeignTable, true
	case "force_not_null", "force_null":
		return catalog.ForeignAttribute, true
	case "default", "on_error", "log_verbosity", "reject_limit":
		return catalog.ForeignTable, false // Released suggestion vocabulary only.
	}
	return 0, false
}

func fileOptionError(ctx context.Context, name string, kind catalog.ForeignObjectKind) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "HV00D", Message: fmt.Sprintf("invalid option %q", name)}
	if kind != catalog.ForeignTable && kind != catalog.ForeignAttribute {
		d.Hint = "There are no valid options in this context."
		return d
	}
	closest, minimum := "", int32(5)
	if len(name) > 0 && len(name) <= 255 {
		for _, candidate := range fileOptionNames {
			context, _ := fileOptionContext(candidate)
			if context != kind { continue }
			distance, err := functions.LevenshteinDistance(ctx, name, candidate, 1, 1, 1, 4)
			if err != nil { return err }
			if distance <= 4 && distance <= int32(len(name)/2) && distance < minimum {
				closest, minimum = candidate, distance
			}
		}
	}
	if closest != "" { d.Hint = fmt.Sprintf("Perhaps you meant the option %q.", closest) }
	return d
}

func ValidateFileOptions(ctx context.Context, kind catalog.ForeignObjectKind, options []catalog.ForeignOption, privileges FilePrivileges) error {
	var space [16]parser.CopyOption
	parsed := space[:]
	if len(options) > len(space) { parsed = make([]parser.CopyOption, len(options)) }
	parsed = parsed[:len(options)]
	for i, option := range options {
		parsed[i] = parser.CopyOption{Name: option.Name, Value: option.Value, HasValue: true, Pos: parser.Position{Offset: -1}}
	}
	return validateFileOptions(ctx, kind, parsed, privileges)
}

// ValidateFileOptionArray decodes untransformRelOptions semantics: first '=',
// and the distinction between an omitted argument and a supplied empty string.
func ValidateFileOptionArray(ctx context.Context, kind catalog.ForeignObjectKind, array sqltype.Value, privileges FilePrivileges) error {
	var space [16]parser.CopyOption
	options := space[:]
	if array.ArrayLen() > len(space) { options = make([]parser.CopyOption, array.ArrayLen()) }
	options = options[:array.ArrayLen()]
	for i := range options {
		value := array.ArrayElement(i)
		if value.IsNull() { return sqlError("22004", "null array element not allowed in this context") }
		text, err := value.Text()
		if err != nil { return err }
		name, argument, present := strings.Cut(text, "=")
		options[i] = parser.CopyOption{Name: name, Value: argument, HasValue: present, Pos: parser.Position{Offset: -1}}
	}
	return validateFileOptions(ctx, kind, options, privileges)
}

func validateFileOptions(ctx context.Context, kind catalog.ForeignObjectKind, options []parser.CopyOption, privileges FilePrivileges) error {
	var source, forceNull, forceNotNull bool
	var space [16]parser.CopyOption
	copyOptions := space[:0]
	for _, option := range options {
		if err := ctx.Err(); err != nil { return err }
		context, accepted := fileOptionContext(option.Name)
		if !accepted || context != kind { return fileOptionError(ctx, option.Name, kind) }
		switch option.Name {
		case "filename", "program":
			if source { return sqlError("42601", "conflicting or redundant options") }
			role, allowed := "pg_read_server_files", privileges.ReadFiles
			if option.Name == "program" { role, allowed = "pg_execute_server_program", privileges.ExecutePrograms }
			if !allowed {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: fmt.Sprintf("permission denied to set the %q option of a file_fdw foreign table", option.Name), Detail: fmt.Sprintf("Only roles with privileges of the %q role may set this option.", role)}
			}
			if !option.HasValue { return sqlError("42601", option.Name+" requires a parameter") }
			source = true
		case "force_null", "force_not_null":
			seen := &forceNull
			if option.Name == "force_not_null" { seen = &forceNotNull }
			if *seen { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Hint: fmt.Sprintf("Option %q supplied more than once for a column.", option.Name)} }
			*seen = true
			if option.HasValue {
				switch strings.ToLower(option.Value) {
				case "true", "false", "on", "off":
				default: return sqlError("42601", option.Name+" requires a Boolean value")
				}
			}
		default:
			copyOptions = append(copyOptions, option)
		}
	}
	if _, err := copyfmt.ParseOptions(&parser.Copy{From: true, Options: copyOptions}); err != nil { return err }
	if kind == catalog.ForeignTable && !source { return sqlError("HV002", "either filename or program is required for file_fdw foreign tables") }
	return ctx.Err()
}
