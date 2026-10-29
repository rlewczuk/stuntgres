package expr

import "context"

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
