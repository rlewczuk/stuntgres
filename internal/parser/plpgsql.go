package parser

import (
	"errors"
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
	Collation         *ObjectName
	Constant, NotNull bool
	NotNullPos        Position
	Default           Expr
	DefaultStart      Position
	DefaultEnd        Position
	DefaultDelimiter  Position
	Alias             Expr
	Cursor            *PLCursorDeclaration
}
type PLStatement struct {
	Block               *PLBlock
	Loop                *PLBlock
	Transfer            *PLTransfer
	Target              Expr
	Expression          Expr
	Return              bool
	Rethrow             bool
	ExpressionStart     Position
	ExpressionEnd       Position
	ExpressionDelimiter Position
	If                  *PLConditional
	Case                *PLConditional
	Pos                 Position
	Cursor              *PLCursorStatement
	QueryLoop           *PLQueryLoop
	Foreach             *PLForeach
	Raise               *PLRaise
	Assert              *PLAssert
}

// PLQueryLoop retains the ordinary SQL syntax and its original source boundary.
// Unlike a cursor FOR, its target belongs to the enclosing lexical namespace.
type PLQueryLoop struct {
	Target                Expr
	Query                 Statement
	Start, End, Delimiter Position
	Body                  *PLBlock
}

// PLForeach retains the ordered enclosing targets and one scalar array source.
// SliceSpecified distinguishes omitted SLICE from an explicit zero.
type PLForeach struct {
	Targets        []Expr
	TargetCommas   []Position
	Slice          int
	SliceSpecified bool
	SlicePos       Position
	Source         PLExpression
	Body           *PLBlock
}

// ValidPLForeachSlice is the nonnegative signed-32-bit ICONST invariant shared
// by syntax admission and checked-program validation.
func ValidPLForeachSlice(level int64) bool { return level >= 0 && level <= 2147483647 }

func (l *lexer) plForeachSlice(keyword, token lexeme) *PLForeach {
	if token.numeric {
		l.syntax(token)
		return nil
	}
	// Match the existing bounded integer-literal convention: decimal leading
	// zeroes stay decimal; only explicit radix prefixes select base zero.
	text := strings.ReplaceAll(token.text, "_", "")
	base := 10
	if len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])) {
		base = 0
	}
	level, err := strconv.ParseInt(text, base, 32)
	if err != nil || !ValidPLForeachSlice(level) {
		l.syntax(token)
		return nil
	}
	return &PLForeach{Slice: int(level), SliceSpecified: true, SlicePos: keyword.pos}
}

type PLBlock struct {
	Label        Identifier
	EndLabel     Identifier
	IsLoop       bool
	Declarations []PLDeclaration
	Statements   []PLStatement
	Handlers     []PLHandler
	Pos          Position
}

type PLTransfer struct {
	Continue bool
	Label    Identifier
	Alias    Identifier // Optional scalar projection alias in WHEN.
}

type PLCondition struct {
	Name Identifier
	Code string
	Pos  Position
}
type PLHandler struct {
	Conditions []PLCondition
	Statements []PLStatement
	Pos        Position
}

type plElse struct {
	HasElse bool
	Else    []PLStatement
}

type plExpressionBoundary uint8

const (
	plNoBoundary plExpressionBoundary = iota
	plThenBoundary
	plWhenBoundary
	plWhenItemBoundary
	plSemicolonBoundary
	plLoopBoundary
	plMessageBoundary
	plOptionBoundary
)

func (l *lexer) plBeginExpression(boundary plExpressionBoundary) {
	l.plBoundary = boundary
	l.plBoundaryDepth, l.plBoundaryTokens, l.plBoundaryHit = 0, 0, 0
	l.plExpression = true
	l.plThen = boundary == plThenBoundary || boundary == plWhenItemBoundary
}

// Only parentheses/brackets protect procedural delimiters. SQL CASE nesting
// does not: the ordinary scalar grammar must reject an incomplete expression
// when the first depth-zero procedural terminal reaches it.
func (l *lexer) plExpressionToken(token rawToken) rawToken {
	if l.plBoundary == plNoBoundary {
		return token
	}
	if l.plBoundaryDepth == 0 {
		code := 0
		switch {
		case token.code == THEN && (l.plBoundary == plThenBoundary || l.plBoundary == plWhenItemBoundary):
			code = PL_THEN
		case token.code == WHEN && l.plBoundary == plWhenBoundary:
			code = PL_WHEN
		case token.code == ',' && l.plBoundary == plWhenItemBoundary:
			code = PL_WHEN_COMMA
		case token.code == ',' && (l.plBoundary == plMessageBoundary || l.plBoundary == plOptionBoundary):
			code = PL_MESSAGE_COMMA
		case token.code == USING && l.plBoundary == plMessageBoundary:
			code = PL_RAISE_USING
		case token.code == ';' && (l.plBoundary == plMessageBoundary || l.plBoundary == plOptionBoundary):
			code = ';'
		case token.code == ';' && l.plBoundary == plSemicolonBoundary:
			code = ';'
		case l.plBoundary == plLoopBoundary && (token.lexeme.keyword != nil || token.code == IDENT) && !quotedIdentifierLexeme(token.lexeme) && token.lexeme.text == "loop":
			code = PL_LOOP
		}
		if code != 0 {
			token.code = code
			if code == PL_LOOP {
				l.plForeachSource = Position{}
			}
			l.plBoundaryHit, l.plBoundary = code, plNoBoundary
			l.plExpression = false
			return token
		}
	}
	l.plBoundaryTokens++
	switch token.code {
	case '(', '[':
		l.plBoundaryDepth++
	case ')', ']':
		if l.plBoundaryDepth > 0 {
			l.plBoundaryDepth--
		}
	}
	return token
}

// PLReturnPolicy applies legality before the scalar grammar reads a RETURN
// argument. A zero policy permits arguments; checked compilation still validates.
type PLReturnPolicy struct{ Code, Message string }

func PLBodyError(err error, origin Position) error {
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	position, failure := strconv.Atoi(diagnostic.Position)
	if failure != nil {
		return err
	}
	next := *diagnostic
	next.Position = strconv.Itoa(origin.Character + position)
	return &next
}

// PLExpression separates the last accepted token's exclusive end from its
// procedural delimiter, retaining grouping and excluding trailing trivia.
type PLExpression struct {
	Expression Expr
	Alias      Identifier
	Start      Position
	End        Position
	Delimiter  Position
}
type PLArm struct {
	Conditions []PLExpression
	Statements []PLStatement
}
type PLConditional struct {
	Search  PLExpression
	Arms    []PLArm
	HasElse bool
	Else    []PLStatement
}

func (l *lexer) plParsedExpression(raw Expr) PLExpression {
	return PLExpression{Expression: raw, End: l.plPriorTokenEnd, Delimiter: l.current.pos}
}

// The predecessor is supplied by grammar, so transparent grouping in the SQL
// AST cannot discard the actual expression origin. This bounded scanner probe
// records provenance only; it neither reparses nor executes the expression.
func (l *lexer) plBoundExpression(raw PLExpression, predecessor lexeme) PLExpression {
	if raw.Expression == nil {
		return raw
	}
	raw.Start = l.plSourceStart(raw.End, raw.Delimiter, predecessor)
	return raw
}

// Expressions and cursor queries share the same grammar-supplied boundary.
func (l *lexer) plSourceStart(end, delimiter Position, predecessor lexeme) Position {
	probe := lexer{
		input: l.input[:delimiter.Offset], offset: predecessor.pos.Offset,
		positionOffset: predecessor.pos.Offset, positionCharacters: predecessor.pos.Character - 1,
		options: Options{StandardConformingStrings: true},
	}
	probe.nextRaw()
	first := probe.nextRaw()
	if first.code == 0 || probe.err != nil {
		l.err = probe.err
		if l.err == nil {
			l.failAt("42601", "missing expression", end.Offset)
		}
		return Position{}
	}
	return first.lexeme.pos
}

// ParsePLpgSQL uses the ordinary scanner and expression grammar. The private
// entry token scopes procedural keywords to this parse; SQL prewalk is unchanged.
func ParsePLpgSQL(source string, returns PLReturnPolicy) (*PLBlock, error) {
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
	l := &lexer{input: source, options: Options{StandardConformingStrings: true}, plEntry: true, plMode: true, plReturnPolicy: returns}
	if parseLexer(l) != 0 || l.err != nil {
		if diagnostic, ok := l.err.(*sqlstate.Error); ok && diagnostic.Code == "42601" {
			copy := *diagnostic
			if l.plBoundaryHit != 0 && l.previousToken == l.plBoundaryHit {
				if l.plBoundaryTokens == 0 {
					copy.Message = fmt.Sprintf("missing expression at or near %q", strings.ToUpper(l.current.text))
				} else {
					copy.Message = "syntax error at end of input"
					copy.Position = strconv.Itoa(l.plPriorTokenEnd.Character)
				}
				return nil, &copy
			}
			if l.plThen && (l.current.text == ";" || l.current.text == "") {
				copy.Message = "missing \"THEN\" at end of SQL expression"
				return nil, &copy
			}
			if l.plExpression && l.current.text == ";" {
				copy.Message = "syntax error at end of input"
				copy.Position = strconv.Itoa(l.plPriorTokenEnd.Character)
				return nil, &copy
			}
		}
		return nil, l.err
	}
	return l.plResult, nil
}

func (l *lexer) plBlock(label Identifier, declarations []PLDeclaration, statements []PLStatement, end Identifier, pos Position) *PLBlock {
	if end.Text != "" && end.Text != label.Text {
		message := fmt.Sprintf(`end label "%s" differs from block's label "%s"`, end.Text, label.Text)
		if label.Text == "" {
			message = fmt.Sprintf(`end label "%s" specified for unlabeled block`, end.Text)
		}
		l.failAt("42601", message, end.Pos.Offset)
	}
	return &PLBlock{Label: label, EndLabel: end, Declarations: declarations, Statements: statements, Pos: pos}
}

func (l *lexer) plLoop(label Identifier, statements []PLStatement, end Identifier, pos Position) *PLBlock {
	block := l.plBlock(label, nil, statements, end, pos)
	block.IsLoop = true
	return block
}

// PLExpressionSource is diagnostic provenance only, never reparsed or rewritten.
func PLExpressionSource(source string, start, end Position) string {
	if start.Character == 0 || start.Offset < 0 || end.Offset < start.Offset || end.Offset > len(source) {
		return ""
	}
	return source[start.Offset:end.Offset]
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

func validPLSQLState(code string) bool {
	if len(code) != 5 {
		return false
	}
	for i := range code {
		if !(code[i] >= '0' && code[i] <= '9' || code[i] >= 'A' && code[i] <= 'Z') {
			return false
		}
	}
	return true
}
