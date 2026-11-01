package functions

import (
	"context"

	"stuntgres/internal/engine/fts"
)

// IntDictionary is dict_int's immutable whole-token byte normalizer. The
// extension option compiler supplies a positive maxlen; token selection belongs
// to the shared text-search parser, not this kernel.
type IntDictionary struct {
	maxlen     int
	rejectlong bool
	absval     bool
}

func NewIntDictionary(maxlen int, rejectlong, absval bool) *IntDictionary {
	return &IntDictionary{maxlen: maxlen, rejectlong: rejectlong, absval: absval}
}

func (d *IntDictionary) Configured(maxlen int, rejectlong, absval bool) bool {
	return d != nil && maxlen > 0 && d.maxlen == maxlen && d.rejectlong == rejectlong && d.absval == absval
}

func (*IntDictionary) Filtering() bool { return false }

func (d *IntDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	if d.absval && len(input) != 0 && (input[0] == '+' || input[0] == '-') {
		input = input[1:]
	}
	if len(input) > d.maxlen {
		if d.rejectlong {
			return fts.LexicalResult{Recognized: true}, nil
		}
		input = input[:d.maxlen]
	}
	// Empty input is one empty lexeme, not a stopword. The result owns its
	// lexeme slice and shares only immutable string backing with the input.
	return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{{Text: input}}}, nil
}
