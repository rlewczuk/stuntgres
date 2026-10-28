package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ConvertTSVector captures one catalog generation. No catalog mutex is held
// while parser or dictionary execution runs; runtimes are immutable authority.
func (h *statementHost) ConvertTSVector(value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	ctx := s.Op.Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	config, ok := read.TSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: ids.OID(oid)})
	var dictionaries map[ids.ObjectAddress]catalog.DictionaryRuntime
	var array *sqltype.Descriptor
	var parser fts.ParserRuntime
	if ok {
		err = (catalog.TSConfigurationProvider{}).Validate(read, config)
		if err == nil {
			parser, err = catalog.ResolveParserRuntime(read, read.MustTSParser(config.Parser))
		}
		if err == nil {
			dictionaries = make(map[ids.ObjectAddress]catalog.DictionaryRuntime)
			for _, m := range config.Mappings {
				for _, a := range m.Dictionaries {
					if _, exists := dictionaries[a]; !exists {
						dictionaries[a] = read.MustTSDictionary(a).Runtime
					}
				}
			}
			array = read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1009}).SQLType
		}
	}
	e.Mu.Unlock()
	if !ok {
		return sqltype.Value{}, SQLError("XX000", fmt.Sprintf("cache lookup failed for text search configuration %d", oid))
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	tokens, err := parser.Parse(ctx, text)
	if err != nil {
		return sqltype.Value{}, err
	}
	var builder fts.VectorBuilder
	position := 0
	for _, token := range tokens {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		// The document parser accepts arbitrary spans, but conversion ignores
		// oversized tokens before consulting any dictionary.
		if len(token.Text) >= 2047 {
			s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."})
			continue
		}
		input := token.Text
		for _, a := range config.Mapping(token.ID) {
			dictionary := dictionaries[a]
			lexemes, err := dictionary.Lexize(ctx, input, array)
			if err != nil {
				return sqltype.Value{}, err
			}
			if lexemes.IsNull() {
				continue
			}
			if dictionary.Filtering() && lexemes.ArrayLen() > 0 {
				input, err = lexemes.ArrayElement(0).Text()
				if err != nil {
					return sqltype.Value{}, err
				}
				continue
			}
			position++
			for i := range lexemes.ArrayLen() {
				word, err := lexemes.ArrayElement(i).Text()
				if err != nil {
					return sqltype.Value{}, err
				}
				if len(word) > 2047 {
					s.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."})
					continue
				}
				builder.Add(word, position)
			}
			break
		}
	}
	vector, err := builder.Finish(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.TSVectorValue(result, vector)
}
