package functions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype/unicode16"
)

// xsyn_sample.rules is verbatim PostgreSQL REL_18_6:
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/contrib/dict_xsyn/xsyn_sample.rules
// Portions Copyright (c) 2007-2025, PostgreSQL Global Development Group.
// PostgreSQL License: https://www.postgresql.org/about/licence/

type xsynEntry struct {
	input string
	group int
}

// XsynDictionary owns compiled immutable groups and a whole-token lookup table.
// Result slices belong to the invocation; only immutable strings are shared.
type XsynDictionary struct {
	rulesPresent                                     bool
	resource                                         string
	matchorig, matchsynonyms, keeporig, keepsynonyms bool
	groups                                           [][]fts.Lexeme
	entries                                          []xsynEntry
}

func CompileXsynDictionary(ctx context.Context, rulesPresent bool, basename string, matchorig, matchsynonyms, keeporig, keepsynonyms bool) (*XsynDictionary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &XsynDictionary{rulesPresent: rulesPresent, resource: basename, matchorig: matchorig, matchsynonyms: matchsynonyms, keeporig: keeporig, keepsynonyms: keepsynonyms}
	if !rulesPresent {
		return d, nil
	}
	if err := CheckDictionaryResourceName(ctx, basename); err != nil {
		return nil, err
	}
	path := basename + ".rules"
	data, err := dictionaryResources.ReadFile(path)
	if err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		reason := err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			reason = "No such file or directory"
		}
		return nil, dictionaryError("F0000", fmt.Sprintf(`could not open synonym file "%s": %s`, path, reason))
	}
	if !matchorig && !matchsynonyms {
		return d, ctx.Err()
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		value, err := unicode16.ConvertCase(ctx, line, unicode16.Lower, unicode16.ASCII)
		if err != nil {
			return nil, err
		}
		group := len(d.groups)
		var output []fts.Lexeme
		// Keep pos separate from the word start: the release tests pos ==
		// value, not key == value, including lines with leading whitespace.
		for pos := 0; pos < len(value); {
			start, end, err := synonymWord(ctx, value, pos)
			if err != nil {
				return nil, err
			}
			if start == end || value[start] == '#' {
				break
			}
			word := value[start:end]
			if (pos == 0 && matchorig) || (pos != 0 && matchsynonyms) {
				d.entries = append(d.entries, xsynEntry{input: word, group: group})
			}
			if (pos == 0 && keeporig) || (pos != 0 && keepsynonyms) {
				output = append(output, fts.Lexeme{Text: word})
			}
			pos = end
		}
		if len(d.entries) != 0 && d.entries[len(d.entries)-1].group == group {
			d.groups = append(d.groups, output)
		}
	}
	if err := sortXsyn(ctx, d.entries); err != nil {
		return nil, err
	}
	return d, nil
}

// In-place heapsort follows the context-aware synonym table owner, with no
// scratch table or panic escape from a comparison callback.
func sortXsyn(ctx context.Context, entries []xsynEntry) error {
	sift := func(root, end int) error {
		for root < end/2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := root*2 + 1
			if child+1 < end && entries[child].input < entries[child+1].input {
				child++
			}
			if entries[root].input >= entries[child].input {
				break
			}
			entries[root], entries[child] = entries[child], entries[root]
			root = child
		}
		return nil
	}
	for root := len(entries)/2 - 1; root >= 0; root-- {
		if err := sift(root, len(entries)); err != nil {
			return err
		}
	}
	for end := len(entries) - 1; end > 0; end-- {
		entries[0], entries[end] = entries[end], entries[0]
		if err := sift(0, end); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (d *XsynDictionary) Configured(rulesPresent bool, basename string, matchorig, matchsynonyms, keeporig, keepsynonyms bool) bool {
	return d != nil && d.rulesPresent == rulesPresent && d.resource == basename && d.matchorig == matchorig && d.matchsynonyms == matchsynonyms && d.keeporig == keeporig && d.keepsynonyms == keepsynonyms
}

func (*XsynDictionary) Filtering() bool { return false }

func (d *XsynDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	if input == "" || len(d.entries) == 0 {
		return fts.LexicalResult{}, nil
	}
	input, err := unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	low, high := 0, len(d.entries)
	for low < high {
		if err := ctx.Err(); err != nil {
			return fts.LexicalResult{}, err
		}
		mid := low + (high-low)/2
		switch strings.Compare(input, d.entries[mid].input) {
		case -1:
			high = mid
		case 1:
			low = mid + 1
		default:
			return fts.LexicalResult{Recognized: true, Lexemes: slices.Clone(d.groups[d.entries[mid].group])}, ctx.Err()
		}
	}
	return fts.LexicalResult{}, ctx.Err()
}
