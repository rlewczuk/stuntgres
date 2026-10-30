package functions

import (
	"context"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ispellNormalization struct {
	ctx      context.Context
	d        *IspellDictionary
	matchers map[*sqltype.PGRegexProgram]*sqltype.PGRegexProgram
}

func (d *IspellDictionary) find(word, flag string, compound uint8) (bool, error) {
	node := &d.root
	for i := range len(word) {
		node = node.children[word[i]]
		if node == nil {
			return false, nil
		}
	}
	if !node.word || word == "" {
		return false, nil
	}
	if compound == 0 {
		if node.compound&ispellOnly != 0 {
			return false, nil
		}
	} else if node.compound&compound == 0 {
		return false, nil
	}
	if flag == "" || node.flags[flag] {
		return true, nil
	}
	if diagnostic, ok := node.flagError.(*sqlstate.Error); ok {
		owned := *diagnostic
		return false, &owned
	}
	return false, node.flagError
}
func (r *ispellNormalization) condition(a *ispellAffix, word string) (bool, error) {
	if err := r.ctx.Err(); err != nil {
		return false, err
	}
	if a.program != nil {
		if r.matchers == nil {
			r.matchers = make(map[*sqltype.PGRegexProgram]*sqltype.PGRegexProgram)
		}
		matcher := r.matchers[a.program]
		if matcher == nil {
			matcher = a.program.NewMatcher()
			r.matchers[a.program] = matcher
		}
		match, err := matcher.Match(r.ctx, word, false)
		return match.Matched, err
	}
	if len(a.mask) == 0 {
		return true, nil
	}
	if a.suffix {
		n := utf8.RuneCountInString(word) - len(a.mask)
		if n < 0 {
			return false, nil
		}
		for range n {
			_, size := utf8.DecodeRuneInString(word)
			word = word[size:]
		}
	}
	for _, mask := range a.mask {
		if word == "" {
			return false, nil
		}
		c, n := utf8.DecodeRuneInString(word)
		if strings.ContainsRune(mask.letters, c) == mask.negate {
			return false, nil
		}
		word = word[n:]
	}
	return true, nil
}
func (r *ispellNormalization) apply(word string, a *ispellAffix, compound uint8, base *int) (string, bool, error) {
	if err := r.ctx.Err(); err != nil {
		return "", false, err
	}
	switch {
	case compound == 0:
		if a.bits&ispellOnly != 0 {
			return "", false, nil
		}
	case compound&ispellBegin != 0:
		if a.bits&ispellForbid != 0 || a.suffix && a.bits&ispellBegin == 0 {
			return "", false, nil
		}
	case compound&ispellMiddle != 0:
		if a.bits&ispellForbid != 0 || a.bits&ispellMiddle == 0 {
			return "", false, nil
		}
	case compound&ispellEnd != 0:
		if a.bits&ispellForbid != 0 || !a.suffix && a.bits&ispellEnd == 0 {
			return "", false, nil
		}
	}
	if len(word) < len(a.add) {
		return "", false, nil
	}
	keep := len(word) - len(a.add)
	if keep+len(a.strip) >= 2*ispellMaxWord {
		return "", false, nil
	}
	var candidate string
	if a.suffix {
		candidate = word[:keep] + a.strip
		if base != nil {
			*base = keep
		}
	} else {
		if base != nil && *base+len(a.strip) <= len(a.add) {
			return "", false, nil
		}
		candidate = a.strip + word[len(a.add):]
	}
	yes, err := r.condition(a, candidate)
	return candidate, yes, err
}
func ispellVisitAffixes(root *ispellAffixNode, word string, suffix bool, visit func(*ispellAffix) error) error {
	for level := 0; root != nil; level++ {
		for _, affix := range root.rules {
			if err := visit(affix); err != nil {
				return err
			}
		}
		if level == len(word) {
			break
		}
		at := level
		if suffix {
			at = len(word) - 1 - level
		}
		root = root.children[word[at]]
	}
	return nil
}
func (r *ispellNormalization) subword(word string, compound uint8) ([]string, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if len(word) > ispellMaxWord {
		return nil, nil
	}
	var forms []string
	add := func(form string) {
		if len(forms) < ispellMaxForms && (len(forms) == 0 || forms[len(forms)-1] != form) {
			forms = append(forms, form)
		}
	}
	if found, err := r.d.find(word, "", compound); err != nil {
		return nil, err
	} else if found {
		forms = append(forms, word)
	}
	accept := func(candidate, flag string) error {
		found, err := r.d.find(candidate, flag, compound)
		if found {
			add(candidate)
		}
		return err
	}
	err := ispellVisitAffixes(&r.d.prefix, word, false, func(a *ispellAffix) error {
		candidate, yes, err := r.apply(word, a, compound, nil)
		if err == nil && yes {
			return accept(candidate, a.flag)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	err = ispellVisitAffixes(&r.d.suffix, word, true, func(suffix *ispellAffix) error {
		base := 0
		candidate, yes, err := r.apply(word, suffix, compound, &base)
		if err != nil || !yes {
			return err
		}
		if err := accept(candidate, suffix.flag); err != nil {
			return err
		}
		return ispellVisitAffixes(&r.d.prefix, candidate, false, func(prefix *ispellAffix) error {
			form, yes, err := r.apply(candidate, prefix, compound, &base)
			flag := prefix.flag
			if prefix.bits&suffix.bits&ispellCross != 0 {
				flag = ""
			}
			if err == nil && yes {
				return accept(form, flag)
			}
			return err
		})
	})
	return forms, err
}
func (r *ispellNormalization) normalize(word string) ([]fts.Lexeme, error) {
	standalone, err := r.subword(word, 0)
	if err != nil {
		return nil, err
	}
	var forms []fts.Lexeme
	variant := uint16(1)
	for _, form := range standalone {
		forms = append(forms, fts.Lexeme{Text: form, Variant: variant})
		variant++
	}
	if !r.d.useCompound {
		return forms, r.ctx.Err()
	}
	variants, err := r.split(word)
	if err != nil {
		return nil, err
	}
	for v := variants; v != nil; v = v.next {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if len(v.stems) < 2 {
			continue
		}
		last, err := r.subword(v.stems[len(v.stems)-1], ispellEnd)
		if err != nil {
			return nil, err
		}
		for _, ending := range last {
			// PostgreSQL NINormalizeWord assigns one variant to every stem
			// of this decomposition, then advances for the next alternative.
			for _, stem := range v.stems[:len(v.stems)-1] {
				if err := r.ctx.Err(); err != nil {
					return nil, err
				}
				if len(forms) < ispellMaxForms {
					forms = append(forms, fts.Lexeme{Text: stem, Variant: variant})
				}
			}
			if len(forms) < ispellMaxForms {
				forms = append(forms, fts.Lexeme{Text: ending, Variant: variant})
			}
			variant++
		}
	}
	return forms, r.ctx.Err()
}
