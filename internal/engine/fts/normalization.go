package fts

import "context"

// NormalizeTokens assigns positions only to recognized tokens, including empty
// stopwords. The caller supplies its captured dictionary chain and consumes each
// invocation-owned lexical result synchronously; the kernel owns no SQL state.
// Streaming avoids a positional scratch array when building a vector.
func NormalizeTokens(ctx context.Context, tokens []DocumentToken, position *int, lexize func(DocumentToken) (LexicalResult, error), emit func(LexicalPosition)) error {
	for _, token := range tokens {
		if err := ctx.Err(); err != nil {
			return err
		}
		lexical, err := lexize(token)
		if err != nil {
			return err
		}
		if !lexical.Recognized {
			continue
		}
		*position++
		emit(LexicalPosition{Position: *position, Lexemes: lexical.Lexemes})
	}
	return ctx.Err()
}
