// Package unicode16 implements the selected PostgreSQL18 normalization and case
// algorithms with Unicode16 data. PostgreSQL18's predicate quickcheck is
// intentional release behavior, not an input exception.
package unicode16

import (
	"context"
	"fmt"
)

type Form uint8

const (
	NFC Form = iota
	NFD
	NFKC
	NFKD
)

func ParseForm(s string) (Form, bool) {
	// PostgreSQL's form names use ASCII pg_strcasecmp, not Unicode folding.
	if (len(s) != 3 && len(s) != 4) || s[0]|0x20 != 'n' || s[1]|0x20 != 'f' {
		return 0, false
	}
	compatibility := len(s) == 4
	if compatibility && s[2]|0x20 != 'k' {
		return 0, false
	}
	switch s[len(s)-1] | 0x20 {
	case 'c':
		if compatibility {
			return NFKC, true
		}
		return NFC, true
	case 'd':
		if compatibility {
			return NFKD, true
		}
		return NFD, true
	}
	return 0, false
}

type entry struct {
	code          rune
	class         uint8
	offset        uint32
	length        uint8
	compatibility bool
}
type composition struct {
	key  uint64
	code rune
}
type property struct {
	lo, hi rune
	check  uint8
}

func lookup(r rune) *entry {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if entries[mid].code < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(entries) && entries[lo].code == r {
		return &entries[lo]
	}
	return nil
}
func class(r rune) uint8 {
	if e := lookup(r); e != nil {
		return e.class
	}
	return 0
}
func properties(f Form) []property {
	switch f {
	case NFC:
		return nfcProperties[:]
	case NFD:
		return nfdProperties[:]
	case NFKC:
		return nfkcProperties[:]
	default:
		return nfkdProperties[:]
	}
}
func quickProperty(r rune, p []property) uint8 {
	lo, hi := 0, len(p)
	for lo < hi {
		m := (lo + hi) / 2
		if p[m].hi < r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(p) && p[lo].lo <= r {
		return p[lo].check
	}
	return 0
}

// quickCheck returns YES=0, NO=1 or MAYBE=2.
func quickCheck(ctx context.Context, s string, f Form) (result uint8, count int, err error) {
	if err = ctx.Err(); err != nil {
		return 0, 0, err
	}
	p := properties(f)
	var previousClass uint8
	for _, r := range s {
		if count&1023 == 0 {
			if err = ctx.Err(); err != nil {
				return 0, count, err
			}
		}
		count++
		if r < 0x80 {
			previousClass = 0
			continue
		}
		c := class(r)
		if c != 0 && previousClass > c {
			result = 1
		}
		previousClass = c
		q := quickProperty(r, p)
		if q == 1 {
			result = 1
		} else if q == 2 && result == 0 {
			result = 2
		}
	}
	return result, count, nil
}
func IsNormalized(ctx context.Context, s string, f Form) (bool, error) {
	if f > NFKD {
		return false, fmt.Errorf("invalid normalization form")
	}
	q, count, err := quickCheck(ctx, s, f)
	if err != nil {
		return false, err
	}
	if q == 0 {
		return true, nil
	}
	if q == 1 {
		return false, nil
	}
	normalized, err := normalizeChecked(ctx, s, f, count)
	return normalized == s, err
}

type work struct {
	ctx   context.Context
	steps int
	runes []rune
}

func (w *work) tick() error {
	w.steps++
	if w.steps&1023 == 0 {
		return w.ctx.Err()
	}
	return nil
}
func (w *work) decompose(r rune, compat bool) error {
	if err := w.tick(); err != nil {
		return err
	}
	if r >= 0xAC00 && r < 0xD7A4 {
		s := r - 0xAC00
		w.runes = append(w.runes, 0x1100+s/(21*28), 0x1161+(s%(21*28))/28)
		if t := s % 28; t != 0 {
			w.runes = append(w.runes, 0x11A7+t)
		}
		return nil
	}
	e := lookup(r)
	if e == nil || e.length == 0 || e.compatibility && !compat {
		w.runes = append(w.runes, r)
		return nil
	}
	for _, d := range decompositionRunes[e.offset : e.offset+uint32(e.length)] {
		if err := w.decompose(d, compat); err != nil {
			return err
		}
	}
	return nil
}
func compose(a, b rune) (rune, bool) {
	if a >= 0x1100 && a < 0x1113 && b >= 0x1161 && b < 0x1176 {
		return 0xAC00 + ((a-0x1100)*21+b-0x1161)*28, true
	}
	// TBASE itself is not a trailing consonant (REL_18_0 consumed it with
	// tindex=0; 18.6 and Unicode leave it in place).
	if a >= 0xAC00 && a < 0xD7A4 && (a-0xAC00)%28 == 0 && b > 0x11A7 && b < 0x11C3 {
		return a + b - 0x11A7, true
	}
	key := uint64(a)<<32 | uint64(b)
	lo, hi := 0, len(compositions)
	for lo < hi {
		m := (lo + hi) / 2
		if compositions[m].key < key {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(compositions) && compositions[lo].key == key {
		return compositions[lo].code, true
	}
	return 0, false
}
func Normalize(ctx context.Context, s string, f Form) (string, error) {
	if f > NFKD {
		return "", fmt.Errorf("invalid normalization form")
	}
	q, count, err := quickCheck(ctx, s, f)
	if err != nil {
		return "", err
	}
	if q == 0 {
		return s, nil
	}
	return normalizeChecked(ctx, s, f, count)
}
func normalizeChecked(ctx context.Context, s string, f Form, count int) (string, error) {
	var err error
	w := work{ctx: ctx, runes: make([]rune, 0, count)}
	for _, r := range s {
		if err = w.decompose(r, f == NFKC || f == NFKD); err != nil {
			return "", err
		}
	}
	// Stable counting order within each nonstarter segment avoids quadratic
	// insertion sorting for unbounded combining-mark runs. Scratch is allocated
	// only for disordered segments and reused throughout this normalization.
	var scratch []rune
	for start := 0; start < len(w.runes); {
		end := start
		var last uint8
		ordered := true
		for end < len(w.runes) {
			c := class(w.runes[end])
			if c == 0 && end > start {
				break
			}
			if c < last && c != 0 {
				ordered = false
			}
			last = c
			end++
			if err = w.tick(); err != nil {
				return "", err
			}
		}
		first := start
		if class(w.runes[first]) == 0 {
			first++
		}
		if !ordered {
			n := end - first
			if cap(scratch) < n {
				scratch = make([]rune, n)
			} else {
				scratch = scratch[:n]
			}
			var offsets [256]int
			for _, r := range w.runes[first:end] {
				offsets[class(r)]++
				if err = w.tick(); err != nil {
					return "", err
				}
			}
			total := 0
			for i, count := range offsets {
				offsets[i] = total
				total += count
			}
			for _, r := range w.runes[first:end] {
				c := class(r)
				scratch[offsets[c]] = r
				offsets[c]++
				if err = w.tick(); err != nil {
					return "", err
				}
			}
			copy(w.runes[first:end], scratch)
		}
		start = end
	}
	if f == NFC || f == NFKC {
		n := 0
		starter := -1
		var lastClass uint8
		for _, r := range w.runes {
			if err = w.tick(); err != nil {
				return "", err
			}
			c := class(r)
			if starter >= 0 && (lastClass == 0 || lastClass < c) {
				if combined, ok := compose(w.runes[starter], r); ok {
					w.runes[starter] = combined
					continue
				}
			}
			w.runes[n] = r
			if c == 0 {
				starter = n
			}
			n++
			lastClass = c
		}
		w.runes = w.runes[:n]
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	return string(w.runes), nil
}
