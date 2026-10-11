package sqltype

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func (s *PGRegexExecution) normalize(ctx context.Context, pattern, escape string) (string, error) {
	if s.translationValid && escape == s.escape {
		same, err := pgRegexCacheEqual(ctx, pattern, s.pattern)
		if err != nil {
			s.Reset()
			return "", err
		}
		if same {
			return s.translated, nil
		}
	}
	// Drop obsolete execution ownership before sizing/building the replacement.
	s.Reset()
	translated, err := translateSimilarBudget(ctx, pattern, escape, s.memoryLimit())
	if err != nil {
		s.Reset()
		return "", err
	}
	s.pattern, s.escape, s.translated, s.translationValid = pattern, escape, translated, true
	return translated, nil
}

// SQL SIMILAR's anchored translation selects its separator capture, or the
// whole input when no separator exists. Its wrapper contract stays separate.
func (s *PGRegexExecution) match(ctx context.Context, canonical, source string, collation CollationRef) (bool, int, int, error) {
	program, err := s.resolveProgram(ctx, canonical, PGRegexOptions{}, collation)
	if err != nil {
		return false, -1, -1, err
	}
	// Capture-free anchored extraction returns the source directly. It needs
	// only acceptance, not the POSIX frontend's selected whole-match span.
	result, err := program.Match(ctx, source, program.HasCapture())
	if err != nil {
		s.Reset()
	}
	if err != nil || !result.Matched {
		return result.Matched, -1, -1, err
	}
	if len(result.Captures) > 0 {
		return true, result.Captures[0].Start, result.Captures[0].End, nil
	}
	return true, -1, -1, nil
}

type SimilarPlan struct {
	args      [2]Usage
	result    Usage
	operator  string
	collation CollationRef
	conflict  bool
}

func BindSimilar(operator string, left, right, result Usage, collation CollationRef, conflict bool) (SimilarPlan, error) {
	p := SimilarPlan{[2]Usage{left, right}, result, operator, collation, conflict}
	if !left.Valid() || !right.Valid() || !result.Valid() || result.Type.ID() != Bool || (operator != "~" && operator != "!~") || right.Type.ID() != Text || (left.Type.ID() != Text && left.Type.ID() != Name && left.Type.ID() != BPChar) {
		return p, fmt.Errorf("invalid SIMILAR signature")
	}
	return p, nil
}
func (p SimilarPlan) Operator() string        { return p.operator }
func (p SimilarPlan) Collation() CollationRef { return p.collation }
func (p SimilarPlan) Conflict() bool          { return p.conflict }
func (p SimilarPlan) Evaluate(ctx context.Context, a, b Value, state *PGRegexExecution) (Value, error) {
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	if a.typ != p.args[0].Type || b.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid SIMILAR values")
	}
	if a.null || b.null {
		state.Reset()
		return Null(p.result.Type), nil
	}
	if p.conflict {
		state.Reset()
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for regular expression", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	result, err := state.regexMatch(ctx, b.text, a.text, PGRegexOptions{}, p.collation, false)
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result.Type, result.Matched != (p.operator == "!~")), nil
}

type SimilarEscapePlan struct {
	args   [2]Usage
	result Usage
}

func BindSimilarEscape(pattern, escape, result Usage) (SimilarEscapePlan, error) {
	p := SimilarEscapePlan{[2]Usage{pattern, escape}, result}
	if !pattern.Valid() || !escape.Valid() || !result.Valid() || pattern.Type.ID() != Text || escape.Type.ID() != Text || result.Type.ID() != Text {
		return p, fmt.Errorf("invalid SIMILAR escape signature")
	}
	return p, nil
}
func (p SimilarEscapePlan) Evaluate(ctx context.Context, pattern, escape Value, state *PGRegexExecution) (Value, error) {
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	if pattern.typ != p.args[0].Type || escape.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid SIMILAR escape values")
	}
	if pattern.null || escape.null {
		state.Reset()
		return Null(p.result.Type), nil
	}
	if escape.text != "" {
		_, width := utf8.DecodeRuneInString(escape.text)
		if width != len(escape.text) {
			state.Reset()
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}
		}
	}
	translated, err := state.normalize(ctx, pattern.text, escape.text)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: translated}, nil
}

type SimilarSubstringPlan struct {
	args           [3]Usage
	result         Usage
	wrapperContext bool
}

func BindSimilarSubstring(args [3]Usage, result Usage, wrapperContext bool) (SimilarSubstringPlan, error) {
	p := SimilarSubstringPlan{args, result, wrapperContext}
	if !result.Valid() || result.Type.ID() != Text {
		return p, fmt.Errorf("invalid SIMILAR substring result")
	}
	for _, arg := range args {
		if !arg.Valid() || arg.Type.ID() != Text {
			return p, fmt.Errorf("invalid SIMILAR substring argument")
		}
	}
	return p, nil
}
func (p SimilarSubstringPlan) WrapperContext() bool { return p.wrapperContext }
func (p SimilarSubstringPlan) Evaluate(ctx context.Context, args [3]Value, state *PGRegexExecution) (Value, error) {
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	for i, arg := range args {
		if arg.typ != p.args[i].Type {
			return Value{}, fmt.Errorf("invalid SIMILAR substring values")
		}
	}
	for _, arg := range args {
		if arg.null {
			state.Reset()
			return Null(p.result.Type), nil
		}
	}
	helper, _ := BindSimilarEscape(p.args[1], p.args[2], p.result)
	canonical, err := helper.Evaluate(ctx, args[1], args[2], state)
	if err != nil {
		return Value{}, p.decorate(err)
	}
	matched, start, end, err := state.match(ctx, canonical.text, args[0].text, CollationRef{ID: 100, Derivation: Default})
	if err != nil {
		return Value{}, p.decorate(err)
	}
	if !matched {
		return Null(p.result.Type), nil
	}
	if start < 0 {
		if state.program.HasCapture() {
			return Null(p.result.Type), nil
		}
		start, end = 0, len(args[0].text)
	}
	return Value{typ: p.result.Type, text: args[0].text[start:end]}, nil
}
func (p SimilarSubstringPlan) decorate(err error) error {
	var diagnostic *sqlstate.Error
	if p.wrapperContext && errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Context = `SQL function "substring" statement 1`
		return &copy
	}
	return err
}
