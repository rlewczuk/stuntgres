package expr

import (
	"context"
	"stuntgres/internal/engine/fts"
)

// DictionaryLexizer is the scalar lexical capability borrowed by a phrase
// invocation/compiler. Persistent dictionaries retain only their pure kernel.
type DictionaryLexizer interface {
	Lexize(context.Context, string) (fts.LexicalResult, error)
}

// PhraseLexeme is an immutable replacement word and its relative position flag.
type PhraseLexeme struct {
	Text        string
	Prefix      bool
	Variant     uint16
	AddPosition bool
}

// PhraseMatcher owns one invocation's continuation, never catalog authority.
// A nil completion is unrecognized; more requests another parser token.
type PhraseMatcher interface {
	Step(context.Context, string, bool) ([]PhraseLexeme, bool, error)
	Reset()
}
