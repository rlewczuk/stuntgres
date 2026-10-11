package sqltype

import (
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

func (p *PGRegexProgram) literalEqual(pattern, source rune) bool {
	if !p.options.CaseInsensitive {
		return pattern == source
	}
	mode := p.collation.CaseMode()
	return source == unicode16.SimpleCase(pattern, unicode16.Lower, mode) || source == unicode16.SimpleCase(pattern, unicode16.Upper, mode)
}
func (p *PGRegexProgram) backrefEqual(captured, source rune) bool {
	if captured == source {
		return true
	}
	if !p.options.CaseInsensitive {
		return false
	}
	mode := p.collation.CaseMode()
	return unicode16.SimpleCase(captured, unicode16.Lower, mode) == unicode16.SimpleCase(source, unicode16.Lower, mode)
}
func (p *PGRegexProgram) matchCharacter(w *pgRegexWork, i pgRegexInst, r rune) (bool, error) {
	switch i.op {
	case pgRegexLiteral:
		return p.literalEqual(i.value, r), nil
	case pgRegexAny:
		return !p.newlineStop || r != '\n', nil
	case pgRegexClass:
		return p.matchesClass(w, i, r)
	}
	return false, nil
}

func (p *PGRegexProgram) matchBackreference(w *pgRegexWork, source string, start, end, pos int) (bool, int, int, error) {
	if start < 0 || end < start {
		return false, pos, 0, nil
	}
	count := 0
	for at := start; at < end; {
		if err := w.step(); err != nil {
			return false, pos, count, err
		}
		if pos == len(source) {
			return false, pos, count, nil
		}
		a, an := utf8.DecodeRuneInString(source[at:end])
		b, bn := utf8.DecodeRuneInString(source[pos:])
		if !p.backrefEqual(a, b) {
			return false, pos, count, nil
		}
		at += an
		pos += bn
		count++
	}
	return true, pos, count, nil
}
