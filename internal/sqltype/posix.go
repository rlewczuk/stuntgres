package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

type PosixPlan struct {
	args      [2]Usage
	result    Usage
	operator  string
	collation CollationRef
	conflict  bool
}

func BindPosix(operator string, left, right, result Usage, collation CollationRef, conflict bool) (PosixPlan, error) {
	p := PosixPlan{[2]Usage{left, right}, result, operator, collation, conflict}
	if !left.Valid() || !right.Valid() || !result.Valid() || result.Type.ID() != Bool || right.Type.ID() != Text || (left.Type.ID() != Text && left.Type.ID() != Name && left.Type.ID() != BPChar) || (operator != "~" && operator != "~*" && operator != "!~" && operator != "!~*") {
		return p, fmt.Errorf("invalid POSIX signature")
	}
	return p, nil
}
func (p PosixPlan) Operator() string        { return p.operator }
func (p PosixPlan) Collation() CollationRef { return p.collation }
func (p PosixPlan) Conflict() bool          { return p.conflict }
func regexCollationConflict() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for regular expression", Hint: "Use the COLLATE clause to set the collation explicitly."}
}
func (p PosixPlan) Evaluate(ctx context.Context, a, b Value, state *PGRegexExecution) (Value, error) {
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	if a.typ != p.args[0].Type || b.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid POSIX values")
	}
	if a.null || b.null {
		state.Reset()
		return Null(p.result.Type), nil
	}
	if p.conflict {
		state.Reset()
		return Value{}, regexCollationConflict()
	}
	result, err := state.regexMatch(ctx, b.text, a.text, PGRegexOptions{CaseInsensitive: p.operator == "~*" || p.operator == "!~*"}, p.collation, false)
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result.Type, result.Matched != (p.operator == "!~" || p.operator == "!~*")), nil
}

type PosixSubstringPlan struct {
	args      [2]Usage
	result    Usage
	collation CollationRef
	conflict  bool
}

func BindPosixSubstring(args [2]Usage, result Usage, collation CollationRef, conflict bool) (PosixSubstringPlan, error) {
	p := PosixSubstringPlan{args, result, collation, conflict}
	if !result.Valid() || result.Type.ID() != Text {
		return p, fmt.Errorf("invalid POSIX substring result")
	}
	for _, arg := range args {
		if !arg.Valid() || arg.Type.ID() != Text {
			return p, fmt.Errorf("invalid POSIX substring argument")
		}
	}
	return p, nil
}
func (p PosixSubstringPlan) Collation() CollationRef { return p.collation }
func (p PosixSubstringPlan) Conflict() bool          { return p.conflict }
func (p PosixSubstringPlan) Evaluate(ctx context.Context, args [2]Value, state *PGRegexExecution) (Value, error) {
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	for k, arg := range args {
		if arg.typ != p.args[k].Type {
			return Value{}, fmt.Errorf("invalid POSIX substring values")
		}
	}
	if args[0].null || args[1].null {
		state.Reset()
		return Null(p.result.Type), nil
	}
	if p.conflict {
		state.Reset()
		return Value{}, regexCollationConflict()
	}
	match, err := state.regexMatch(ctx, args[1].text, args[0].text, PGRegexOptions{}, p.collation, true)
	if err != nil {
		return Value{}, err
	}
	if !match.Matched {
		return Null(p.result.Type), nil
	}
	span := match.Whole
	if len(match.Captures) > 0 {
		span = match.Captures[0]
	}
	if span.Start < 0 {
		return Null(p.result.Type), nil
	}
	return Value{typ: p.result.Type, text: args[0].text[span.Start:span.End]}, nil
}
