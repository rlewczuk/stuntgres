package fdw

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PostgresPrivileges contains invocation facts, never cached declaration state.
// Installed and Notice are used synchronously after catalog locks are released.
type PostgresPrivileges struct {
	Superuser bool
	Installed func(string) bool
	Notice    func(sqlstate.Error)
}

var postgresTransportNames = [...]string{"service", "user", "password", "passfile", "channel_binding", "connect_timeout", "dbname", "host", "hostaddr", "port", "options", "application_name", "keepalives", "keepalives_idle", "keepalives_interval", "keepalives_count", "tcp_user_timeout", "sslmode", "sslnegotiation", "sslcompression", "sslcert", "sslkey", "sslpassword", "sslcertmode", "sslrootcert", "sslcrl", "sslcrldir", "sslsni", "requirepeer", "require_auth", "min_protocol_version", "max_protocol_version", "ssl_min_protocol_version", "ssl_max_protocol_version", "gssencmode", "krbsrvname", "gsslib", "gssdelegation", "target_session_attrs", "load_balance_hosts"}
var postgresOptionNames = [...]string{"schema_name", "table_name", "column_name", "use_remote_estimate", "fdw_startup_cost", "fdw_tuple_cost", "extensions", "updatable", "truncatable", "fetch_size", "batch_size", "async_capable", "analyze_sampling", "keep_connections", "parallel_commit", "parallel_abort", "password_required", "use_scram_passthrough"}

func postgresOptionAllowed(name string, kind catalog.ForeignObjectKind) bool {
	switch name {
	case "schema_name", "table_name":
		return kind == catalog.ForeignTable
	case "column_name":
		return kind == catalog.ForeignAttribute
	case "use_remote_estimate", "updatable", "truncatable", "fetch_size", "batch_size", "async_capable", "analyze_sampling":
		return kind == catalog.ForeignServer || kind == catalog.ForeignTable
	case "fdw_startup_cost", "fdw_tuple_cost", "extensions", "keep_connections", "parallel_commit", "parallel_abort":
		return kind == catalog.ForeignServer
	case "password_required", "user", "password", "sslpassword":
		return kind == catalog.ForeignUserMapping
	case "use_scram_passthrough", "sslcert", "sslkey", "gssdelegation":
		return kind == catalog.ForeignServer || kind == catalog.ForeignUserMapping
	default:
		return kind == catalog.ForeignServer && slices.Contains(postgresTransportNames[:], name)
	}
}

func postgresOptionError(ctx context.Context, name string, kind catalog.ForeignObjectKind) error {
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "HV00D", Message: fmt.Sprintf(`invalid option "%s"`, name)}
	if kind != catalog.ForeignServer && kind != catalog.ForeignUserMapping && kind != catalog.ForeignTable && kind != catalog.ForeignAttribute {
		d.Hint = "There are no valid options in this context."
		return d
	}
	closest, minimum := "", int32(5)
	if len(name) > 0 && len(name) <= 255 {
		for _, names := range [][]string{postgresTransportNames[:], postgresOptionNames[:]} {
			for _, candidate := range names {
				if !postgresOptionAllowed(candidate, kind) {
					continue
				}
				distance, err := functions.LevenshteinDistance(ctx, name, candidate, 1, 1, 1, 4)
				if err != nil {
					return err
				}
				if distance <= 4 && distance <= int32(len(name)/2) && distance < minimum {
					closest, minimum = candidate, distance
				}
			}
		}
	}
	if closest != "" {
		d.Hint = fmt.Sprintf("Perhaps you meant the option %q.", closest)
	}
	return d
}

func ValidatePostgresOptions(ctx context.Context, kind catalog.ForeignObjectKind, options []catalog.ForeignOption, privileges PostgresPrivileges) error {
	for _, option := range options {
		if err := validatePostgresOption(ctx, kind, option.Name, option.Value, true, privileges); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func ValidatePostgresOptionArray(ctx context.Context, kind catalog.ForeignObjectKind, array sqltype.Value, privileges PostgresPrivileges) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Array deconstruction rejects any NULL before processing the first option.
	for i := range array.ArrayLen() {
		if array.ArrayElement(i).IsNull() {
			return sqlError("22004", "null array element not allowed in this context")
		}
	}
	for i := range array.ArrayLen() {
		text, err := array.ArrayElement(i).Text()
		if err != nil {
			return err
		}
		name, value, present := strings.Cut(text, "=")
		if err := validatePostgresOption(ctx, kind, name, value, present, privileges); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func validatePostgresOption(ctx context.Context, kind catalog.ForeignObjectKind, name, value string, present bool, privileges PostgresPrivileges) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !postgresOptionAllowed(name, kind) {
		return postgresOptionError(ctx, name, kind)
	}
	switch name {
	case "use_remote_estimate", "updatable", "truncatable", "async_capable", "keep_connections", "parallel_commit", "parallel_abort", "password_required", "use_scram_passthrough":
		boolean := true
		if present {
			switch {
			case strings.EqualFold(value, "true"), strings.EqualFold(value, "on"):
			case strings.EqualFold(value, "false"), strings.EqualFold(value, "off"):
				boolean = false
			default:
				return sqlError("42601", name+" requires a Boolean value")
			}
		}
		if name == "password_required" && !boolean && !privileges.Superuser {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "password_required=false is superuser-only", Hint: "User mappings with the password_required option set to false may only be created or modified by the superuser."}
		}
	case "fetch_size", "batch_size", "fdw_startup_cost", "fdw_tuple_cost", "extensions", "analyze_sampling":
		if !present {
			return sqlError("42601", name+" requires a parameter")
		}
		switch name {
		case "fetch_size", "batch_size":
			_, err := PostgresIntegerOption(name, value)
			return err
		case "fdw_startup_cost", "fdw_tuple_cost":
			n, suffix, valid := settings.ParseNumber(value, false)
			if !valid || suffix != "" {
				return sqlError("22023", fmt.Sprintf("invalid value for floating point option %q: %s", name, value))
			}
			if n < 0 {
				return sqlError("22023", fmt.Sprintf("%q must be a floating point value greater than or equal to zero", name))
			}
		case "analyze_sampling":
			switch value {
			case "off", "auto", "random", "system", "bernoulli":
			default:
				return sqlError("22023", "invalid value for string option \"analyze_sampling\": "+value)
			}
		case "extensions":
			list, err := settings.ParseIdentifierList("extensions", value)
			if err != nil {
				return sqlError("22023", `parameter "extensions" must be a list of extension names`)
			}
			for _, extension := range list {
				if privileges.Installed != nil && privileges.Installed(extension) {
					continue
				}
				if privileges.Notice != nil {
					privileges.Notice(sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: fmt.Sprintf(`extension "%s" is not installed`, extension)})
				}
			}
		}
	case "sslcert", "sslkey":
		if kind == catalog.ForeignUserMapping && !privileges.Superuser {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "sslcert and sslkey are superuser-only", Hint: "User mappings with the sslcert or sslkey options set may only be created or modified by the superuser."}
		}
	}
	return nil
}

func PostgresIntegerOption(name, value string) (int32, error) {
	n, suffix, valid := settings.ParseNumber(value, true)
	n = math.RoundToEven(n)
	if !valid || suffix != "" || math.IsNaN(n) || n < math.MinInt32 || n > math.MaxInt32 {
		return 0, sqlError("22023", fmt.Sprintf("invalid value for integer option %q: %s", name, value))
	}
	if n <= 0 {
		return 0, sqlError("22023", fmt.Sprintf("%q must be an integer value greater than zero", name))
	}
	return int32(n), nil
}

// PostgresTransportOptions filters only the stock consumer, not generic GO
// requests. Forced startup options are appended after ordered server/mapping data.
func PostgresTransportOptions(options []catalog.ForeignOption) []catalog.ForeignOption {
	var out []catalog.ForeignOption
	for i, option := range options {
		if !slices.Contains(postgresTransportNames[:], option.Name) {
			continue
		}
		if out == nil {
			out = make([]catalog.ForeignOption, 0, len(options)-i)
		}
		out = append(out, option)
	}
	return out
}
