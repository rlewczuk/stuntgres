package expr

import "stuntgres/internal/sqltype"

// Renderable nodes describe their own presentation. One Rendering value serves
// every engine renderer that lists nodes: the node-tree output, the PostgreSQL
// deparser and stored scalar SQL (replay). The renderers own quoting, literal
// transport and parenthesization; the node only states what it looks like.
type Renderable interface {
	// Render validates the node and returns its presentation. Its errors are
	// integrity errors of a corrupted node; a node bound by the engine renders.
	Render() (Rendering, error)
}

// Rendering is a node's presentation, read by each renderer for the parts it
// needs.
type Rendering struct {
	// Tree is the node-tree header after "node ", for example
	// "Math pg_size_pretty". An empty Tree means the node has no node-tree form.
	Tree string
	// Inputs are the node-tree children in output order; nil entries are skipped.
	Inputs []Expr

	// Form is the SQL shape; FormNone means the node has no SQL form.
	Form Form
	// Text is the FormLeaf SQL text, exactly as displayed.
	Text string
	// Name is the FormCall function name or the FormInfix operator spelling.
	Name string
	// Namespace qualifies a captured FormCall. For FormInfix, "" and
	// pg_catalog display the bare spelling; other schemas use OPERATOR.
	Namespace string
	// Quote is how the deparser quotes a FormCall Name.
	Quote Quote
	// LabelQuote is how the deparser quotes argument labels.
	LabelQuote Quote
	// Args are the call arguments in written order, the two infix operands, or
	// the arguments that template pieces refer to.
	Args []RenderArg
	// Variadic marks the last call argument VARIADIC.
	Variadic bool
	// UnknownTexts makes each argument's UnknownText replace the flag the
	// deparser inherited from the enclosing call.
	UnknownTexts bool
	// Grouping is how the deparser parenthesizes a FormInfix operator.
	Grouping Grouping
	// Pieces are the FormTemplate keyword syntax, rendered in order.
	Pieces []Piece
	// SimpleCast is set for calls whose explicit cast the deparser displays
	// without parentheses around the call in pretty mode.
	SimpleCast bool
	// JSONConstructor marks a SQL/JSON representation boundary, independently
	// of call spelling or SQL form.
	JSONConstructor bool
	// JSONReturning permits the enclosing constructor-owned conversion to
	// supply a type usage for Returning pieces. Ordinary casts never supply it.
	JSONReturning bool
	// OwnedReturning supplies a query expression's internal result conversion;
	// unlike constructors, no enclosing coercion owns this RETURNING clause.
	OwnedReturning sqltype.Usage

	// Replay is how stored scalar SQL writes the node where that differs from
	// the display form above.
	Replay Replay
}

// Replay is the stored scalar SQL form of a node. Stored SQL renders every
// argument in order before assembling the form, ignores the display-only
// argument fields (HideImplicit, Listed, UnknownText, Cast), qualifies call
// names with pg_catalog and always parenthesizes infix operators. The zero
// value replays the display form that way.
type Replay struct {
	// Validate, when set, checks the node before stored SQL replays it. Its
	// error is the integrity error stored SQL reports instead: a stored
	// provider that no longer validates, or a node with no stored form
	// (set-returning calls, a standalone SIMILAR normalizer). Display
	// renderers never call it.
	Validate func() error
	// Name is the FormCall function name after "pg_catalog." when it differs
	// from Name; replay writes it as given.
	Name string
	// Text is the FormLeaf text when it differs from Text.
	Text string
	// Pieces, when set, replay the node as a template over Args.
	Pieces []Piece
	// Args are the replay arguments when they differ from Rendering.Args.
	Args []RenderArg
	// QuoteNamespace always quotes a FormInfix operator schema; otherwise
	// replay quotes it only when needed.
	QuoteNamespace bool
	// CatalogSnapshot marks a call whose result reads catalog names, so its
	// stored definition demands a catalog snapshot.
	CatalogSnapshot bool
	// VisibilitySnapshot marks a call that reads visibility state unless a
	// literal NULL argument decides it.
	VisibilitySnapshot bool
	// AlwaysVisibilitySnapshot preserves unconditional stored demand for a
	// retained call, independently of NULL arguments and execution folding.
	AlwaysVisibilitySnapshot bool
	// RelationOnly accepts the call only in an expression a relation owns;
	// elsewhere stored SQL reports a volatile stored default.
	RelationOnly bool
	// Types are types the node holds besides its arguments and result; stored
	// SQL checks that each belongs to the definition's catalog.
	Types []*sqltype.Descriptor
}

// Form is a node's SQL shape.
type Form uint8

const (
	// FormNone has no SQL form.
	FormNone Form = iota
	// FormLeaf displays Text.
	FormLeaf
	// FormCall displays Name(arg, ...), with labels and VARIADIC.
	FormCall
	// FormInfix displays Args[0] operator Args[1].
	FormInfix
	// FormTemplate displays Pieces, the keyword syntax of SQL-standard calls.
	FormTemplate
)

// Quote is how the deparser quotes a name or label.
type Quote uint8

const (
	// QuoteOnRequest quotes only under quote_all_identifiers.
	QuoteOnRequest Quote = iota
	// QuoteVerbatim displays the text as written; nodes pre-quote fixed names
	// that PostgreSQL always quotes, such as "substring".
	QuoteVerbatim
	// QuoteIdentifier quotes when needed and under quote_all_identifiers.
	QuoteIdentifier
	// QuoteWhenNeeded quotes only when the identifier needs it, regardless of
	// quote_all_identifiers.
	QuoteWhenNeeded
)

// Grouping is how the deparser parenthesizes a FormInfix operator.
type Grouping uint8

const (
	// GroupPrecedence is the ordinary operator rule: operands see the operator
	// as their parent, and the operator is grouped outside pretty mode or by
	// precedence against its own parent.
	GroupPrecedence Grouping = iota
	// GroupNested renders operands without an operator parent and groups the
	// operator only by precedence against its own parent, in every mode.
	GroupNested
	// GroupAlways always parenthesizes the operator.
	GroupAlways
)

// RenderArg is one argument or operand and how the deparser displays it.
type RenderArg struct {
	Expr Expr
	// Label is the named-notation label; "" is positional.
	Label string
	// HideImplicit hides the argument's implicit coercions, as keyword syntax
	// and operator operands do; call arguments show them.
	HideImplicit bool
	// JSONFormattedSource renders the original child of one recognized
	// formatted-JSON input conversion. The engine owns that conversion.
	JSONFormattedSource bool
	// Listed renders the argument in a function-argument position, where
	// pretty mode drops parentheses around COLLATE and nested casts.
	Listed bool
	// UnknownText displays an untyped text literal without its annotation;
	// it applies when the Rendering sets UnknownTexts.
	UnknownText bool
	// Cast is a display-only cast to the argument's resolved formal type;
	// a nil Cast.Type displays none.
	Cast sqltype.Usage
	// CastForm is when the displayed cast keeps the argument unparenthesized
	// in pretty mode.
	CastForm CastForm
	// Transport is how replay keeps the argument's type.
	Transport Transport
	// TransportType is the TransportCast target.
	TransportType sqltype.Usage
	// Absorbed marks a replay argument that the replay form spells through its
	// own inputs: stored SQL records its routine but does not render it.
	Absorbed bool
}

// Transport is how stored scalar SQL keeps an argument's type on replay,
// where its literal spelling alone would resolve differently. Every kind but
// TransportCast leaves arguments alone while the renderer transports
// literals privately (CHECK rebinding).
type Transport uint8

const (
	// TransportNone writes the argument as rendered.
	TransportNone Transport = iota
	// TransportCast always casts the argument to TransportType, selecting the
	// stored overload.
	TransportCast
	// TransportName casts a NAME literal to NAME.
	TransportName
	// TransportLiteral casts a typed literal of a non-domain type to its type.
	TransportLiteral
	// TransportAmbiguous casts a typed literal of a non-domain type whose SQL
	// spelling does not already select it: anything except text, integer,
	// boolean, regclass, regtype, operator and text-search aliases and bit.
	TransportAmbiguous
	// TransportAmbiguousText is TransportAmbiguous that also casts text.
	TransportAmbiguousText
	// TransportIntervalNulls writes NULL interval literals anywhere in the
	// argument as typed NULLs.
	TransportIntervalNulls
)

// CastForm is when a displayed argument cast leaves its argument bare in
// pretty mode.
type CastForm uint8

const (
	// CastParenthesized always parenthesizes the argument.
	CastParenthesized CastForm = iota
	// CastLeaf leaves column references and literals bare.
	CastLeaf
	// CastLeafOrCoercion also leaves coercions bare.
	CastLeafOrCoercion
)

// Piece is one part of a FormTemplate: literal SQL text, a word the renderer
// quotes as an identifier (EXTRACT's field), an argument, or the text value of
// a literal argument used as a keyword (NORMALIZE's form).
type Piece struct {
	Text       string
	Identifier bool
	Arg        int
	Value      bool
	// Returning is an optional engine-supplied type declaration, surrounded by
	// Text and Suffix. Without a declaration the entire piece is omitted.
	Returning bool
	Suffix    string
}

// TextPiece is literal SQL text.
func TextPiece(text string) Piece { return Piece{Text: text} }

// IdentifierPiece is a word displayed as an identifier, quoted when needed.
func IdentifierPiece(word string) Piece { return Piece{Text: word, Identifier: true} }

// ArgPiece displays Args[i].
func ArgPiece(i int) Piece { return Piece{Arg: i} }

// ValuePiece displays the text value of the literal Args[i].
func ValuePiece(i int) Piece { return Piece{Arg: i, Value: true} }
