package index

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func Capability(message string, pos parser.Position) error {
	d := sqlError("0A000", message).(*sqlstate.Error)
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}

// ValidateIndexOptions preserves transform-before-AM validation and RESET's
// permissive unknown/repeated-name contract.
func ValidateIndexOptions(s *parser.AlterIndex, method catalog.IndexMethod) error {
	for _, o := range s.Options {
		if s.Action == "reset" && o.HasValue {
			return sqlError("42601", "RESET must not include values for parameters")
		}
		if s.Action == "reset" || o.Namespace == "toast" {
			continue
		}
		if strings.Contains(o.Name.Text, "=") {
			return sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, o.Name.Text))
		}
		if o.Namespace != "" && o.Namespace != "toast" {
			return sqlError("22023", fmt.Sprintf(`unrecognized parameter namespace "%s"`, o.Namespace))
		}
	}
	if s.Action == "reset" {
		return nil
	}
	return ValidateIndexReloptions(s.Options, method, true)
}

// TransformIndexOptions returns owned immutable slices; untouched keys retain
// their order and SET appends the supplied lexical values in request order.
func TransformIndexOptions(old catalog.RelationOptions, s *parser.AlterIndex, method catalog.IndexMethod) (catalog.RelationOptions, error) {
	if err := ValidateIndexOptions(s, method); err != nil {
		return catalog.RelationOptions{}, err
	}
	next := catalog.RelationOptions{Toast: old.Toast}
	for _, option := range old.Base {
		touched := false
		for _, request := range s.Options {
			if request.Namespace == "" && request.Name.Text == option.Name {
				touched = true
				break
			}
		}
		if !touched {
			next.Base = append(next.Base, option)
		}
	}
	if s.Action == "set" {
		for _, request := range s.Options {
			if request.Namespace != "" {
				continue
			}
			option := catalog.RelationOption{Name: request.Name.Text, Text: indexOptionText(request.Value)}
			if option.Name == "fillfactor" || option.Name == "pages_per_range" {
				option.Kind = catalog.RelationOptionInteger
				n := float64(request.Value.Integer)
				if request.Value.Kind != parser.IntegerValue {
					n, _, _ = settings.ParseNumber(request.Value.Text, true)
				}
				option.Integer = int64(math.RoundToEven(n))
			} else {
				option.Kind = catalog.RelationOptionBoolean
				switch request.Value.Kind {
				case parser.BooleanValue:
					option.Boolean = request.Value.Boolean
				case parser.IntegerValue:
					option.Boolean = request.Value.Integer == 1
				default:
					value, _ := (settings.Definition{Name: option.Name, Type: settings.Boolean}).Parse(request.Value.Text)
					option.Boolean = value.Boolean
				}
			}
			next.Base = append(next.Base, option)
		}
	}
	return next, nil
}

// CREATE rejects namespaces; ALTER retains PostgreSQL's toast-ignore policy.
// This validates the complete list, without accepting physical options as no-ops.
func ValidateIndexReloptions(options []parser.IndexOption, method catalog.IndexMethod, alter bool) error {
	seen := make(map[string]bool, len(options))
	for _, o := range options {
		if alter && o.Namespace == "toast" {
			continue
		}
		name := o.Name.Text
		if strings.Contains(name, "=") {
			return sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, name))
		}
		if o.Namespace != "" {
			return sqlError("22023", fmt.Sprintf(`unrecognized parameter namespace "%s"`, o.Namespace))
		}
		recognized := name == "fillfactor" || name == "deduplicate_items" && method == catalog.BtreeMethod
		if method == catalog.BrinMethod {
			recognized = name == "pages_per_range" || name == "autosummarize"
		}
		if !recognized {
			return sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if seen[name] {
			return sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
		}
		seen[name] = true
		if name == "fillfactor" || name == "pages_per_range" {
			n, suffix, ok := float64(o.Value.Integer), "", true
			if o.Value.Kind != parser.IntegerValue {
				n, suffix, ok = settings.ParseNumber(o.Value.Text, true)
			}
			n = math.RoundToEven(n)
			if !ok || suffix != "" || math.IsNaN(n) || n < math.MinInt32 || n > math.MaxInt32 {
				return sqlError("22023", fmt.Sprintf(`invalid value for integer option "%s": %s`, name, indexOptionText(o.Value)))
			}
			low, high := 10, 100
			if name == "pages_per_range" {
				low, high = 1, 131072
			}
			if n < float64(low) || n > float64(high) {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`value %s out of bounds for option "%s"`, indexOptionText(o.Value), name), Detail: fmt.Sprintf(`Valid values are between "%d" and "%d".`, low, high)}
			}
		} else {
			valid := o.Value.Kind == parser.BooleanValue
			if o.Value.Kind == parser.IntegerValue {
				valid = o.Value.Integer == 0 || o.Value.Integer == 1
			} else if !valid {
				_, err := (settings.Definition{Name: name, Type: settings.Boolean}).Parse(o.Value.Text)
				valid = err == nil
			}
			if !valid {
				return sqlError("22023", fmt.Sprintf(`invalid value for boolean option "%s": %s`, name, indexOptionText(o.Value)))
			}
		}
	}
	return nil
}

// Validate malformed controls in input order before target/schema/ACL admission.
// Earlier valid controls must not mask a later unknown or malformed option.
func ValidateReindexOptions(s *parser.Reindex) error {
	for _, o := range s.Options {
		switch o.Name.Text {
		case "verbose", "concurrently":
			if _, ok := reindexBoolean(o.Value); !ok {
				return sqlError("42601", o.Name.Text+" requires a Boolean value")
			}
		case "tablespace":
			if !o.HasValue {
				return sqlError("42601", "tablespace requires a parameter")
			}
		default:
			d := sqlError("42601", fmt.Sprintf(`unrecognized REINDEX option "%s"`, o.Name.Text)).(*sqlstate.Error)
			d.Position = strconv.Itoa(o.Name.Pos.Character)
			return d
		}
	}
	return nil
}

// Admission already validated the complete list. Keep valid capability rejection
// at its established target phase, separately from malformed-control precedence.
func ReindexControlError(s *parser.Reindex) error {
	if s.Kind.Text != "index" && s.Kind.Text != "table" {
		return Capability("REINDEX target is unsupported", s.Kind.Pos)
	}
	for _, o := range s.Options {
		switch o.Name.Text {
		case "tablespace":
			return Capability("index tablespaces are unsupported", o.Name.Pos)
		case "concurrently":
			if enabled, _ := reindexBoolean(o.Value); enabled {
				return Capability("concurrent index operations are unsupported", o.Name.Pos)
			}
		}
		return Capability("REINDEX options are unsupported", o.Name.Pos)
	}
	if s.Concurrent.Character > 0 {
		return Capability("concurrent index operations are unsupported", s.Concurrent)
	}
	return nil
}

// defGetBoolean is narrower than reloption parse_bool: only integer 0/1 or
// Boolean true/false (including those exact string spellings) are accepted.
func reindexBoolean(v parser.Value) (bool, bool) {
	switch v.Kind {
	case parser.BooleanValue:
		return v.Boolean, true
	case parser.IntegerValue:
		if v.Integer == 0 || v.Integer == 1 {
			return v.Integer == 1, true
		}
	case parser.StringValue:
		if strings.EqualFold(v.Text, "true") {
			return true, true
		}
		if strings.EqualFold(v.Text, "false") {
			return false, true
		}
	}
	return false, false
}

// Integer DefElem arguments stringify their decoded int4 value; quoted and
// numeric arguments retain spelling. Avoid normalizing successful requests.
func indexOptionText(v parser.Value) string {
	if v.Kind == parser.IntegerValue && v.Integer >= math.MinInt32 && v.Integer <= math.MaxInt32 {
		return strconv.FormatInt(v.Integer, 10)
	}
	return v.Text
}
