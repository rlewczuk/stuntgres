package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
)

// ConvertTextSearch captures one catalog generation, including the live default
// when omitted. Engine.Mu precedes the effective settings mutex. All parser,
// dictionary and notice callbacks run after releasing catalog authority.
func (h *statementHost) ConvertTextSearch(ctx context.Context, kind fts.ConversionKind, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var oid uint32
	var err error
	if explicit {
		oid, err = value.RegTSOID()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	if !explicit {
		var auth AuthorizationView
		auth, err = s.namespaceAuthorizationLocked(e)
		if err == nil {
			name, _ := s.Op.Setting("default_text_search_config")
			oid, err = regobj.NewTextSearchInput(inputCapture(auth, nil)).ResolveRegconfig(ctx, name)
		}
	}
	var captured textSearchCapture
	if err == nil {
		captured, err = captureTextSearch(read, oid)
	}
	e.Mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	walk := func(input string, emit func(fts.LexicalPosition)) error {
		position := 0
		return captured.walk(ctx, s, input, &position, emit)
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
		return sqltype.TSVectorValue(result, vector)
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
		s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	}
	return sqltype.TSQueryValue(result, query)
}

// textSearchCapture owns one immutable configuration generation. Capturing
// requires Engine.Mu; walking never holds it across parser/dictionary callbacks.
type textSearchCapture struct {
	config       *catalog.TSConfiguration
	parser       fts.ParserRuntime
	dictionaries map[ids.ObjectAddress]catalog.DictionaryRuntime
}

func captureTextSearch(read catalog.Read, oid uint32) (textSearchCapture, error) {
	config, ok := read.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return textSearchCapture{}, SQLError("XX000", fmt.Sprintf("cache lookup failed for text search configuration %d", oid))
	}
	if err := (catalog.TSConfigurationProvider{}).Validate(read, config); err != nil {
		return textSearchCapture{}, err
	}
	parser, err := catalog.ResolveParserRuntime(read, read.MustTSParser(config.Parser))
	if err != nil {
		return textSearchCapture{}, err
	}
	dictionaries := make(map[ids.ObjectAddress]catalog.DictionaryRuntime)
	for _, mapping := range config.Mappings {
		for _, address := range mapping.Dictionaries {
			if _, exists := dictionaries[address]; !exists {
				dictionaries[address] = read.MustTSDictionary(address).Runtime
			}
		}
	}
	return textSearchCapture{config: config, parser: parser, dictionaries: dictionaries}, nil
}

func (c textSearchCapture) walk(ctx context.Context, s *StatementScope, text string, position *int, emit func(fts.LexicalPosition)) error {
	noticeLong := func() {
		s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."})
	}
	lexize := func(token fts.DocumentToken) (fts.LexicalResult, error) {
		if err := workadmission.Admit(ctx, workadmission.WorkTSVectorConvert); err != nil {
			return fts.LexicalResult{}, err
		}
		if len(token.Text) >= 2047 {
			noticeLong()
			return fts.LexicalResult{}, nil
		}
		input := token.Text
		for _, address := range c.config.Mapping(token.ID) {
			if err := ctx.Err(); err != nil {
				return fts.LexicalResult{}, err
			}
			dictionary := c.dictionaries[address]
			lexical, err := dictionary.Lexize(ctx, input)
			if err != nil {
				return fts.LexicalResult{}, err
			}
			if !lexical.Recognized {
				continue
			}
			if dictionary.Filtering() && len(lexical.Lexemes) > 0 {
				input = lexical.Lexemes[0].Text
				continue
			}
			words := lexical.Lexemes[:0]
			for _, word := range lexical.Lexemes {
				if err := ctx.Err(); err != nil {
					return fts.LexicalResult{}, err
				}
				if len(word.Text) > 2047 {
					noticeLong()
					continue
				}
				words = append(words, word)
			}
			lexical.Lexemes = words
			return lexical, nil
		}
		return fts.LexicalResult{}, nil
	}
	tokens, err := c.parser.Parse(ctx, text)
	if err != nil {
		return err
	}
	return fts.NormalizeTokens(ctx, tokens, position, lexize, emit)
}
