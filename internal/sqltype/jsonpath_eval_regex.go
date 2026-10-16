package sqltype

// Match scratch belongs to one executor, not the immutable path or held plan.
func (e *jsonpathExecutor) match(n *jsonpathNode, text string) (bool, error) {
	program := e.regex[n]
	if program == nil {
		options := PGRegexOptions{CaseInsensitive: n.flags&1 != 0, NewlineStop: n.flags&2 == 0, NewlineAnchor: n.flags&4 != 0}
		if n.flags&16 != 0 {
			options = PGRegexOptions{Mode: pgRegexQuote, CaseInsensitive: n.flags&1 != 0}
		}
		var err error
		program, err = CompilePGRegex(e.ctx, n.text, options, CollationRef{})
		if err != nil {
			return false, err
		}
		if e.regex == nil {
			e.regex = make(map[*jsonpathNode]*PGRegexProgram)
		}
		e.regex[n] = program
	}
	result, err := program.Match(e.ctx, text, false)
	return result.Matched, err
}
