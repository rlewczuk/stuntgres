package parser

type xmlLexical struct {
	Depth     int
	Serialize bool
	Exists    bool
	Passed    bool
	Target    bool
	Table     bool
	Columns   bool
	DocumentStarted bool // Distinguishes a prefix mechanism from a suffix.
	ColumnStage int // 1=name, 2=first type token, 3=type tail/options.
}

// XMLParseExpr retains the written mode and whitespace clause independently of
// the session input mode. An empty Whitespace is the omitted clause.
type XMLParseExpr struct {
	Child      Expr
	Document   bool
	Whitespace string
	Pos        Position
}

func (XMLParseExpr) expression()                {}
func (e XMLParseExpr) SourcePosition() Position { return e.Pos }

// XMLSerializeExpr retains its target syntax; binding supplies target authority.
type XMLSerializeExpr struct {
	Child    Expr
	Document bool
	Target   TypeName
	Indent   string
	Pos      Position
}

func (XMLSerializeExpr) expression()                {}
func (e XMLSerializeExpr) SourcePosition() Position { return e.Pos }

type XMLDocumentPredicateExpr struct {
	Child   Expr
	Negated bool
	Pos     Position
}

func (XMLDocumentPredicateExpr) expression()                {}
func (e XMLDocumentPredicateExpr) SourcePosition() Position { return e.Pos }
