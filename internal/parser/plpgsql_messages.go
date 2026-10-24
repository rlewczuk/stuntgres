package parser

// PLRaise keeps presence separate from empty format text and preserves every
// option occurrence. Expressions retain their original SQL source boundaries.
type PLRaise struct {
	Level PLRaiseLevel
	Form PLRaiseForm
	Format string
	Condition PLCondition
	Arguments []PLExpression
	Options []PLRaiseOption
}

type PLRaiseLevel uint8
const (
	PLRaiseException PLRaiseLevel = iota
	PLRaiseDebug
	PLRaiseLog
	PLRaiseInfo
	PLRaiseNotice
	PLRaiseWarning
)

type PLRaiseForm uint8
const (
	PLRaiseFormat PLRaiseForm = iota
	PLRaiseCondition
	PLRaiseSQLState
	PLRaiseUsing
)

type PLRaiseOptionKind uint8
const (
	PLRaiseMessage PLRaiseOptionKind = iota
	PLRaiseDetail
	PLRaiseHint
	PLRaiseErrcode
	PLRaiseColumn
	PLRaiseConstraint
	PLRaiseDatatype
	PLRaiseTable
	PLRaiseSchema
)

func (k PLRaiseOptionKind) String() string {
	switch k {
	case PLRaiseMessage: return "MESSAGE"
	case PLRaiseDetail: return "DETAIL"
	case PLRaiseHint: return "HINT"
	case PLRaiseErrcode: return "ERRCODE"
	case PLRaiseColumn: return "COLUMN"
	case PLRaiseConstraint: return "CONSTRAINT"
	case PLRaiseDatatype: return "DATATYPE"
	case PLRaiseTable: return "TABLE"
	case PLRaiseSchema: return "SCHEMA"
	default: return ""
	}
}

type PLRaiseOption struct {
	Kind PLRaiseOptionKind
	Pos Position
	Value PLExpression
}

type PLAssert struct {
	Condition PLExpression
	Message PLExpression
}

func plRaiseLevel(token lexeme) PLRaiseLevel {
	switch token.text {
	case "debug": return PLRaiseDebug
	case "log": return PLRaiseLog
	case "info": return PLRaiseInfo
	case "notice": return PLRaiseNotice
	case "warning": return PLRaiseWarning
	default: return PLRaiseException
	}
}

func (l *lexer) plRaiseOption(token lexeme) PLRaiseOption {
	option := PLRaiseOption{Pos: token.pos}
	switch token.text {
	case "message": option.Kind = PLRaiseMessage
	case "detail": option.Kind = PLRaiseDetail
	case "hint": option.Kind = PLRaiseHint
	case "errcode": option.Kind = PLRaiseErrcode
	case "column": option.Kind = PLRaiseColumn
	case "constraint": option.Kind = PLRaiseConstraint
	case "datatype": option.Kind = PLRaiseDatatype
	case "table": option.Kind = PLRaiseTable
	case "schema": option.Kind = PLRaiseSchema
	default:
		l.failAt("42601", "unrecognized RAISE statement option at or near \""+token.raw+"\"", token.pos.Offset)
	}
	return option
}
