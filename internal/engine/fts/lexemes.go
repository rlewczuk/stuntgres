package fts

// Lexeme retains dictionary-origin prefix and alternative-variant identity.
// Lexemes of one variant are conjunctive; distinct variants are alternatives.
type Lexeme struct {
	Text    string
	Prefix  bool
	Variant uint16
}

// LexicalResult distinguishes an unrecognized token from a recognized stopword.
// Providers return invocation-owned slices with immutable spelling strings.
type LexicalResult struct {
	Recognized bool
	Lexemes    []Lexeme
}

type LexicalPosition struct {
	Position int
	Lexemes  []Lexeme
}

type ConversionKind uint8

const (
	VectorConversion ConversionKind = iota
	StrictConversion
	PlainConversion
	PhraseConversion
	WebConversion
)

func (k ConversionKind) Valid() bool { return k <= WebConversion }
func (k ConversionKind) Name() string {
	switch k {
	case VectorConversion:
		return "to_tsvector"
	case StrictConversion:
		return "to_tsquery"
	case PlainConversion:
		return "plainto_tsquery"
	case PhraseConversion:
		return "phraseto_tsquery"
	case WebConversion:
		return "websearch_to_tsquery"
	}
	return ""
}
