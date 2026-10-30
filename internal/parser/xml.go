package parser

type xmlLexical struct {
	Depth     int
	Parse     bool
	Serialize bool
	Exists    bool
	Passed    bool
	Target    bool
	Table     bool
	Columns   bool
	DocumentStarted bool // Distinguishes a prefix mechanism from a suffix.
	ColumnStage int // 1=name, 2=first type token, 3=type tail/options.
	Construct string
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

type XMLConstructKind uint8
const (
	XMLConcat XMLConstructKind = iota
	XMLElement
	XMLForest
	XMLPI
	XMLRoot
)
func (k XMLConstructKind) String() string {
	switch k {case XMLConcat:return "xmlconcat";case XMLElement:return "xmlelement";case XMLForest:return "xmlforest";case XMLPI:return "xmlpi";case XMLRoot:return "xmlroot"}
	return ""
}
type XMLNamedOperand struct {
	Expr Expr
	Name *Identifier
	Pos Position
}
type xmlStandaloneOption struct { Kind uint8; Pos Position }
type XMLConstructExpr struct {
	Kind XMLConstructKind
	Name Identifier
	Named []XMLNamedOperand
	Args []Expr
	Version Expr
	VersionPos Position
	Standalone uint8 // omitted, NO VALUE, YES, NO
	StandalonePos Position
	Pos Position
}
func (XMLConstructExpr) expression() {}
func (e XMLConstructExpr) SourcePosition() Position { return e.Pos }
func (e XMLConstructExpr) Children() []Expr {
	out:=make([]Expr,0,len(e.Named)+len(e.Args)+1)
	for _,named:=range e.Named {out=append(out,named.Expr)}
	out=append(out,e.Args...)
	if e.Version!=nil {out=append(out,e.Version)}
	return out
}
