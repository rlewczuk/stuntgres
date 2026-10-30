package functions

// The ispell reader and normalization traversal follow PostgreSQL REL_18_6
// src/backend/tsearch/spell.c and regis.c.
// Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// PostgreSQL License: https://www.postgresql.org/about/licence/

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype/unicode16"
)

const (
	ispellBegin uint8 = 1 << iota
	ispellMiddle
	ispellEnd
	ispellOnly
	ispellPermit
	ispellForbid
	ispellCross
	ispellAll      = ispellBegin | ispellMiddle | ispellEnd
	ispellMaxWord  = 256
	ispellMaxForms = 1023 // spell.c MAX_NORM (1024), with one terminator.
)

type ispellWord struct {
	text, flags string
	flagID      int
}
type ispellNode struct {
	children    map[byte]*ispellNode
	word        bool
	flags       map[string]bool
	flagError   error // Deferred malformed flag, when release has not demanded it at init.
	compound    uint8
}
type ispellAffixNode struct {
	children map[byte]*ispellAffixNode
	rules    []*ispellAffix
}
type ispellCompoundAffix struct {
	text   string
	suffix bool
}

// IspellDictionary owns only compiled immutable data. Matcher/traversal scratch
// is allocated by each Lexize, never shared by dictionaries or statements.
type IspellDictionary struct {
	dictfile, afffile, stopwords string
	root                         ispellNode
	prefix, suffix               ispellAffixNode
	compoundAffixes              []ispellCompoundAffix
	useCompound                  bool
	stops                        []string
}

// IspellCompiler belongs to one detached initialization. Finish consumes it;
// no catalog, session, callback or input token enters the published runtime.
type IspellCompiler struct {
	files      *DictionaryFiles
	d          *IspellDictionary
	words      []ispellWord
	affixes    []*ispellAffix
	mode       string
	aliases    []string
	aliasCount int
	compound   map[string]uint8
}

func NewIspellCompiler(files *DictionaryFiles) *IspellCompiler {
	return &IspellCompiler{files: files, d: &IspellDictionary{}, compound: make(map[string]uint8)}
}
func ispellLower(ctx context.Context, s string) (string, error) {
	return unicode16.ConvertCase(ctx, s, unicode16.Lower, unicode16.ASCII)
}
func ispellSpace(c byte) bool { return strings.ContainsRune(" \t\r\n\v\f", rune(c)) }
func (b *IspellCompiler) LoadDictionary(ctx context.Context, name string) error {
	data, err := b.files.resource(ctx, name, "dict", "dictionary")
	if err != nil {
		return err
	}
	b.d.dictfile = name
	return readDictionaryLines(ctx, name+".dict", data, func(line string) error {
		word, flags, _ := strings.Cut(line, "/")
		for i := range len(flags) {
			if flags[i] < 33 || flags[i] > 126 {
				flags = flags[:i]
				break
			}
		}
		if i := strings.IndexAny(word, " \t\r\n\v\f"); i >= 0 {
			word = word[:i]
		}
		word, err = ispellLower(ctx, word)
		if err == nil {
			b.words = append(b.words, ispellWord{text: word, flags: flags})
		}
		return err
	})
}
func (b *IspellCompiler) LoadStopwords(ctx context.Context, name string) error {
	b.d.stopwords = name
	if name == "" {
		return ctx.Err()
	}
	data, err := b.files.resource(ctx, name, "stop", "stop-word")
	if err != nil {
		return err
	}
	var words []string
	err = readDictionaryLines(ctx, name+".stop", data, func(line string) error {
		if i := strings.IndexAny(line, " \t\r\n\v\f"); i >= 0 {
			line = line[:i]
		}
		if line == "" {
			return nil
		}
		word, err := ispellLower(ctx, line)
		if err == nil {
			words = append(words, word)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := ispellSort(ctx, words, func(a, b string) int { return ispellCompare(ctx, a, b) }); err != nil {
		return err
	}
	b.d.stops = words
	return ctx.Err()
}

// ispellNumber preserves the released LP64 decimal-prefix conversion: checked
// signed long first, then signed32 before each caller's distinct range checks.
func ispellNumber(s string) (int64, error) {
	i := 0
	for i < len(s) && ispellSpace(s[i]) {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digits {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.ParseInt(s[start:i], 10, 64)
	return int64(int32(n)), err
}
func (b *IspellCompiler) compoundFlag(s string) (string, error) {
	if b.mode != "num" {
		return s, nil
	}
	n, err := ispellNumber(s)
	if err != nil {
		return "", dictionaryError("F0000", fmt.Sprintf("invalid affix flag \"%s\"", s))
	}
	if n < 0 || n > 65536 {
		return "", dictionaryError("F0000", fmt.Sprintf("affix flag \"%s\" is out of range", s))
	}
	return strconv.FormatInt(n, 10), nil
}
func (b *IspellCompiler) flags(ctx context.Context, s string) ([]string, error) {
	var out []string
	for len(s) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b.mode == "num" {
			n, err := ispellNumber(s)
			if err != nil {
				return out, dictionaryError("F0000", fmt.Sprintf("invalid affix flag \"%s\"", s))
			}
			if n < 0 || n > 65536 {
				return out, dictionaryError("F0000", fmt.Sprintf("affix flag \"%s\" is out of range", s))
			}
			i := 0
			for i < len(s) && ispellSpace(s[i]) {
				i++
			}
			if i < len(s) && (s[i] == '+' || s[i] == '-') {
				i++
			}
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			comma := false
			for i < len(s) {
				if s[i] >= '0' && s[i] <= '9' {
					if !comma {
						return out, dictionaryError("F0000", fmt.Sprintf("invalid affix flag \"%s\"", s[i:]))
					}
					break
				}
				if s[i] == ',' {
					if comma {
						return out, dictionaryError("F0000", fmt.Sprintf("invalid affix flag \"%s\"", s[i:]))
					}
					comma = true
				} else if !ispellSpace(s[i]) {
					return out, dictionaryError("F0000", fmt.Sprintf("invalid character in affix flag \"%s\"", s[i:]))
				}
				i++
			}
			out = append(out, strconv.FormatInt(n, 10))
			s = s[i:]
		} else {
			_, n := utf8.DecodeRuneInString(s)
			if b.mode == "long" {
				if n == len(s) {
					return out, dictionaryError("F0000", fmt.Sprintf("invalid affix flag \"%s\" with \"long\" flag value", s))
				}
				_, width := utf8.DecodeRuneInString(s[n:])
				n += width
			}
			out = append(out, s[:n])
			s = s[n:]
		}
	}
	return out, nil
}
func (b *IspellCompiler) alias(s string) (string, error) {
	if b.aliases == nil || s == "" {
		return s, nil
	}
	n, err := ispellNumber(s)
	total := int64(b.aliasCount) + 1
	if err != nil || n > total || n > 0 && n < total && n >= int64(len(b.aliases)) {
		return "", dictionaryError("F0000", fmt.Sprintf("invalid affix alias \"%s\"", s))
	}
	if n > 0 && n < total {
		return b.aliases[n], nil
	}
	return "", nil
}
func (b *IspellCompiler) dictionaryAliasID(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := ispellNumber(s)
	i := 0
	for i < len(s) && ispellSpace(s[i]) {
		i++
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if err != nil || n < 0 || n >= int64(len(b.aliases)) || i < len(s) && !ispellSpace(s[i]) {
		return 0, dictionaryError("F0000", fmt.Sprintf("invalid affix alias \"%s\"", s))
	}
	return int(n), nil
}
func (b *IspellCompiler) flagBits(ctx context.Context, s string) (uint8, error) {
	if len(b.compound) == 0 {
		return 0, ctx.Err()
	}
	flags, err := b.flags(ctx, s)
	var bits uint8
	for _, flag := range flags {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		bits |= b.compound[flag]
	}
	return bits, err
}
func (b *IspellCompiler) Finish(ctx context.Context) (*IspellDictionary, error) {
	d := b.d
	vectors, err := b.flagVectors(ctx)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(b.words); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		word := b.words[start]
		end := start + 1
		for end < len(b.words) && ispellCompare(ctx, word.text, b.words[end].text) == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			end++
		}
		if word.text == "" {
			start = end
			continue
		}
		node := &d.root
		for i := range len(word.text) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if node.children == nil {
				node.children = make(map[byte]*ispellNode)
			}
			child := node.children[word.text[i]]
			if child == nil {
				child = &ispellNode{}
				node.children[word.text[i]] = child
			}
			node = child
		}
		aggregate := word.flagID
		bits, err := vectors.compound(ctx, aggregate)
		if err != nil {
			return nil, err
		}
		node.compound = bits
		if bits&ispellOnly != 0 && bits&ispellAll == 0 {
			node.compound |= ispellAll
		}
		for i := start + 1; i < end; i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			incoming := b.words[i].flagID
			clearOnly := false
			if aggregate != incoming {
				incomingBits, err := vectors.compound(ctx, incoming)
				if err != nil {
					return nil, err
				}
				clearOnly = node.compound&incomingBits&ispellOnly == 0
				aggregate, err = vectors.merge(ctx, aggregate, incoming)
				if err != nil {
					return nil, err
				}
			}
			bits, err = vectors.compound(ctx, aggregate)
			if err != nil {
				return nil, err
			}
			node.compound = bits
			if bits&ispellOnly != 0 && bits&ispellAll == 0 {
				node.compound |= ispellAll
			}
			if clearOnly {
				node.compound &^= ispellOnly
			}
		}
		value, err := vectors.decode(ctx, aggregate)
		if err != nil {
			return nil, err
		}
		if len(value.flags) > 0 {
			node.flags = make(map[string]bool, len(value.flags))
			for _, flag := range value.flags {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				node.flags[flag] = true
			}
		}
		node.flagError = value.err
		node.word = true
		start = end
	}
	// PostgreSQL's affix indexes visit zero-length additions first, then
	// increasing prefix/reversed-suffix depth; ties preserve rule order.
	if err := ispellSort(ctx, b.affixes, func(a, c *ispellAffix) int {
		if a.suffix != c.suffix {
			if a.suffix {
				return 1
			}
			return -1
		}
		if a.suffix {
			return ispellReverseCompare(a.add, c.add)
		}
		return strings.Compare(a.add, c.add)
	}); err != nil {
		return nil, err
	}
	for _, affix := range b.affixes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root := &d.prefix
		if affix.suffix {
			root = &d.suffix
		}
		for i := range len(affix.add) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			at := i
			if affix.suffix {
				at = len(affix.add) - 1 - i
			}
			if root.children == nil {
				root.children = make(map[byte]*ispellAffixNode)
			}
			child := root.children[affix.add[at]]
			if child == nil {
				child = &ispellAffixNode{}
				root.children[affix.add[at]] = child
			}
			root = child
		}
		root.rules = append(root.rules, affix)
		if affix.bits&ispellAll != 0 && affix.add != "" {
			used, err := vectors.inUse(ctx, affix.flag)
			if err != nil {
				return nil, err
			}
			if !used {
				continue
			}
			if n := len(d.compoundAffixes); n > 0 {
				prev := d.compoundAffixes[n-1]
				if prev.suffix == affix.suffix && strings.HasSuffix(affix.add, prev.text) {
					continue
				}
			}
			d.compoundAffixes = append(d.compoundAffixes, ispellCompoundAffix{affix.add, affix.suffix})
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.d = nil // ownership transfer; the initialization builder is consumed.
	return d, nil
}
func ispellReverseCompare(a, b string) int {
	for i := range min(len(a), len(b)) {
		if c := int(a[len(a)-1-i]) - int(b[len(b)-1-i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}
func (d *IspellDictionary) Configured(dictfile, afffile, stopwords string) bool {
	return d != nil && d.dictfile == dictfile && d.afffile == afffile && d.stopwords == stopwords
}
func (*IspellDictionary) Filtering() bool { return false }
func (d *IspellDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	word, err := ispellLower(ctx, input)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	if word == "" {
		return fts.LexicalResult{}, ctx.Err()
	}
	run := ispellNormalization{ctx: ctx, d: d}
	forms, err := run.normalize(word)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	if len(forms) == 0 {
		return fts.LexicalResult{}, ctx.Err()
	}
	kept := forms[:0]
	for _, form := range forms {
		if err := ctx.Err(); err != nil {
			return fts.LexicalResult{}, err
		}
		if _, stop := slices.BinarySearch(d.stops, form.Text); !stop {
			kept = append(kept, form)
		}
	}
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	return fts.LexicalResult{Recognized: true, Lexemes: kept}, nil
}
