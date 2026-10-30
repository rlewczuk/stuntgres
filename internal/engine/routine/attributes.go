package routine

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// normalizeRoutineAttributes edits only the caller's unpublished candidate.
func normalizeRoutineAttributes(h DDLHost, auth ddl.Authority, d *catalog.Procedure, attributes []parser.RoutineAttribute, returnsSet bool) error {
	seen := make(map[string]bool)
	for _, attribute := range attributes {
		kind := attribute.Kind
		group := kind
		switch kind {
		case "immutable", "stable", "volatile":
			group = "volatility"
		}
		if d.RoutineKind == catalog.ProcedureRoutine && kind != "security" && kind != "set" && kind != "resetall" && kind != "transform" {
			return expr.ErrorAt("42P13", "invalid attribute in procedure definition", attribute.Pos)
		}
		if kind != "set" && kind != "resetall" {
			if seen[group] {
				return expr.ErrorAt("42601", "conflicting or redundant options", attribute.Pos)
			}
			seen[group] = true
		}
	}
	var entries []string
	configurationChanged := false
	for _, attribute := range attributes {
		kind := attribute.Kind
		switch kind {
		case "transform":
			// Admitted before formal/result types, not an ALTER attribute.
		case "strict":
			d.Strict = attribute.Value != "false"
		case "immutable":
			d.Volatility = expr.Immutable
		case "stable":
			d.Volatility = expr.Stable
		case "volatile":
			d.Volatility = expr.Volatile
		case "security":
			d.SecurityDefiner = attribute.Value == "definer"
		case "parallel":
			switch attribute.Value {
			case "safe":
				d.Parallel = 's'
			case "restricted":
				d.Parallel = 'r'
			case "unsafe":
				d.Parallel = 'u'
			default:
				return sqlError("42601", `parameter "parallel" must be SAFE, RESTRICTED, or UNSAFE`)
			}
		case "cost", "rows":
			value, err := strconv.ParseFloat(attribute.Value, 32)
			if err != nil || !(value > 0) {
				return sqlError("22023", strings.ToUpper(kind)+" must be positive")
			}
			if kind == "cost" {
				d.Cost = float32(value)
			} else {
				if !returnsSet {
					return sqlError("22023", "ROWS is not applicable when function does not return a set")
				}
				d.Rows = float32(value)
			}
		case "set", "resetall":
			if !configurationChanged {
				for i := range d.Configuration.Len() {
					entries = append(entries, d.Configuration.At(i))
				}
				configurationChanged = true
			}
			if kind == "resetall" {
				entries = nil
				continue
			}
			name, value, remove, err := h.ValidateRoutineSetting(auth, attribute.Setting)
			if err != nil {
				return err
			}
			index := -1
			for i, entry := range entries {
				if strings.HasPrefix(entry, name+"=") {
					index = i
					break
				}
			}
			if remove {
				if index >= 0 {
					entries = append(entries[:index], entries[index+1:]...)
				}
			} else if index >= 0 {
				entries[index] = name + "=" + value
			} else {
				entries = append(entries, name+"="+value)
			}
		default:
			return expr.ErrorAt("0A000", "SQL function attributes are unsupported", attribute.Pos)
		}
	}
	if configurationChanged {
		d.Configuration = catalog.NewRoutineConfiguration(entries)
	}
	return nil
}
