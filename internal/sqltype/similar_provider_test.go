package sqltype

import "context"

// Existing SQL-translation tests retain their first separator-capture contract;
// common-provider tests assert the complete result separately.
func similarTestMatch(p *PGRegexProgram, ctx context.Context, source string) (bool, int, int, error) {
	result, err := p.Match(ctx, source, p.HasCapture())
	if err != nil || !result.Matched {
		return result.Matched, -1, -1, err
	}
	if len(result.Captures) > 0 {
		return true, result.Captures[0].Start, result.Captures[0].End, nil
	}
	return true, -1, -1, nil
}
