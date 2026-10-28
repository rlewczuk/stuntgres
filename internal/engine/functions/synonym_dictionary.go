package functions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/unicode16"
)

// synonym_sample.syn is the verbatim PostgreSQL REL_18_6 resource:
// https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/backend/tsearch/dicts/synonym_sample.syn
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group.
// PostgreSQL License: https://www.postgresql.org/about/licence/

type synonymEntry struct {
	input  string
	lexeme fts.Lexeme
}

// SynonymDictionary owns immutable, sorted whole-token definitions. It neither
// tokenizes its input nor filters an unrecognized token out of a mapping chain.
type SynonymDictionary struct {
	resource      string
	caseSensitive bool
	entries       []synonymEntry
}

func CompileSynonymDictionary(ctx context.Context, basename string, caseSensitive bool) (*SynonymDictionary, error) {
	return loadSynonymDictionary(ctx, dictionaryResources, basename, caseSensitive)
}

func loadSynonymDictionary(ctx context.Context, resources fs.FS, basename string, caseSensitive bool) (*SynonymDictionary, error) {
	if err := CheckDictionaryResourceName(ctx, basename); err != nil {
		return nil, err
	}
	path := basename + ".syn"
	data, err := fs.ReadFile(resources, path)
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
	return compileSynonymDictionary(ctx, string(data), basename, caseSensitive)
}

func synonymSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

func compileSynonymDictionary(ctx context.Context, data, basename string, caseSensitive bool) (*SynonymDictionary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &SynonymDictionary{resource: basename, caseSensitive: caseSensitive}
	lineNumber := 0
	nextCheck := 0
	for offset := 0; offset < len(data); {
		lineNumber++
		start := offset
		for offset < len(data) {
			if offset >= nextCheck {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				nextCheck = offset + 256
			}
			c := data[offset]
			if c >= utf8.RuneSelf {
				_, width := utf8.DecodeRuneInString(data[offset:])
				if width == 1 {
					err := sqltype.ValidateTextInput(data[offset:])
					var diagnostic *sqlstate.Error
					if errors.As(err, &diagnostic) {
						copy := *diagnostic
						copy.Context = fmt.Sprintf(`line %d of configuration file "%s.syn"`, lineNumber, basename)
						return nil, &copy
					}
					return nil, err
				}
				offset += width
				continue
			}
			offset++
			if c == '\n' {
				break
			}
		}
		line := data[start:offset]
		inputStart, inputEnd, err := synonymWord(ctx, line, 0)
		if err != nil {
			return nil, err
		}
		outputStart, outputEnd, err := synonymWord(ctx, line, inputEnd)
		if err != nil {
			return nil, err
		}
		if inputStart == inputEnd || outputStart == outputEnd {
			continue
		}
		input, output := line[inputStart:inputEnd], line[outputStart:outputEnd]
		prefix := output[len(output)-1] == '*'
		if prefix {
			output = output[:len(output)-1]
		}
		if !caseSensitive {
			var err error
			input, err = unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
			if err != nil {
				return nil, err
			}
			output, err = unicode16.ConvertCase(ctx, output, unicode16.Lower, unicode16.ASCII)
			if err != nil {
				return nil, err
			}
		}
		d.entries = append(d.entries, synonymEntry{input: input, lexeme: fts.Lexeme{Text: output, Prefix: prefix}})
	}
	if err := sortSynonyms(ctx, d.entries); err != nil {
		return nil, err
	}
	return d, nil
}

// In-place heapsort makes cancellation an ordinary error, with no panic-based
// sort escape, extra table allocation or work left running after cancellation.
func sortSynonyms(ctx context.Context, entries []synonymEntry) error {
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

func (d *SynonymDictionary) Configured(resource string, caseSensitive bool) bool {
	return d != nil && d.resource == resource && d.caseSensitive == caseSensitive
}

func (*SynonymDictionary) Filtering() bool { return false }

func (d *SynonymDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	if input == "" || len(d.entries) == 0 {
		return fts.LexicalResult{}, nil
	}
	if !d.caseSensitive {
		var err error
		input, err = unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
		if err != nil {
			return fts.LexicalResult{}, err
		}
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
			if err := ctx.Err(); err != nil {
				return fts.LexicalResult{}, err
			}
			return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{d.entries[mid].lexeme}}, nil
		}
	}
	return fts.LexicalResult{}, ctx.Err()
}

func synonymWord(ctx context.Context, line string, offset int) (int, int, error) {
	for offset < len(line) && synonymSpace(line[offset]) {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		offset++
	}
	start := offset
	for offset < len(line) && !synonymSpace(line[offset]) {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		offset++
	}
	return start, offset, ctx.Err()
}
