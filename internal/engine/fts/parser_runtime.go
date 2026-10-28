package fts

import (
	"context"
	"stuntgres/internal/sqlstate"
)

// ParserRuntime owns immutable callback identities, not catalog or session
// authority. Its catalog owner checks exact SQL signatures before construction.
// Each operation checks only the native capabilities it actually consumes.
type ParserRuntime struct {
	start, token, end, headline, lexType uint32
}

func NewParserRuntime(start, token, end, headline, lexType uint32) ParserRuntime {
	return ParserRuntime{start, token, end, headline, lexType}
}

func unsupportedParserMethod() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "text search parser callback has no native implementation"}
}

func (r ParserRuntime) Parse(ctx context.Context, text string) ([]DocumentToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.start != 3717 || r.token != 3718 || r.end != 3719 {
		return nil, unsupportedParserMethod()
	}
	return ParseDocument(ctx, text)
}

func (r ParserRuntime) TokenTypes(ctx context.Context) ([23]struct{ Alias, Description string }, error) {
	if err := ctx.Err(); err != nil {
		return [23]struct{ Alias, Description string }{}, err
	}
	if r.lexType != 3721 {
		return [23]struct{ Alias, Description string }{}, unsupportedParserMethod()
	}
	return DocumentTypes(), nil
}
