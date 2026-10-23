package table

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type relationOptionDefinition struct {
	name     string
	kind     catalog.RelationOptionKind
	min, max float64
	toast    bool
}

var relationOptionDefinitions = [...]relationOptionDefinition{
	{"fillfactor", catalog.RelationOptionInteger, 10, 100, false},
	{"toast_tuple_target", catalog.RelationOptionInteger, 128, 8160, false},
	{"parallel_workers", catalog.RelationOptionInteger, 0, 1024, false},
	{"autovacuum_enabled", catalog.RelationOptionBoolean, 0, 0, true},
	{"vacuum_index_cleanup", catalog.RelationOptionEnum, 0, 0, true},
	{"vacuum_truncate", catalog.RelationOptionBoolean, 0, 0, true},
	{"autovacuum_vacuum_threshold", catalog.RelationOptionInteger, 0, math.MaxInt32, true},
	{"autovacuum_vacuum_max_threshold", catalog.RelationOptionInteger, -1, math.MaxInt32, true},
	{"autovacuum_vacuum_insert_threshold", catalog.RelationOptionInteger, -1, math.MaxInt32, true},
	{"autovacuum_vacuum_scale_factor", catalog.RelationOptionReal, 0, 100, true},
	{"autovacuum_vacuum_insert_scale_factor", catalog.RelationOptionReal, 0, 100, true},
	{"autovacuum_analyze_threshold", catalog.RelationOptionInteger, 0, math.MaxInt32, false},
	{"autovacuum_analyze_scale_factor", catalog.RelationOptionReal, 0, 100, false},
	{"autovacuum_vacuum_cost_delay", catalog.RelationOptionReal, 0, 100, true},
	{"autovacuum_vacuum_cost_limit", catalog.RelationOptionInteger, 1, 10000, true},
	{"autovacuum_freeze_min_age", catalog.RelationOptionInteger, 0, 1000000000, true},
	{"autovacuum_freeze_max_age", catalog.RelationOptionInteger, 100000, 2000000000, true},
	{"autovacuum_freeze_table_age", catalog.RelationOptionInteger, 0, 2000000000, true},
	{"autovacuum_multixact_freeze_min_age", catalog.RelationOptionInteger, 0, 1000000000, true},
	{"autovacuum_multixact_freeze_max_age", catalog.RelationOptionInteger, 10000, 2000000000, true},
	{"autovacuum_multixact_freeze_table_age", catalog.RelationOptionInteger, 0, 2000000000, true},
	{"log_autovacuum_min_duration", catalog.RelationOptionInteger, -1, math.MaxInt32, true},
	{"vacuum_max_eager_freeze_failure_rate", catalog.RelationOptionReal, 0, 1, true},
	{"user_catalog_table", catalog.RelationOptionBoolean, 0, 0, false},
}

// HasRelationOptions keeps mixed-list admission distinct from option-only work.
func HasRelationOptions(actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		if RelationOptionsAction(action.Action) {
			return true
		}
	}
	return false
}
func RelationOptionsAction(kind parser.AlterTableActionKind) bool {
	return kind == parser.SetRelationOptions || kind == parser.ResetRelationOptions
}

func RelationOptionsOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, a := range actions {
		if !RelationOptionsAction(a.Action) {
			return false
		}
	}
	return true
}

func relationOptionText(v parser.Value) string {
	if v.Kind == parser.IntegerValue && v.Integer >= math.MinInt32 && v.Integer <= math.MaxInt32 {
		return strconv.FormatInt(v.Integer, 10)
	}
	return v.Text
}

func validateRelationOption(o parser.IndexOption) (catalog.RelationOption, error) {
	name, text := o.Name.Text, relationOptionText(o.Value)
	var definition *relationOptionDefinition
	for i := range relationOptionDefinitions {
		d := &relationOptionDefinitions[i]
		if d.name == name && (o.Namespace == "" || d.toast) {
			definition = d
			break
		}
	}
	if definition == nil {
		return catalog.RelationOption{}, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
	}
	d := definition
	entry := catalog.RelationOption{Name: name, Text: text, Kind: d.kind}
	invalid := func(kind string) (catalog.RelationOption, error) {
		return entry, sqlError("22023", fmt.Sprintf(`invalid value for %s option "%s": %s`, kind, name, text))
	}
	switch d.kind {
	case catalog.RelationOptionBoolean:
		value, err := (settings.Definition{Name: name, Type: settings.Boolean}).Parse(text)
		if err != nil {
			return invalid("boolean")
		}
		entry.Boolean = value.Boolean
	case catalog.RelationOptionEnum:
		switch strings.ToLower(text) {
		case "auto":
			entry.Enum = "auto"
		case "on", "true", "yes", "1":
			entry.Enum = "on"
		case "off", "false", "no", "0":
			entry.Enum = "off"
		default:
			return entry, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for enum option "%s": %s`, name, text), Detail: `Valid values are "on", "off", and "auto".`}
		}
	default:
		integer := d.kind == catalog.RelationOptionInteger
		n, suffix, ok := settings.ParseNumber(text, integer)
		kind := "floating point"
		if integer {
			kind = "integer"
			n = math.RoundToEven(n)
		}
		if !ok || suffix != "" || math.IsNaN(n) || integer && (n < math.MinInt32 || n > math.MaxInt32) {
			return invalid(kind)
		}
		if n < d.min || n > d.max {
			var detail string
			if integer {
				detail = fmt.Sprintf(`Valid values are between "%.0f" and "%.0f".`, d.min, d.max)
			} else {
				detail = fmt.Sprintf(`Valid values are between "%f" and "%f".`, d.min, d.max)
			}
			return entry, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`value %s out of bounds for option "%s"`, text, name), Detail: detail}
		}
		if integer {
			entry.Integer = int64(n)
		} else {
			entry.Real = n
		}
	}
	return entry, nil
}

// RelationOptionsCandidate transforms each namespace independently, base first,
// matching PostgreSQL's catalog pass ordering without inventing a TOAST catalog.
func RelationOptionsCandidate(old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	reset := action.Action == parser.ResetRelationOptions
	if reset {
		for _, o := range action.Options {
			if o.HasValue {
				return nil, sqlError("42601", "RESET must not include values for parameters")
			}
		}
	}
	transform := func(previous []catalog.RelationOption, namespace string) ([]catalog.RelationOption, error) {
		if !reset {
			// Finish this namespace's structural transformation before checking
			// values. Other namespaces' names belong to their own later pass.
			for _, o := range action.Options {
				if o.Namespace != "" && o.Namespace != "toast" {
					return nil, sqlError("22023", fmt.Sprintf(`unrecognized parameter namespace "%s"`, o.Namespace))
				}
				if o.Namespace == namespace && strings.Contains(o.Name.Text, "=") {
					return nil, sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, o.Name.Text))
				}
			}
		}
		var additions []catalog.RelationOption
		for i, o := range action.Options {
			if o.Namespace != namespace {
				continue
			}
			if reset {
				continue
			}
			// Validate before detecting a later duplicate, preserving the first error.
			for _, prior := range action.Options[:i] {
				if prior.Namespace == namespace && prior.Name.Text == o.Name.Text {
					return nil, sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, o.Name.Text))
				}
			}
			entry, err := validateRelationOption(o)
			if err != nil {
				return nil, err
			}
			additions = append(additions, entry)
		}
		touched := len(additions) != 0
		for _, entry := range previous {
			for _, o := range action.Options {
				if o.Namespace == namespace && o.Name.Text == entry.Name {
					touched = true
					break
				}
			}
		}
		if !touched {
			return previous, nil
		}
		next := make([]catalog.RelationOption, 0, len(previous)+len(additions))
		for _, entry := range previous {
			remove := false
			for _, o := range action.Options {
				if o.Namespace == namespace && o.Name.Text == entry.Name {
					remove = true
					break
				}
			}
			if !remove {
				next = append(next, entry)
			}
		}
		next = append(next, additions...)
		if len(next) == 0 {
			return nil, nil
		}
		return next, nil
	}
	base, err := transform(old.StorageOptions.Base, "")
	if err != nil {
		return nil, err
	}
	if old.Partition != nil && len(base) > 0 {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot specify storage parameters for a partitioned table", Hint: "Specify storage parameters for its leaf partitions instead."}
	}
	toast, err := transform(old.StorageOptions.Toast, "toast")
	if err != nil {
		return nil, err
	}
	if reset && slices.Equal(base, old.StorageOptions.Base) && slices.Equal(toast, old.StorageOptions.Toast) {
		return old, nil
	}
	next := *old
	next.StorageOptions = catalog.RelationOptions{Base: base, Toast: toast}
	return &next, nil
}
