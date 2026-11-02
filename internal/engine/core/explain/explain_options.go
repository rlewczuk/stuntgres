package explain

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type Options struct {
	Costs, Verbose, Analyze, Timing, Summary, Debug bool
	Format                                          string
}

// Structured selects detached typed properties, rather than TEXT detail lines.
func (o Options) Structured() bool { return o.Format != "" && o.Format != "text" }

func Error(code, message string, pos parser.Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: explainPosition(pos)}
}

func explainPosition(pos parser.Position) string {
	if pos.Character == 0 {
		return ""
	}
	return strconv.Itoa(pos.Character)
}

func explainOptionString(option parser.ExplainOption) (string, error) {
	switch value := option.Value.(type) {
	case nil:
		return "", Error("42601", option.Name+" requires a parameter", parser.Position{})
	case parser.ColumnExpr:
		return value.Name.Parts[0].Text, nil
	case parser.LiteralExpr:
		switch value.Value.Kind {
		case parser.StringValue, parser.NumericValue:
			return value.Value.Text, nil
		case parser.IntegerValue:
			return strconv.FormatInt(value.Value.Integer, 10), nil
		case parser.BooleanValue:
			return strconv.FormatBool(value.Value.Boolean), nil
		}
	}
	return "", fmt.Errorf("stuntgres: invalid EXPLAIN option value")
}

// Describe only consults FORMAT to determine the result descriptor. Execution
// validates all entries, including invalid entries overridden by later ones.
func DescribeFormat(statement *parser.Explain) (string, error) {
	format := "text"
	for _, option := range statement.Options {
		if option.Name == "format" {
			value, err := explainOptionString(option)
			if err != nil {
				return "", err
			}
			format = value
		}
	}
	return format, nil
}

func explainBoolean(option parser.ExplainOption) (bool, error) {
	if option.Value == nil {
		return true, nil
	}
	if literal, ok := option.Value.(parser.LiteralExpr); ok {
		switch literal.Value.Kind {
		case parser.IntegerValue:
			if literal.Value.Integer == 0 || literal.Value.Integer == 1 {
				return literal.Value.Integer == 1, nil
			}
			return false, Error("42601", option.Name+" requires a Boolean value", parser.Position{})
		case parser.NumericValue:
			return false, Error("42601", option.Name+" requires a Boolean value", parser.Position{})
		}
	}
	text, err := explainOptionString(option)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(text) {
	case "true", "on":
		return true, nil
	case "false", "off":
		return false, nil
	}
	return false, Error("42601", strings.ToLower(option.Name)+" requires a Boolean value", parser.Position{})
}

const (
	explainAnalyze = iota
	explainVerbose
	explainCosts
	explainSettings
	explainGenericPlan
	explainBuffers
	explainWAL
	explainTiming
	explainSummary
	explainMemory
	explainBooleanCount
)

var explainBooleanNames = [explainBooleanCount]string{"analyze", "verbose", "costs", "settings", "generic_plan", "buffers", "wal", "timing", "summary", "memory"}

func ValidateOptions(statement *parser.Explain, overexplainLoaded bool) (Options, error) {
	options := Options{Costs: true, Format: "text"}
	booleans := [explainBooleanCount]bool{explainCosts: true}
	var positions [explainBooleanCount]parser.Position
	var serializePosition parser.Position
	var specified [explainBooleanCount]bool
	format, serialize := "text", "none"
	for _, option := range statement.Options {
		name := option.Name
		index := -1
		for i, candidate := range explainBooleanNames {
			if candidate == name {
				index = i
				break
			}
		}
		if index >= 0 {
			value, err := explainBoolean(option)
			if err != nil {
				return options, err
			}
			booleans[index], positions[index] = value, option.Pos
			specified[index] = true
			continue
		}
		switch name {
		case "debug":
			if !overexplainLoaded {
				return options, Error("42601", `unrecognized EXPLAIN option "debug"`, option.Pos)
			}
			value, err := explainBoolean(option)
			if err != nil {
				return options, err
			}
			options.Debug = value
		case "format", "serialize":
			value := "text"
			if option.Value != nil || name == "format" {
				var err error
				value, err = explainOptionString(option)
				if err != nil {
					return options, err
				}
			}
			lower := value
			if name == "serialize" {
				lower = strings.ToLower(value)
			}
			valid := name == "format" && (lower == "text" || lower == "json" || lower == "xml" || lower == "yaml") || name == "serialize" && (lower == "none" || lower == "text" || lower == "binary")
			if !valid {
				return options, Error("22023", fmt.Sprintf("unrecognized value for EXPLAIN option %q: %q", name, value), option.Pos)
			}
			if name == "format" {
				format = lower
			} else {
				serialize, serializePosition = lower, option.Pos
			}
		default:
			return options, Error("42601", fmt.Sprintf("unrecognized EXPLAIN option %q", option.Name), option.Pos)
		}
	}
	if booleans[explainWAL] && !booleans[explainAnalyze] {
		return options, Error("22023", "EXPLAIN option WAL requires ANALYZE", parser.Position{})
	}
	if booleans[explainTiming] && !booleans[explainAnalyze] {
		return options, Error("22023", "EXPLAIN option TIMING requires ANALYZE", parser.Position{})
	}
	if serialize != "none" && !booleans[explainAnalyze] {
		return options, Error("22023", "EXPLAIN option SERIALIZE requires ANALYZE", parser.Position{})
	}
	if booleans[explainAnalyze] && booleans[explainGenericPlan] {
		return options, Error("22023", "EXPLAIN options ANALYZE and GENERIC_PLAN cannot be used together", parser.Position{})
	}
	for _, index := range [...]int{explainSettings, explainGenericPlan, explainBuffers, explainWAL, explainMemory} {
		if booleans[index] {
			return options, Error("0A000", "EXPLAIN option "+strings.ToUpper(explainBooleanNames[index])+" is not supported", positions[index])
		}
	}
	if serialize != "none" {
		return options, Error("0A000", "EXPLAIN option SERIALIZE is not supported", serializePosition)
	}
	options.Costs, options.Verbose = booleans[explainCosts], booleans[explainVerbose]
	options.Format = format
	options.Analyze = booleans[explainAnalyze]
	options.Timing, options.Summary = options.Analyze, options.Analyze
	if specified[explainTiming] {
		options.Timing = booleans[explainTiming]
	}
	if specified[explainSummary] {
		options.Summary = booleans[explainSummary]
	}
	return options, nil
}
