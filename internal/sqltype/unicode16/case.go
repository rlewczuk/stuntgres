package unicode16

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// CaseOperation selects a PostgreSQL string case provider.
type CaseOperation uint8

const (
	Lower CaseOperation = iota + 1
	Upper
	Fold
	Initcap
)

// CaseMode selects only the three fixed, language-independent builtin modes.
type CaseMode uint8

const (
	ASCII CaseMode = iota
	Simple
	Full
)

type caseMapping struct {
	runes [3]rune
	count uint8
}
type caseEntry struct {
	code   rune
	simple [4]rune // lower, upper, fold, title
	full   [4]caseMapping
}
type caseRange struct{ lo, hi rune }

func caseProperty(r rune, ranges []caseRange) bool {
	lo, hi := 0, len(ranges)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if ranges[mid].hi < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(ranges) && ranges[lo].lo <= r
}
func caseRecord(r rune) *caseEntry {
	lo, hi := 0, len(caseEntries)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if caseEntries[mid].code < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(caseEntries) && caseEntries[lo].code == r {
		return &caseEntries[lo]
	}
	return nil
}

// SimpleCase returns one scalar using the pinned PostgreSQL Unicode tables.
// Regex case handling never expands a character or applies contextual casing.
func SimpleCase(r rune, operation CaseOperation, mode CaseMode) rune {
	if mode == ASCII {
		if operation == Lower && r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if operation == Upper && r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	if entry := caseRecord(r); entry != nil && operation >= Lower && operation <= Fold {
		return entry.simple[operation-1]
	}
	return r
}

// WalkSimpleCaseRange visits the simple lower/upper mappings of original range
// members with case records. It does not visit mappings of newly added members,
// so callers can construct a nontransitive closure while retaining the original
// interval. The visitor owns checked storage and primitive cancellation polls.
func WalkSimpleCaseRange(lo, hi rune, mode CaseMode, visit func(lower, upper rune) error) error {
	if mode == ASCII {
		for c := rune('A'); c <= 'Z'; c++ {
			if c >= lo && c <= hi {
				if err := visit(c+32, c); err != nil {
					return err
				}
			}
			if c+32 >= lo && c+32 <= hi {
				if err := visit(c+32, c); err != nil {
					return err
				}
			}
		}
		return nil
	}
	left, right := 0, len(caseEntries)
	for left < right {
		mid := left + (right-left)/2
		if caseEntries[mid].code < lo {
			left = mid + 1
		} else {
			right = mid
		}
	}
	for at := left; at < len(caseEntries) && caseEntries[at].code <= hi; at++ {
		e := &caseEntries[at]
		if err := visit(e.simple[0], e.simple[1]); err != nil {
			return err
		}
	}
	return nil
}

type caseScan struct {
	ctx  context.Context
	work uint32
}

func (s *caseScan) step() error {
	s.work++
	if s.work&255 == 0 {
		return s.ctx.Err()
	}
	return nil
}

// finalSigma scans original input, never mapped output. Ignorable runs after
// distinct sigmas cannot overlap: sigma itself is not Case_Ignorable.
func (s *caseScan) finalSigma(source string, offset int, precedingCased bool) (bool, error) {
	if !precedingCased {
		return false, nil
	}
	for offset < len(source) {
		if err := s.step(); err != nil {
			return false, err
		}
		r, n := utf8.DecodeRuneInString(source[offset:])
		offset += n
		if caseProperty(r, caseIgnorableRanges[:]) {
			continue
		}
		return !caseProperty(r, casedRanges[:]), nil
	}
	return true, nil
}

// ConvertCase borrows unchanged strings. Changed output is built lazily and
// published only after bounded cancellation checks, including sigma lookahead.
// Input validity is owned by the SQL textual boundary.
func ConvertCase(ctx context.Context, source string, operation CaseOperation, mode CaseMode) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if operation < Lower || operation > Initcap || mode > Full {
		return "", fmt.Errorf("invalid case operation")
	}
	if mode == ASCII {
		return asciiCase(ctx, source, operation)
	}
	scan := caseScan{ctx: ctx}
	var out strings.Builder
	changed, copied := false, 0
	previousAlnum, previousCased := false, false
	for offset := 0; offset < len(source); {
		if err := scan.step(); err != nil {
			return "", err
		}
		r, width := utf8.DecodeRuneInString(source[offset:])
		index := int(operation) - 1
		if operation == Initcap {
			alnum := caseProperty(r, alphabeticRanges[:]) || r >= '0' && r <= '9' || mode == Full && caseProperty(r, decimalRanges[:])
			index = 0
			if alnum && !previousAlnum {
				index = 1
				if mode == Full {
					index = 3
				}
			}
			previousAlnum = alnum
		}
		mapping := caseMapping{runes: [3]rune{r}, count: 1}
		if entry := caseRecord(r); entry != nil {
			if mode == Full {
				mapping = entry.full[index]
			} else {
				mapping.runes[0] = entry.simple[index]
			}
		}
		if mode == Full && index == 0 && r == 0x03A3 {
			// As in PostgreSQL 18.6, the nearest preceding character that is
			// not Case_Ignorable must be cased; an entirely ignorable prefix
			// (which 18.0 accepted) is not.
			final, err := scan.finalSigma(source, offset+width, previousCased)
			if err != nil {
				return "", err
			}
			if final {
				mapping = finalSigmaLower
			}
		}
		if !caseProperty(r, caseIgnorableRanges[:]) {
			previousCased = caseProperty(r, casedRanges[:])
		}
		if mapping.count != 1 || mapping.runes[0] != r {
			if !changed {
				out.Grow(len(source))
				changed = true
			}
			out.WriteString(source[copied:offset])
			for _, mapped := range mapping.runes[:mapping.count] {
				out.WriteRune(mapped)
			}
			copied = offset + width
		}
		offset += width
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !changed {
		return source, nil
	}
	out.WriteString(source[copied:])
	return out.String(), nil
}

func asciiCase(ctx context.Context, source string, operation CaseOperation) (string, error) {
	var out strings.Builder
	changed, copied, previousAlnum := false, 0, false
	for i := range len(source) {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		c, mapped := source[i], source[i]
		upper := operation == Upper
		if operation == Initcap {
			alnum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
			upper = alnum && !previousAlnum
			previousAlnum = alnum
		}
		if upper {
			if c >= 'a' && c <= 'z' {
				mapped = c - ('a' - 'A')
			}
		} else if c >= 'A' && c <= 'Z' {
			mapped = c + ('a' - 'A')
		}
		if mapped != c {
			if !changed {
				out.Grow(len(source))
				changed = true
			}
			out.WriteString(source[copied:i])
			out.WriteByte(mapped)
			copied = i + 1
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !changed {
		return source, nil
	}
	out.WriteString(source[copied:])
	return out.String(), nil
}
