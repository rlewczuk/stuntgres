package table

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ColumnStatisticsAction(kind parser.AlterTableActionKind) bool {
	return kind == parser.SetColumnStatistics || kind == parser.SetColumnOptions || kind == parser.ResetColumnOptions
}
func ColumnStatisticsOnly(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, a := range actions {
		if !ColumnStatisticsAction(a.Action) {
			return false
		}
	}
	return true
}

// ColumnStatisticsCandidate owns no publication; options are detached before edits.
func ColumnStatisticsCandidate(old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, error) {
	name := action.Column.Text
	if catalog.IsSystemColumn(name) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot alter system column "%s"`, name))
	}
	slot, ok := old.Index[name]
	if !ok {
		return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name, old.Name()))
	}
	value := old.ColumnMeta.Statistics[old.Attributes[slot]]
	if action.Action == parser.SetColumnStatistics {
		if generation, exists := old.ColumnMeta.Generations[old.Attributes[slot]]; exists && generation.Kind == catalog.GenerationVirtual {
			return nil, sqlError("0A000", fmt.Sprintf(`cannot alter statistics on virtual generated column "%s"`, name))
		}
		target := action.StatisticsTarget
		if target < -1 {
			return nil, sqlError("22023", fmt.Sprintf("statistics target %d is too low", target))
		}
		if target > 10000 {
			target = 10000
		}
		value.TargetSet = target != -1
		value.Target = 0
		if value.TargetSet {
			value.Target = int16(target)
		}
	} else {
		// Complete source-ordered transformation checks across the whole list
		// before validating names, duplicates, or numeric option values.
		for _, option := range action.Options {
			if action.Action == parser.ResetColumnOptions && option.HasValue {
				return nil, sqlError("42601", "RESET must not include values for parameters")
			}
			if action.Action == parser.SetColumnOptions && option.Namespace != "" {
				return nil, sqlError("22023", fmt.Sprintf(`unrecognized parameter namespace "%s"`, option.Namespace))
			}
			if action.Action == parser.SetColumnOptions && strings.Contains(option.Name.Text, "=") {
				return nil, sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, option.Name.Text))
			}
		}
		value.Options = slices.Clone(value.Options)
		// transformRelOptions retains untouched old options first, then new options
		// in source order. Duplicate SET names are diagnosed after transformation.
		value.Options = slices.DeleteFunc(value.Options, func(old catalog.ForeignOption) bool {
			return slices.ContainsFunc(action.Options, func(o parser.IndexOption) bool { return o.Namespace == "" && o.Name.Text == old.Name })
		})
		if action.Action == parser.SetColumnOptions {
			seen := make(map[string]bool, len(action.Options))
			for _, option := range action.Options {
				name := option.Name.Text
				if name != "n_distinct" && name != "n_distinct_inherited" {
					return nil, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
				}
				if seen[name] {
					return nil, sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
				}
				seen[name] = true
				text := relationOptionText(option.Value)
				n, valid := catalog.ParseColumnDistinct(text)
				if !valid {
					return nil, sqlError("22023", fmt.Sprintf(`invalid value for floating point option "%s": %s`, name, text))
				}
				if n < -1 || n > math.MaxFloat64 {
					diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`value %s out of bounds for option "%s"`, text, name)}
					diagnostic.Detail = `Valid values are between "-1.000000" and "` + strconv.FormatFloat(math.MaxFloat64, 'f', 6, 64) + `".`
					return nil, diagnostic
				}
				value.Options = append(value.Options, catalog.ForeignOption{Name: name, Value: text})
			}
		}
	}
	return old.WithColumnStatistics(slot, value), nil
}
