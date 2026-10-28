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

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/unicode16"
)

// PostgreSQL REL_18_6 src/backend/snowball/stopwords/english.stop.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/snowball/stopwords/english.stop
// Portions Copyright (c) 1996-2026, PostgreSQL Global Development Group.
// Portions Copyright (c) 1994, Regents of the University of California.
// PostgreSQL License: https://www.postgresql.org/about/licence/
//
//go:embed english.stop unaccent.rules
var dictionaryResources embed.FS

var englishWords struct {
	sync.Mutex
	words []string
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
	englishWords.Lock()
	defer englishWords.Unlock()
	if englishWords.words == nil || stopwords != "english" {
		data, err := dictionaryResources.ReadFile(path)
		if err != nil {
			reason := err.Error()
			if errors.Is(err, fs.ErrNotExist) {
				reason = "No such file or directory"
			}
			return nil, dictionaryError("F0000", fmt.Sprintf(`could not open stop-word file "%s": %s`, path, reason))
		}
		words, err := compileStopwords(ctx, string(data))
		if err != nil {
			return nil, err
		}
		englishWords.words = words
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return englishWords.words, nil
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

func lexemeArray(ctx context.Context, result *sqltype.Descriptor, token string, empty bool) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if result == nil || result.ID() != 1009 || !result.IsArray() || result.Element().ID() != sqltype.Text {
		return sqltype.Value{}, fmt.Errorf("invalid dictionary result descriptor")
	}
	if empty {
		return sqltype.OwnedArrayValue(result, nil, nil)
	}
	return sqltype.OwnedArrayValue(result, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.String(result.Element(), token)})
}

func (*SimpleDictionary) Filtering() bool { return false }
func (*UnaccentMatcher) Filtering() bool  { return true }

func (d *SimpleDictionary) Lexize(ctx context.Context, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	token, err := unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return sqltype.Value{}, err
	}
	_, stopped := slices.BinarySearch(d.words, token)
	if token == "" || stopped {
		return lexemeArray(ctx, result, "", true)
	}
	if !d.accept {
		return sqltype.Null(result), ctx.Err()
	}
	return lexemeArray(ctx, result, token, false)
}

// Lexize applies the genuine unaccent dictionary's recognized/unrecognized rule.
func (p *UnaccentMatcher) Lexize(ctx context.Context, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	token, changed, err := p.lexize(ctx, p.init(), input)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !changed {
		return sqltype.Null(result), ctx.Err()
	}
	return lexemeArray(ctx, result, token, false)
}
