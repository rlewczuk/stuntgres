package sqltype

import (
	"context"
	"unsafe"
)

// PGRegexExecution belongs to one statement execution, never a prepared plan.
// It retains only one active pattern/program and, for SIMILAR, translation.
// Compiled IR and matcher scratch share the checked memory allowance.
type PGRegexExecution struct {
	pattern, escape, translated string
	translationValid            bool
	canonical                   string
	collation                   CollationRef
	program                     *PGRegexProgram
	options                     PGRegexOptions
	// Zero uses the production ceiling. Units may inject a smaller allowance.
	limit int
}

func (s *PGRegexExecution) Reset() { *s = PGRegexExecution{limit: s.limit} }
func (s *PGRegexExecution) memoryLimit() int {
	if s.limit == 0 {
		return pgRegexMemoryLimit
	}
	return s.limit
}
func pgRegexCacheEqual(ctx context.Context, a, b string) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	if unsafe.StringData(a) == unsafe.StringData(b) {
		return true, nil
	}
	for at := 0; at < len(a); at += 4096 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(at+4096, len(a))
		if a[at:end] != b[at:end] {
			return false, nil
		}
	}
	return true, nil
}

// resolveProgram is shared by frontends so demand can depend on compiled metadata
// without duplicating cache admission, replacement ownership or error reset.
func (s *PGRegexExecution) resolveProgram(ctx context.Context, canonical string, options PGRegexOptions, collation CollationRef) (*PGRegexProgram, error) {
	same := false
	if s.program != nil && s.collation == collation && s.options == options {
		var err error
		same, err = pgRegexCacheEqual(ctx, canonical, s.canonical)
		if err != nil {
			s.Reset()
			return nil, err
		}
	}
	if !same {
		// Drop obsolete ownership before sizing or allocating its replacement.
		s.program, s.canonical, s.collation = nil, "", CollationRef{}
		// A translated string is owned only if compilation counts that exact backing.
		if len(s.translated) != len(canonical) || unsafe.StringData(s.translated) != unsafe.StringData(canonical) {
			s.pattern, s.escape, s.translated, s.translationValid = "", "", "", false
		}
		program, err := compilePGRegexBudget(ctx, canonical, options, collation, s.memoryLimit(), false)
		if err != nil {
			s.Reset()
			return nil, err
		}
		s.program, s.canonical, s.collation, s.options = program, canonical, collation, options
	}
	return s.program, nil
}
func (s *PGRegexExecution) regexMatch(ctx context.Context, canonical, source string, options PGRegexOptions, collation CollationRef, wantCaptures bool) (PGRegexMatch, error) {
	program, err := s.resolveProgram(ctx, canonical, options, collation)
	if err != nil {
		return PGRegexMatch{}, err
	}
	result, err := program.Match(ctx, source, wantCaptures)
	if err != nil {
		s.Reset()
	}
	return result, err
}
