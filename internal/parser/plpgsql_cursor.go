package parser

// Cursor payloads preserve syntax distinctions (notably counted FETCH) until
// checked compilation. Expressions are evaluated only by the ordinary binder.
type PLCursorDeclaration struct {
	Scroll                               CursorScroll
	Arguments                            []PLDeclaration
	Query                                *Select
	QueryStart, QueryEnd, QueryDelimiter Position
}

type PLCursorAction uint8

const (
	PLOpenCursor PLCursorAction = iota + 1
	PLFetchCursor
	PLMoveCursor
	PLCloseCursor
	PLForCursor
)

type PLCursorActual struct {
	Name                  Identifier
	Expression            Expr
	Separator             Position
	Start, End, Delimiter Position
}

type PLCursorMotion struct {
	Direction                            CursorDirection
	Count                                Expr
	CountStart, CountEnd, CountDelimiter Position
	All, Counted                         bool
}

type PLCursorStatement struct {
	Action                                     PLCursorAction
	Variable                                   Expr
	Scroll                                     CursorScroll
	Query                                      *Select
	QueryStart, QueryEnd, QueryDelimiter       Position
	Command                                    Expr
	CommandStart, CommandEnd, CommandDelimiter Position
	Using                                      []PLExpression
	Arguments                                  []PLCursorActual
	HasArguments                               bool
	ArgumentsEnd                               Position
	ArgumentsStart                             Position
	Motion                                     PLCursorMotion
	Targets                                    []Expr
	TargetsStart, TargetsEnd, TargetsDelimiter Position
	LoopName                                   Identifier
	Loop                                       *PLBlock
}

func plCursorInteger(n int64, pos Position) Expr {
	return LiteralExpr{Value: Value{Kind: IntegerValue, Integer: n, Pos: pos}}
}

type plCursorQuery struct {
	Query                 *Select
	Start, End, Delimiter Position
}

func (l *lexer) plBoundCursorQuery(raw plCursorQuery, predecessor lexeme) plCursorQuery {
	raw.Start = l.plSourceStart(raw.End, raw.Delimiter, predecessor)
	return raw
}

func (l *lexer) plCursorActual(raw PLExpression) PLCursorActual {
	return PLCursorActual{Expression: raw.Expression, Start: raw.Start, End: raw.End, Delimiter: raw.Delimiter}
}

func (l *lexer) plBoundCursorActual(raw PLCursorActual, predecessor lexeme) PLCursorActual {
	if raw.Start.Character == 0 {
		raw.Start = l.plSourceStart(raw.End, raw.Delimiter, predecessor)
	}
	return raw
}

func (l *lexer) plNamedCursorActual(raw, value PLExpression, separator lexeme) PLCursorActual {
	name, ok := raw.Expression.(ColumnExpr)
	if !ok || len(name.Name.Parts) != 1 {
		l.syntax(separator)
		return PLCursorActual{}
	}
	actual := l.plCursorActual(l.plBoundExpression(value, separator))
	actual.Name = name.Name.Parts[0]
	return actual
}

func (l *lexer) plCursorMotion(direction CursorDirection, raw PLExpression, counted bool, predecessor lexeme) PLCursorMotion {
	raw = l.plBoundExpression(raw, predecessor)
	return PLCursorMotion{Direction: direction, Count: raw.Expression, CountStart: raw.Start, CountEnd: raw.End, CountDelimiter: raw.Delimiter, Counted: counted}
}

func (l *lexer) plFixedCursorMotion(count int64, token lexeme) PLCursorMotion {
	// Fixed keywords may reduce without a lookahead, unlike SQL expressions.
	end := Position{Offset: token.pos.Offset + len(token.raw), Character: token.pos.Character + len(token.raw)}
	return PLCursorMotion{Direction: CursorAbsolute, Count: plCursorInteger(count, token.pos), CountStart: token.pos, CountEnd: end, CountDelimiter: end}
}

func (l *lexer) plCursorTargets(raw *PLCursorStatement, into lexeme) *PLCursorStatement {
	raw.TargetsEnd, raw.TargetsDelimiter = l.plPriorTokenEnd, l.current.pos
	raw.TargetsStart = l.plSourceStart(raw.TargetsEnd, raw.TargetsDelimiter, into)
	return raw
}
