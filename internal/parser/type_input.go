package parser

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

// ParseTypeName uses the private goyacc start, consuming the decoded input in
// full without a statement wrapper or expanding ordinary annotation admission.
func ParseTypeName(ctx context.Context, input string, notice func(sqlstate.Error)) (TypeName, error) {
	if err := ctx.Err(); err != nil {
		return TypeName{}, err
	}
	if strings.Trim(input, " \t\n\r\f\v") == "" {
		return TypeName{}, invalidTypeName(input)
	}
	l := &lexer{input: input, options: Options{Context: ctx, StandardConformingStrings: true}, typeInputEntry: true, typeInputMode: true, typeInputNotice: notice}
	if parseLexer(l) != 0 || l.err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(l.err, &diagnostic) {
			copied := *diagnostic
			copied.Context = fmt.Sprintf("invalid type name \"%s\"", input)
			return TypeName{}, &copied
		}
		return TypeName{}, l.err
	}
	if l.typeInputResult.Setof {
		return TypeName{}, invalidTypeName(input)
	}
	return l.typeInputResult, nil
}

func invalidTypeName(input string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: fmt.Sprintf("invalid type name \"%s\"", input)}
}

func inputColumnType(l sqlLexer, tok lexeme) TypeName {
	if !l.(*lexer).typeInputMode {
		return columnType(tok)
	}
	raw := TypeName{Name: tok.text, Pos: tok.pos, Quoted: columnType(tok).Quoted}
	// Generic type names use PostgreSQL's type/function-name category, not
	// the broader ordinary column-name grammar. Real builtin type syntax is
	// recognized by the existing standalone normalizer; quoted and qualified
	// labels retain their own grammar admission.
	if tok.keyword != nil && !tok.keyword.typeFunctionName() && !standaloneType(raw).System {
		l.(*lexer).syntax(tok)
	}
	return raw
}

func appendStandalonePart(raw TypeName, tok lexeme) TypeName {
	if len(raw.Parts) == 0 {
		if raw.Schema != nil {
			raw.Parts = append(raw.Parts, *raw.Schema)
		}
		raw.Parts = append(raw.Parts, Identifier{Text: raw.Name, Pos: raw.Pos})
	}
	raw.Parts = append(raw.Parts, Identifier{Text: tok.text, Pos: tok.pos})
	raw.Name, raw.Quoted = tok.text, columnType(tok).Quoted
	raw.System = false
	return raw
}

func standaloneType(raw TypeName) TypeName {
	if raw.Schema != nil || len(raw.Parts) > 1 || raw.Quoted {
		return raw
	}
	switch raw.Name {
	case "integer", "int":
		raw.Name, raw.System = "int4", true
	case "smallint":
		raw.Name, raw.System = "int2", true
	case "bigint":
		raw.Name, raw.System = "int8", true
	case "boolean":
		raw.Name, raw.System = "bool", true
	case "real":
		raw.Name, raw.System = "float4", true
	case "float", "double precision":
		raw.Name, raw.System = "float8", true
	case "decimal", "dec":
		raw.Name, raw.System = "numeric", true
	case "char", "character":
		raw.Name, raw.System = "bpchar", true
	case "character varying", "varchar":
		raw.Name, raw.System = "varchar", true
	case "bit varying":
		raw.Name, raw.System = "varbit", true
	case "numeric", "bit", "time", "timestamp", "interval", "json":
		raw.System = true
	}
	return raw
}
