package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func indexCapability(message string, pos parser.Position) error {
	d := sqlError("0A000", message).(*sqlstate.Error)
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}

// Validate reloptions in input order. A first malformed value wins over a later
// duplicate; RESET has a different contract and accepts unknown/repeated names.
func validateIndexOptions(s *parser.AlterIndex, method indexMethod) error {
	if s.Action == "reset" {
		for _, o := range s.Options {
			if o.HasValue {
				return sqlError("42601", "RESET must not include values for parameters")
			}
		}
		return indexCapability("index storage parameters are unsupported", parser.Position{})
	}
	if err := validateIndexReloptions(s.Options, method, true); err != nil {
		return err
	}
	return indexCapability("index storage parameters are unsupported", parser.Position{})
}

// CREATE rejects namespaces; ALTER retains PostgreSQL's toast-ignore policy.
// This validates the complete list, without accepting physical options as no-ops.
func validateIndexReloptions(options []parser.IndexOption, method indexMethod, alter bool) error {
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
		if name != "fillfactor" && (name != "deduplicate_items" || method != btreeMethod) {
			return sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if seen[name] {
			return sqlError("22023", fmt.Sprintf(`parameter "%s" specified more than once`, name))
		}
		seen[name] = true
		if name == "fillfactor" {
			n, suffix, ok := float64(o.Value.Integer), "", true
			if o.Value.Kind != parser.IntegerValue {
				n, suffix, ok = settingNumber(o.Value.Text, true)
			}
			n = math.RoundToEven(n)
			if !ok || suffix != "" || math.IsNaN(n) || n < math.MinInt32 || n > math.MaxInt32 {
				return sqlError("22023", fmt.Sprintf(`invalid value for integer option "fillfactor": %s`, indexOptionText(o.Value)))
			}
			if n < 10 || n > 100 {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`value %s out of bounds for option "fillfactor"`, indexOptionText(o.Value)), Detail: `Valid values are between "10" and "100".`}
			}
		} else {
			valid := o.Value.Kind == parser.BooleanValue
			if o.Value.Kind == parser.IntegerValue {
				valid = o.Value.Integer == 0 || o.Value.Integer == 1
			} else if !valid {
				_, err := (SettingDefinition{Name: name, Type: SettingBoolean}).parse(o.Value.Text)
				valid = err == nil
			}
			if !valid {
				return sqlError("22023", fmt.Sprintf(`invalid value for boolean option "deduplicate_items": %s`, indexOptionText(o.Value)))
			}
		}
	}
	return nil
}

// Validate malformed controls in input order before target/schema/ACL admission.
// Earlier valid controls must not mask a later unknown or malformed option.
func validateReindexOptions(s *parser.Reindex) error {
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
func reindexControlError(s *parser.Reindex) error {
	if s.Kind.Text != "index" && s.Kind.Text != "table" {
		return indexCapability("REINDEX target is unsupported", s.Kind.Pos)
	}
	for _, o := range s.Options {
		switch o.Name.Text {
		case "tablespace":
			return indexCapability("index tablespaces are unsupported", o.Name.Pos)
		case "concurrently":
			if enabled, _ := reindexBoolean(o.Value); enabled {
				return indexCapability("concurrent index operations are unsupported", o.Name.Pos)
			}
		}
		return indexCapability("REINDEX options are unsupported", o.Name.Pos)
	}
	if s.Concurrent.Character > 0 {
		return indexCapability("concurrent index operations are unsupported", s.Concurrent)
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
