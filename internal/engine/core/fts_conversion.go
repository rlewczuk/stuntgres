package core

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// Convert captures immutable metadata, including the live default
// when omitted. Engine.Mu precedes the effective settings mutex. Parser,
// dictionary and notice callbacks run detached; runtime demand follows the
// actual ordered chain, never every mapping or a category prewarm.
func Convert[A Authority[A]](h Host[A], ctx context.Context, kind fts.ConversionKind, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	captured, err := captureTextSearchInput(h, ctx, explicit, value, false)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx = h.ActivateRuntime(ctx)
	capture := captured.acquisition
	walk := func(input string, emit func(fts.LexicalPosition)) error {
		position := 0
		return captured.Walk(ctx, h, input, &position, emit)
	}
	normalize := func(input string) ([]fts.LexicalPosition, error) {
		var positions []fts.LexicalPosition
		err := walk(input, func(position fts.LexicalPosition) { positions = append(positions, position) })
		return positions, err
	}
	if kind == fts.VectorConversion {
		var builder fts.VectorBuilder
		err := walk(text, func(position fts.LexicalPosition) {
			for _, word := range position.Lexemes {
				builder.Add(word.Text, position.Position)
			}
		})
		if err != nil {
			return sqltype.Value{}, err
		}
		vector, err := builder.Finish(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		value, err := sqltype.TSVectorValue(result, vector)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err := capture.accept(ctx, h); err != nil {
			return sqltype.Value{}, err
		}
		return value, nil
	}
	query, parsed, err := fts.ConvertQuery(ctx, kind, text, normalize)
	if err != nil {
		return sqltype.Value{}, err
	}
	if query.Empty() {
		message := "text-search query doesn't contain lexemes: \"" + text + "\""
		if parsed {
			message = "text-search query contains only stop words or doesn't contain lexemes, ignored"
		}
		h.Operation().EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	}
	value, err = sqltype.TSQueryValue(result, query)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := capture.accept(ctx, h); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}

// Capture owns one immutable configuration generation. Acquisition
// uses a captured catalog read; parser/dictionary callbacks run detached.
type Capture[A Authority[A]] struct {
	config      *catalog.TSConfiguration
	parser      fts.ParserRuntime
	acquisition *tsAcquisition[A]
}

// CaptureConfiguration captures configuration oid in read and its parser. It
// takes no lock: read is immutable.
func CaptureConfiguration[A Authority[A]](ctx context.Context, read catalog.Read, oid uint32) (Capture[A], error) {
	config, ok := read.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return Capture[A]{}, sqlError("XX000", fmt.Sprintf("cache lookup failed for text search configuration %d", oid))
	}
	if err := (catalog.TSConfigurationProvider{}).Validate(read, config); err != nil {
		return Capture[A]{}, err
	}
	parser, err := catalog.ResolveParserRuntime(read, read.MustTSParser(config.Parser))
	if err != nil {
		return Capture[A]{}, err
	}
	return Capture[A]{config: config, parser: parser, acquisition: newTSAcquisition[A](ctx, read, config)}, nil
}

// Captured reports whether c holds a configuration.
func (c Capture[A]) Captured() bool { return c.config != nil }

// Accept fences c's dictionary acquisition, if any, against the current
// catalog and authorization, and publishes its thesaurus receipts.
func (c Capture[A]) Accept(ctx context.Context, h Host[A]) error {
	if c.acquisition == nil {
		return nil
	}
	return c.acquisition.accept(ctx, h)
}

// Walk tokenizes input and emits its lexical positions, numbering from
// *position. It runs with no engine lock held.
func (c Capture[A]) Walk(ctx context.Context, h Host[A], input string, position *int, emit func(fts.LexicalPosition)) error {
	parser, config, capture := c.parser, c.config, c.acquisition
	noticeLong := func() {
		h.Operation().EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."})
	}
	tokens, err := parser.Parse(ctx, input)
	if err != nil {
		return err
	}
	// Lookahead can revisit a suffix after an earlier completion. Token
	// cursors scan forward, so one high-water mark prevents notice replay.
	lastLongToken := -1
	for head := 0; head < len(tokens); {
		if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
			return err
		}
		if err := util.ExecutionCheckpoint(ctx, "text_search.vector"); err != nil {
			return err
		}
		token := tokens[head]
		if len(token.Text) >= 2047 {
			if head > lastLongToken {
				noticeLong()
				lastLongToken = head
			}
			head++
			continue
		}
		word := token.Text
		nextHead := head + 1
		for _, address := range config.Mapping(token.ID) {
			if err := ctx.Err(); err != nil {
				return err
			}
			dictionary, err := capture.dictionary(h, address)
			if err != nil {
				return err
			}
			if phrase, ok := dictionary.(catalog.PhraseDictionaryRuntime); ok {
				matcher := phrase.NewMatcher()
				words, more, err := matcher.Step(ctx, word, false)
				if err != nil {
					return err
				}
				saved, consumed := words, head+1
				continued := more
				for cursor := head + 1; more; cursor++ {
					if cursor == len(tokens) {
						_, _, err = matcher.Step(ctx, "", true)
						if err != nil {
							return err
						}
						break
					}
					continuation := tokens[cursor]
					if len(continuation.Text) >= 2047 {
						if cursor > lastLongToken {
							noticeLong()
							lastLongToken = cursor
						}
						continue
					}
					mapping := config.Mapping(continuation.ID)
					if len(mapping) == 0 {
						continue
					}
					if !slices.Contains(mapping, address) {
						saved = nil
						break
					}
					words, more, err = matcher.Step(ctx, continuation.Text, false)
					if err != nil {
						return err
					}
					if words != nil {
						saved, consumed = words, cursor+1
					}
				}
				matcher.Reset()
				if saved == nil {
					if continued {
						word = token.Text
					}
					continue
				}
				nextHead = consumed
				*position++
				lexemes := make([]fts.Lexeme, 0, len(saved))
				for _, replacement := range saved {
					if replacement.AddPosition {
						emit(fts.LexicalPosition{Position: *position, Lexemes: lexemes})
						*position++
						// Query normalization can retain previously emitted slices.
						lexemes = lexemes[len(lexemes):len(lexemes):cap(lexemes)]
					}
					if len(replacement.Text) > 2047 {
						noticeLong()
						continue
					}
					lexemes = append(lexemes, fts.Lexeme{Text: replacement.Text, Prefix: replacement.Prefix, Variant: replacement.Variant})
				}
				emit(fts.LexicalPosition{Position: *position, Lexemes: lexemes})
				break
			}
			lexical, err := dictionary.Lexize(ctx, word)
			if err != nil {
				return err
			}
			if !lexical.Recognized {
				continue
			}
			if dictionary.Filtering() && len(lexical.Lexemes) > 0 {
				word = lexical.Lexemes[0].Text
				continue
			}
			words := lexical.Lexemes[:0]
			for _, lexeme := range lexical.Lexemes {
				if err := ctx.Err(); err != nil {
					return err
				}
				if len(lexeme.Text) > 2047 {
					noticeLong()
					continue
				}
				words = append(words, lexeme)
			}
			*position++
			emit(fts.LexicalPosition{Position: *position, Lexemes: words})
			break
		}
		head = nextHead
	}
	return ctx.Err()
}
