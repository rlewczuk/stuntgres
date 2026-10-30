package sqltype

// NewMatcher shares only a validated program's immutable instructions/classes
// and preference metadata. It creates fresh execution-local scratch and budget;
// populated matcher slices, generation counters and contexts are never copied.
// The source program must not be mutated concurrently with this construction.
// Independent returned matchers can execute concurrently, including when the
// source has previously matched. Each returned matcher still has one owner.
func (p *PGRegexProgram) NewMatcher() *PGRegexProgram {
	return &PGRegexProgram{
		code: p.code, classes: p.classes, classRanges: p.classRanges, start: p.start,
		collation: p.collation, compiledBytes: p.compiledBytes, capture: p.capture,
		captures: p.captures, backrefs: p.backrefs, options: p.options,
		newlineStop: p.newlineStop, newlineAnchor: p.newlineAnchor,
		preference: p.preference, preferences: p.preferences, progress: p.progress,
		budget: pgRegexBudget{used: p.compiledBytes, limit: p.budget.limit, runtime: true},
	}
}
