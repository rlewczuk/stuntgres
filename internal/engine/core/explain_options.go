package core

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type explainOptions struct {
	costs, verbose, analyze, timing, summary bool
	format                                   string
}

func explainError(code, message string, pos parser.Position) error {
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
		return "", explainError("42601", option.Name+" requires a parameter", parser.Position{})
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
func explainDescribeFormat(statement *parser.Explain) (string, error) {
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
			return false, explainError("42601", option.Name+" requires a Boolean value", parser.Position{})
		case parser.NumericValue:
			return false, explainError("42601", option.Name+" requires a Boolean value", parser.Position{})
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
	return false, explainError("42601", strings.ToLower(option.Name)+" requires a Boolean value", parser.Position{})
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

func validateExplainOptions(statement *parser.Explain) (explainOptions, error) {
	options := explainOptions{costs: true, format: "text"}
	booleans := [explainBooleanCount]bool{explainCosts: true}
	var positions [explainBooleanCount]parser.Position
	var formatPosition, serializePosition parser.Position
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
				return options, explainError("22023", fmt.Sprintf("unrecognized value for EXPLAIN option %q: %q", name, value), option.Pos)
			}
			if name == "format" {
				format, formatPosition = lower, option.Pos
			} else {
				serialize, serializePosition = lower, option.Pos
			}
		default:
			return options, explainError("42601", fmt.Sprintf("unrecognized EXPLAIN option %q", option.Name), option.Pos)
		}
	}
	if booleans[explainWAL] && !booleans[explainAnalyze] {
		return options, explainError("22023", "EXPLAIN option WAL requires ANALYZE", parser.Position{})
	}
	if booleans[explainTiming] && !booleans[explainAnalyze] {
		return options, explainError("22023", "EXPLAIN option TIMING requires ANALYZE", parser.Position{})
	}
	if serialize != "none" && !booleans[explainAnalyze] {
		return options, explainError("22023", "EXPLAIN option SERIALIZE requires ANALYZE", parser.Position{})
	}
	if booleans[explainAnalyze] && booleans[explainGenericPlan] {
		return options, explainError("22023", "EXPLAIN options ANALYZE and GENERIC_PLAN cannot be used together", parser.Position{})
	}
	for _, index := range [...]int{explainSettings, explainGenericPlan, explainBuffers, explainWAL, explainMemory} {
		if booleans[index] {
			return options, explainError("0A000", "EXPLAIN option "+strings.ToUpper(explainBooleanNames[index])+" is not supported", positions[index])
		}
	}
	if serialize != "none" {
		return options, explainError("0A000", "EXPLAIN option SERIALIZE is not supported", serializePosition)
	}
	if format != "text" && format != "json" {
		return options, explainError("0A000", "EXPLAIN format "+strings.ToUpper(format)+" is not supported", formatPosition)
	}
	options.costs, options.verbose = booleans[explainCosts], booleans[explainVerbose]
	options.format = format
	options.analyze = booleans[explainAnalyze]
	options.timing, options.summary = options.analyze, options.analyze
	if specified[explainTiming] {
		options.timing = booleans[explainTiming]
	}
	if specified[explainSummary] {
		options.summary = booleans[explainSummary]
	}
	return options, nil
}
