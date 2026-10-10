package unicode16

// RegexPropertyKind names only properties used by the internal SQL-regex
// provider. Every table is generated from the checked-in Unicode16 UCD.
type RegexPropertyKind uint8

const (
	RegexAlphabetic RegexPropertyKind = iota
	RegexDecimal
	RegexLowercase
	RegexUppercase
	RegexWhiteSpace
	RegexGraph
	RegexPrint
	RegexPunctuation
	RegexPunctOrSymbol
)

func RegexProperty(r rune, property RegexPropertyKind) bool {
	if r < 0 || r > 0x10FFFF {
		return false
	}
	switch property {
	case RegexAlphabetic:
		return caseProperty(r, alphabeticRanges[:])
	case RegexDecimal:
		return caseProperty(r, decimalRanges[:])
	case RegexLowercase:
		return caseProperty(r, regexLowercaseRanges[:])
	case RegexUppercase:
		return caseProperty(r, regexUppercaseRanges[:])
	case RegexWhiteSpace:
		return r >= 9 && r <= 13 || r == 0x85 || caseProperty(r, regexSeparatorRanges[:])
	case RegexGraph:
		return caseProperty(r, regexAssignedGraphicRanges[:]) && !RegexProperty(r, RegexWhiteSpace)
	case RegexPrint:
		return RegexProperty(r, RegexGraph) || caseProperty(r, regexSpaceSeparatorRanges[:])
	case RegexPunctuation:
		return caseProperty(r, regexPunctuationRanges[:])
	case RegexPunctOrSymbol:
		return caseProperty(r, regexPunctuationRanges[:]) || caseProperty(r, regexSymbolRanges[:])
	}
	return false
}
