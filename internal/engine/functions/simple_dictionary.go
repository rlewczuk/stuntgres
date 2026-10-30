package functions

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
)

// PostgreSQL REL_18_6 src/backend/snowball/stopwords/{english,danish,dutch,german}.stop.
// https://github.com/postgres/postgres/tree/REL_18_6/src/backend/snowball/stopwords
// Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// PostgreSQL License: https://www.postgresql.org/about/licence/
//
// thesaurus_sample.ths is verbatim PostgreSQL REL_18_6
// src/backend/tsearch/dicts/thesaurus_sample.ths, under the same license above.
// stuntgres_thes_*.ths are project-owned deterministic dictionary resources.
//
//go:embed english.stop danish.stop dutch.stop german.stop unaccent.rules synonym_sample.syn *.ths
var dictionaryResources embed.FS

// Each exact resource has immutable backing shared by simple and Snowball.
// This dedicated loader mutex is never acquired under catalog locks.
var dictionaryStopwords struct {
	sync.Mutex
	words map[string][]string
}

// SimpleDictionary is an immutable whole-token normalizer, not a tokenizer.
type SimpleDictionary struct {
	accept    bool
	stopwords string
	words     []string
}

// DefaultSimpleDictionary is the immutable resource-free bootstrap state.
var DefaultSimpleDictionary = &SimpleDictionary{accept: true}

func dictionaryError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

// CheckDictionaryResourceName checks PostgreSQL's text-search resource basename.
func CheckDictionaryResourceName(ctx context.Context, name string) error {
	for i := range len(name) {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return dictionaryError("22023", fmt.Sprintf(`invalid text search configuration file name "%s"`, name))
		}
	}
	return ctx.Err()
}

func CompileUnaccentDictionary(ctx context.Context, name string) (*UnaccentMatcher, error) {
	if err := CheckDictionaryResourceName(ctx, name); err != nil {
		return nil, err
	}
	if name == "unaccent" {
		return StockUnaccent, nil
	}
	path := name + ".rules"
	_, err := dictionaryResources.ReadFile(path)
	if err == nil {
		return nil, fmt.Errorf("stuntgres: uncompiled dictionary resource %s", path)
	}
	reason := err.Error()
	if errors.Is(err, fs.ErrNotExist) {
		reason = "No such file or directory"
	}
	return nil, dictionaryError("F0000", fmt.Sprintf(`could not open unaccent file "%s": %s`, path, reason))
}

func CompileSimpleDictionary(ctx context.Context, accept bool, stopwords string) (*SimpleDictionary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	words, err := loadDictionaryStopwords(ctx, stopwords)
	if err != nil {
		return nil, err
	}
	return &SimpleDictionary{accept: accept, stopwords: stopwords, words: words}, nil
}

// CheckDictionaryStopwords loads and validates a resource in source-option order.
func CheckDictionaryStopwords(ctx context.Context, name string) error {
	_, err := loadDictionaryStopwords(ctx, name)
	return err
}

func loadDictionaryStopwords(ctx context.Context, stopwords string) ([]string, error) {
	if stopwords == "" {
		return nil, ctx.Err()
	}
	if err := CheckDictionaryResourceName(ctx, stopwords); err != nil {
		return nil, err
	}
	path := stopwords + ".stop"
	dictionaryStopwords.Lock()
	defer dictionaryStopwords.Unlock()
	words, cached := dictionaryStopwords.words[stopwords]
	if !cached {
		data, err := dictionaryResources.ReadFile(path)
		if err != nil {
			reason := err.Error()
			if errors.Is(err, fs.ErrNotExist) {
				reason = "No such file or directory"
			}
			return nil, dictionaryError("F0000", fmt.Sprintf(`could not open stop-word file "%s": %s`, path, reason))
		}
		compiled, err := compileStopwords(ctx, string(data))
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if dictionaryStopwords.words == nil {
			dictionaryStopwords.words = make(map[string][]string)
		}
		dictionaryStopwords.words[stopwords] = compiled
		words = compiled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return words, nil
}

func (d *SimpleDictionary) Configured(accept bool, stopwords string) bool {
	return d != nil && d.accept == accept && d.stopwords == stopwords
}

func compileStopwords(ctx context.Context, data string) ([]string, error) {
	words := make([]string, 0, strings.Count(data, "\n"))
	for line := range strings.SplitSeq(data, "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := strings.IndexAny(line, " \t\n\r\v\f")
		if end >= 0 {
			line = line[:end]
		}
		if line == "" {
			continue
		}
		word, err := unicode16.ConvertCase(ctx, line, unicode16.Lower, unicode16.ASCII)
		if err != nil {
			return nil, err
		}
		words = append(words, word)
	}
	slices.Sort(words)
	return words, ctx.Err()
}

func (*SimpleDictionary) Filtering() bool { return false }
func (*UnaccentMatcher) Filtering() bool  { return true }

func (d *SimpleDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	token, err := unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	_, stopped := slices.BinarySearch(d.words, token)
	if token == "" || stopped {
		return fts.LexicalResult{Recognized: true}, ctx.Err()
	}
	if !d.accept {
		return fts.LexicalResult{}, ctx.Err()
	}
	return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{{Text: token}}}, ctx.Err()
}

// Lexize retains the filtering provider's recognized/unrecognized distinction.
func (p *UnaccentMatcher) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	token, changed, err := p.lexize(ctx, p.init(), input)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	if !changed {
		return fts.LexicalResult{}, ctx.Err()
	}
	return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{{Text: token}}}, ctx.Err()
}
