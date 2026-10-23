package parser

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// DO is an anonymous, no-parameter language invocation, not a transaction block.
type Do struct {
	Body         string
	BodyPos, Pos Position
	Language     Identifier
}

func (*Do) statement() {}

type PLType struct {
	Type TypeName
	Copy ObjectName
	Row  bool
}
type PLDeclaration struct {
	Name              Identifier
	Type              PLType
	Constant, NotNull bool
	NotNullPos        Position
	Default           Expr
	Alias             Expr
}
type PLStatement struct {
	Block      *PLBlock
	Target     Expr
	Expression Expr
	Return     bool
	Pos        Position
}
type PLBlock struct {
	Label        Identifier
	Declarations []PLDeclaration
	Statements   []PLStatement
	Pos          Position
}

// ParsePLpgSQL uses the ordinary scanner and expression grammar. The private
// entry token scopes procedural keywords to this parse; SQL prewalk is unchanged.
func ParsePLpgSQL(source string) (*PLBlock, error) {
	if !utf8.ValidString(source) {
		for offset := 0; offset < len(source); {
			r, n := utf8.DecodeRuneInString(source[offset:])
			if r == utf8.RuneError && n == 1 {
				return nil, PLPositionError("42601", "invalid UTF-8 in SQL", Position{Offset: offset, Character: utf8.RuneCountInString(source[:offset]) + 1})
			}
			offset += n
		}
	}
	if offset := strings.IndexByte(source, 0); offset >= 0 {
		return nil, PLPositionError("42601", "NUL in SQL", Position{Offset: offset, Character: utf8.RuneCountInString(source[:offset]) + 1})
	}
	l := &lexer{input: source, options: Options{StandardConformingStrings: true}, plEntry: true, plMode: true}
	if parseLexer(l) != 0 || l.err != nil {
		if diagnostic, ok := l.err.(*sqlstate.Error); ok && l.plExpression && l.current.text == ";" && diagnostic.Code == "42601" {
			copy := *diagnostic
			copy.Message = "syntax error at end of input"
			copy.Position = strconv.Itoa(l.current.pos.Character - 1)
			return nil, &copy
		}
		return nil, l.err
	}
	return l.plResult, nil
}

func (l *lexer) plBlock(label Identifier, declarations []PLDeclaration, statements []PLStatement, end Identifier, pos Position) *PLBlock {
	if end.Text != "" && end.Text != label.Text {
		message := fmt.Sprintf("end label %q differs from block's label %q", end.Text, label.Text)
		if label.Text == "" {
			message = fmt.Sprintf("end label %q specified for unlabeled block", end.Text)
		}
		l.failAt("42601", message, end.Pos.Offset)
	}
	return &PLBlock{Label: label, Declarations: declarations, Statements: statements, Pos: pos}
}

// PLExpressionSource is diagnostic provenance only, never reparsed or rewritten.
func PLExpressionSource(source string, expression Expr) string {
	if expression == nil {
		return ""
	}
	start := ExpressionStart(expression).Offset
	if start < 0 || start >= len(source) {
		return ""
	}
	l := lexer{input: source, offset: start, options: Options{StandardConformingStrings: true}}
	depth := 0
	end := len(source)
	for {
		tok := l.nextRaw()
		if tok.code == 0 {
			break
		}
		if tok.code == '(' || tok.code == '[' {
			depth++
		}
		if tok.code == ')' || tok.code == ']' {
			depth--
		}
		if tok.code == ';' && depth == 0 {
			end = tok.lexeme.pos.Offset
			break
		}
	}
	return strings.TrimSpace(source[start:end])
}

func PLPositionError(code, message string, pos Position) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: strconv.Itoa(pos.Character)}
}

func plCopiedType(raw TypeName, row bool) PLType {
	parts := raw.Parts
	if len(parts) == 0 {
		if raw.Schema != nil {
			parts = append(parts, *raw.Schema)
		}
		parts = append(parts, Identifier{Text: raw.Name, Pos: raw.Pos})
	}
	return PLType{Copy: ObjectName{Parts: parts}, Row: row}
}
