package xml

import (
	"context"
	"errors"
)

// compareXSLTText compares ascending keys, as libxslt 1.1.35's POSIX
// xsltStrxfrm/strcmp path does. Only en_US.utf8 is a linguistic locale in the
// pinned image. Missing, malformed and unavailable languages select xmlStrcmp,
// not the process locale. The caller alone handles descending/stable ties.
//
// Although preproc.c records lower_first, the released POSIX sorter never reads
// it: case-order does not alter these keys. In particular, upper-first must not
// be implemented by changing case weights or swapping only case-only ties.
func compareXSLTText(ctx context.Context, lang, caseOrder, a, b string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	_ = caseOrder
	if !xsltEnglishLocale(lang) {
		return xsltCompareBytes(ctx, a, b)
	}
	// strxfrm handles the empty C string separately, before emitting levels.
	if len(a) == 0 || a[0] == 0 || len(b) == 0 || b[0] == 0 {
		return xsltCollationResult(ctx, xsltBoolInt(len(a) != 0 && a[0] != 0)-xsltBoolInt(len(b) != 0 && b[0] != 0))
	}
	left := xsltCollationIterator{text: a}
	right := xsltCollationIterator{text: b}
	// Compare the actual byte streams for each transform level. Separators
	// sort before all weights; position is present only at the final level.
	// There is no scalar normalization, rune fallback, or final byte tiebreak.
	for level := range 4 {
		left.reset(level)
		right.reset(level)
		for n := uint32(0); ; n++ {
			if n&255 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
			x, xok, err := left.next(ctx)
			if err != nil {
				return 0, err
			}
			y, yok, err := right.next(ctx)
			if err != nil {
				return 0, err
			}
			if !xok || !yok {
				if xok != yok {
					return xsltCollationResult(ctx, xsltBoolInt(xok)-xsltBoolInt(yok))
				}
				break
			}
			if x < y {
				return xsltCollationResult(ctx, -1)
			}
			if x > y {
				return xsltCollationResult(ctx, 1)
			}
		}
	}
	return xsltCollationResult(ctx, 0)
}

// xsltEnglishLocale implements xsltNewLocale's ASCII RFC-tag grammar and its
// territory -> bare language -> default territory search against the pinned
// locale inventory (C, C.utf8, en_US.utf8, POSIX). Thus en-GB also finds en_US,
// but en_US, en-US-extra, eng and other unavailable languages do not.
func xsltEnglishLocale(tag string) bool {
	i := 0
	for i < len(tag) && i < 8 && xsltLocaleAlpha(tag[i]) {
		i++
	}
	if i == 0 {
		return false
	}
	languageEnd := i
	if i < len(tag) && tag[i] != 0 {
		if tag[i] != '-' {
			return false
		}
		i++
		start := i
		for i < len(tag) && i-start < 8 && xsltLocaleAlpha(tag[i]) {
			i++
		}
		if i == start || (i < len(tag) && tag[i] != 0) {
			return false
		}
	}
	return languageEnd == 2 && tag[0]|32 == 'e' && tag[1]|32 == 'n'
}

func xsltLocaleAlpha(c byte) bool { return c|32 >= 'a' && c|32 <= 'z' }

func xsltBoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func xsltCollationResult(ctx context.Context, result int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return result, nil
}

func xsltCompareBytes(ctx context.Context, a, b string) (int, error) {
	for i := 0; ; i++ {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		var x, y byte
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return xsltCollationResult(ctx, -1)
		}
		if x > y {
			return xsltCollationResult(ctx, 1)
		}
		if x == 0 {
			return xsltCollationResult(ctx, 0)
		}
	}
}

// The iterator interprets complete collation elements, not individual Unicode
// scalars. Its only growing scratch is a long backward segment: remembering
// its elements is necessary to reverse contractions without quadratic rescans.
// Ordinary forward text and short backward segments need no dynamic scratch.
// Everything, including overflow storage, is private to one comparison.
type xsltCollationIterator struct {
	text                 string
	cursor, level, polls int
	weight, weightEnd    int
	prefix               [6]byte
	prefixAt, prefixEnd  int
	ignored              int
	backward             [32]uint32
	overflow             []uint32
	backwardCount        int
	pending              uint32
	hasPending           bool
}

func (it *xsltCollationIterator) reset(level int) {
	it.cursor, it.level, it.polls = 0, level, 0
	it.weight, it.weightEnd = 0, 0
	it.prefixAt, it.prefixEnd, it.ignored = 0, 0, 0
	it.backwardCount, it.hasPending = 0, false
}

func (it *xsltCollationIterator) next(ctx context.Context) (byte, bool, error) {
	for {
		if it.polls&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
		}
		it.polls++
		if it.prefixAt < it.prefixEnd {
			v := it.prefix[it.prefixAt]
			it.prefixAt++
			return v, true, nil
		}
		if it.weight < it.weightEnd {
			v := xsltCollationWeights[it.weight]
			it.weight++
			return v, true, nil
		}
		entry, ok, err := it.element(ctx)
		if err != nil || !ok {
			return 0, false, err
		}
		weight := int(entry & 0xffffff)
		for range it.level {
			weight += 1 + int(xsltCollationWeights[weight])
		}
		length := int(xsltCollationWeights[weight])
		if length == 0 {
			if it.level == 3 {
				if it.ignored == 0x7ffffffe {
					return 0, false, errors.New("XSLT collation position exceeds signed 32-bit range")
				}
				it.ignored++
			}
			continue
		}
		it.weight, it.weightEnd = weight+1, weight+1+length
		if it.level == 3 {
			// glibc's transformation encodes positions as historical UTF-8
			// integers (up to six bytes), not Unicode scalar values.
			it.prefixEnd = xsltCollationPosition(&it.prefix, uint32(it.ignored+1))
			it.prefixAt = 0
		}
		it.ignored = 0
	}
}

func (it *xsltCollationIterator) element(ctx context.Context) (uint32, bool, error) {
	if it.backwardCount != 0 {
		it.backwardCount--
		if it.backwardCount < len(it.backward) {
			return it.backward[it.backwardCount], true, nil
		}
		return it.overflow[it.backwardCount-len(it.backward)], true, nil
	}
	if it.hasPending {
		it.hasPending = false
		return it.pending, true, nil
	}
	for it.cursor < len(it.text) && it.text[it.cursor] != 0 {
		if it.polls&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
		}
		it.polls++
		entry, next, err := xsltCollationLookup(ctx, it.text, it.cursor)
		if err != nil {
			return 0, false, err
		}
		it.cursor = next
		rule := xsltCollationRules[int(entry>>24)*4+it.level]
		if rule&2 == 0 {
			if it.backwardCount == 0 {
				return entry, true, nil
			}
			it.pending, it.hasPending = entry, true
			break
		}
		n := it.backwardCount
		if n < len(it.backward) {
			it.backward[n] = entry
		} else if n-len(it.backward) < len(it.overflow) {
			it.overflow[n-len(it.backward)] = entry
		} else {
			it.overflow = append(it.overflow, entry)
		}
		it.backwardCount++
	}
	if it.backwardCount != 0 {
		return it.element(ctx)
	}
	return 0, false, nil
}

// The packed data consists of a 256-entry initial-byte table, aligned lists of
// exact/range tails, a range indirection table, and length-prefixed weights.
// Keeping the released byte representation preserves unspecified/unassigned
// characters and all multi-scalar contractions without inventing a UCA rule.
func xsltCollationLookup(ctx context.Context, text string, at int) (uint32, int, error) {
	entry := int32(xsltCollationWord(xsltCollationTable, int(text[at])*4))
	at++
	if entry >= 0 {
		return uint32(entry), at, nil
	}
	for offset, n := int(-entry), 0; ; n++ {
		if n&255 == 255 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		entry = int32(xsltCollationWord(xsltCollationExtra, offset))
		length := int(xsltCollationExtra[offset+4])
		tail := offset + 5
		if entry >= 0 {
			if at+length <= len(text) && text[at:at+length] == xsltCollationExtra[tail:tail+length] {
				return uint32(entry), at + length, nil
			}
			offset = (tail + length + 3) &^ 3
			continue
		}
		if at+length <= len(text) {
			value := text[at : at+length]
			lower := xsltCollationExtra[tail : tail+length]
			upper := xsltCollationExtra[tail+length : tail+2*length]
			if value >= lower && value <= upper {
				index := 0
				for i := range length {
					index = index*256 + int(value[i]) - int(lower[i])
				}
				return xsltCollationWord(xsltCollationIndirect, (int(-entry)+index)*4), at + length, nil
			}
		}
		offset = (tail + 2*length + 3) &^ 3
	}
}

func xsltCollationWord(data string, at int) uint32 {
	return uint32(data[at]) | uint32(data[at+1])<<8 | uint32(data[at+2])<<16 | uint32(data[at+3])<<24
}

func xsltCollationPosition(dst *[6]byte, value uint32) int {
	if value < 0x80 {
		dst[0] = byte(value)
		return 1
	}
	n := 2
	for n < 6 && value >= uint32(1)<<(5*n+1) {
		n++
	}
	for i := n - 1; i > 0; i-- {
		dst[i] = 0x80 | byte(value&0x3f)
		value >>= 6
	}
	dst[0] = byte(0xff<<uint(8-n)) | byte(value)
	return n
}
