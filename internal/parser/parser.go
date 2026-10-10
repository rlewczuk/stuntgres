package parser

//go:generate sh generate.sh

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// Parse consumes a complete SQL script. Execution policy (including the
// single-statement limit for normal driver calls) belongs to the caller.
func Parse(query string) ([]Statement, error) {
	return ParseWithOptions(query, Options{StandardConformingStrings: true})
}

// Options are captured per parse; prepared literals retain that parse's mode.
type Options struct {
	StandardConformingStrings bool
	Context                   context.Context
}

func ParseWithOptions(query string, options Options) ([]Statement, error) {
	l := &lexer{input: query, options: options, backslashEscapes: !options.StandardConformingStrings}
	if options.Context != nil {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}
	}
	invalidOffset := -1
	if !utf8.ValidString(query) {
		for offset := 0; offset < len(query); {
			r, size := utf8.DecodeRuneInString(query[offset:])
			if r == utf8.RuneError && size == 1 {
				invalidOffset = offset
				break
			}
			offset += size
		}
	}
	if nulOffset := strings.IndexByte(query, 0); nulOffset >= 0 &&
		(invalidOffset < 0 || nulOffset < invalidOffset) {
		l.failAt("42601", "NUL in SQL", nulOffset)
		return nil, l.err
	}
	if invalidOffset >= 0 {
		l.failAt("42601", "invalid UTF-8 in SQL", invalidOffset)
		return nil, l.err
	}
	if sqlParse(l) != 0 || l.err != nil {
		return nil, l.err
	}
	if l.pendingMessage != "" {
		l.failAt("0A000", l.pendingMessage, l.pendingOffset)
		return nil, l.err
	}
	for i, statement := range l.statements {
		if query, ok := statement.(*Select); ok {
			first := LeftmostSelect(query)
			if first != nil && first.Into != nil {
				destination := *first.Into
				first.Into = nil
				statement = &CreateQueryTable{Name: destination, Query: query, Pos: query.Pos, Form: SelectInto}
				l.statements[i] = statement
			}
		}
		var viewQuery *Select
		if view, ok := statement.(*CreateView); ok {
			viewQuery = LeftmostSelect(view.Query)
		}
		if err := VisitQuerySources(statement, func(query *Select) error {
			if query.Set != nil {
				first := LeftmostSelect(query)
				if first.Into == nil || first == viewQuery {
					if into := nonFirstSetInto(query, first); into != nil {
						l.failAt("42601", "INTO is only allowed on first SELECT of UNION/INTERSECT/EXCEPT", into.Parts[0].Pos.Offset)
						return l.err
					}
				}
			}
			if query.Into != nil && query != viewQuery {
				l.failAt("42601", "SELECT ... INTO is not allowed here", query.Into.Parts[0].Pos.Offset)
				return l.err
			}
			return nil
		}, nil); err != nil {
			return nil, err
		}
	}
	return l.statements, nil
}

// The complete INSERT source frontier distinguishes pure VALUES from genuine
// set operands. Replay only the bounded rows through the restricted grammar,
// retaining parentheses and ORDER/bounds on the original source. Set arms keep
// query expressions; original input, options and absolute positions stay authoritative.
func (l *lexer) validateInsertValues(values *ValuesQuery) bool {
	replay := &lexer{input: l.input, offset: values.Pos.Offset + len("values"), options: l.options,
		backslashEscapes: l.backslashEscapes, insertScalarEntry: true, insertScalarEnd: values.rowEnd}
	if sqlParse(replay) != 0 || replay.err != nil {
		l.err = replay.err
		return false
	}
	if replay.pendingMessage != "" {
		replay.failAt("0A000", replay.pendingMessage, replay.pendingOffset)
		l.err = replay.err
		return false
	}
	values.Rows = replay.statements[0].(*Insert).Rows
	return true
}

func nonFirstSetInto(query, first *Select) *ObjectName {
	if query.Set == nil {
		if query != first {
			return query.Into
		}
		return nil
	}
	if into := nonFirstSetInto(query.Set.Left, first); into != nil {
		return into
	}
	return nonFirstSetInto(query.Set.Right, first)
}

// Run only at the complete statement-query frontier: an enclosing set tail can
// supply the locking clause that requires retaining an arm's DISTINCT ON syntax.
func deferDistinctOnBoundary(l *lexer, s *Select) {
	on, marked := queryDistinctOnShape(s)
	if on != nil && !marked {
		l.deferUnsupported(lexeme{pos: on.Pos}, "SELECT DISTINCT ON is unsupported")
	}
}

func queryDistinctOnShape(s *Select) (*DistinctClause, bool) {
	if s == nil {
		return nil, false
	}
	if len(s.Locking) != 0 {
		return nil, true
	}
	var on *DistinctClause
	if s.Distinct != nil && len(s.Distinct.On) != 0 {
		on = s.Distinct
	}
	if s.Set != nil {
		left, leftMarked := queryDistinctOnShape(s.Set.Left)
		right, rightMarked := queryDistinctOnShape(s.Set.Right)
		if leftMarked || rightMarked {
			return nil, true
		}
		if on == nil {
			on = left
		}
		if on == nil {
			on = right
		}
	}
	return on, false
}

type lexeme struct {
	text           string
	raw            string
	pos            Position
	keyword        *keyword
	numeric        bool
	generic        bool
	resolutionOnly bool
}

func integerValue(l sqlLexer, tok lexeme, sign string) (Value, bool) {
	return numberValue(tok, sign), true
}

func numberValue(tok lexeme, sign string) Value {
	if tok.numeric {
		return Value{Kind: NumericValue, Text: sign + tok.text, Pos: tok.pos}
	}
	base, offset := uint64(10), 0
	if len(tok.text) > 2 && tok.text[0] == '0' {
		switch tok.text[1] {
		case 'b', 'B':
			base, offset = 2, 2
		case 'o', 'O':
			base, offset = 8, 2
		case 'x', 'X':
			base, offset = 16, 2
		}
	}
	limit := uint64(1<<63 - 1)
	if sign == "-" {
		limit++
	}
	var magnitude uint64
	for ; offset < len(tok.text); offset++ {
		if tok.text[offset] == '_' {
			continue
		}
		d := numericDigit(tok.text[offset])
		if magnitude > (limit-d)/base {
			return Value{Kind: NumericValue, Text: sign + tok.text, Pos: tok.pos}
		}
		magnitude = magnitude*base + d
	}
	var n int64
	if sign == "-" && magnitude != 0 {
		// Map magnitude 2^63 to MinInt64 without overflowing conversion or
		// negation; zero uses the ordinary conversion below.
		n = -int64(magnitude-1) - 1
	} else {
		n = int64(magnitude)
	}
	return Value{Kind: IntegerValue, Integer: n, Pos: tok.pos}
}

func arithmeticExpr(tok lexeme, left, right Expr) Expr {
	args := []Expr{left}
	if right != nil {
		args = append(args, right)
	}
	return OperatorExpr{Name: ObjectName{Parts: []Identifier{{Text: tok.text, Pos: tok.pos}}}, Args: args, Pos: tok.pos}
}

// PostgreSQL lowers each occurrence independently, preserving binary typing
// and three-valued logic rather than sorting or coercing the bounds together.
func betweenExpr(a, lower, upper Expr, pos Position, negated, symmetric bool) Expr {
	first, second, inner, outer := ">=", "<=", LogicalAnd, LogicalOr
	if negated {
		first, second, inner, outer = "<", ">", LogicalOr, LogicalAnd
	}
	comparison := func(op string, right Expr) Expr {
		return OperatorExpr{Name: ObjectName{Parts: []Identifier{{Text: op, Pos: pos}}}, Args: []Expr{a, right}, Pos: pos}
	}
	branch := func(l, u Expr) Expr {
		return LogicalExpr{Op: inner, Left: comparison(first, l), Right: comparison(second, u), Pos: pos}
	}
	forward := branch(lower, upper)
	if !symmetric {
		return forward
	}
	return LogicalExpr{Op: outer, Left: forward, Right: branch(upper, lower), Pos: pos}
}

// PostgreSQL folds numeric literal negation, including grouped constants.
func negateExpr(l sqlLexer, tok lexeme, child Expr) Expr {
	literal, ok := child.(LiteralExpr)
	if !ok {
		return arithmeticExpr(tok, child, nil)
	}
	v := literal.Value
	switch v.Kind {
	case IntegerValue:
		if v.Integer == -1<<63 {
			v.Kind = NumericValue
			v.Integer = 0
			v.Text = "9223372036854775808"
		} else {
			v.Integer = -v.Integer
		}
	case NumericValue:
		text := v.Text
		sign := "-"
		if strings.HasPrefix(text, "-") {
			text = text[1:]
			sign = ""
		} else {
			text = strings.TrimPrefix(text, "+")
		}
		numeric := strings.ContainsAny(text, ".eE") && !(strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X"))
		v, _ = integerValue(l, lexeme{text: text, numeric: numeric, pos: tok.pos}, sign)
	default:
		return arithmeticExpr(tok, child, nil)
	}
	v.Pos = tok.pos
	return LiteralExpr{Value: v}
}

func settingInteger(l sqlLexer, tok lexeme, negative bool) (string, bool) {
	text := tok.text
	if tok.numeric {
		l.(*lexer).failAt("0A000", "non-decimal-integer literal is unsupported", tok.pos.Offset)
		return "", false
	}
	// NUMBER is shared with INSERT/SELECT, which also accept radix/separator
	// spellings. Settings deliberately support decimal digits only.
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			l.(*lexer).failAt("0A000", "non-decimal setting integers are unsupported", tok.pos.Offset)
			return "", false
		}
	}
	// PostgreSQL's scanner canonicalizes an unsigned int32 integer token.
	// Larger decimal tokens retain their original spelling as NumericOnly text;
	// the unary sign is applied separately, without broadening numeric syntax.
	if n, err := strconv.ParseInt(text, 10, 32); err == nil {
		if negative {
			n = -n
		}
		return strconv.FormatInt(n, 10), true
	}
	if negative {
		return "-" + text, true
	}
	return text, true
}

func (l *lexer) fail(code, message string) {
	l.failAt(code, message, l.current.pos.Offset)
}

func (l *lexer) failAt(code, message string, offset int) {
	l.failDiagnostic(code, message, "", offset)
}

// A negative offset deliberately omits Position (decoded encoding errors).
func (l *lexer) failDiagnostic(code, message, hint string, offset int) {
	if l.err == nil {
		e := &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: code, Message: message, Hint: hint,
		}
		if offset >= 0 {
			// Unicode decoding can place the release-derived byte limit inside
			// a source rune after dequoting/continuation. Count rune starts in
			// the complete input, including the rune crossing that limit.
			position := 1
			for start := range l.input {
				if start >= offset {
					break
				}
				position++
			}
			e.Position = strconv.Itoa(position)
		}
		l.err = e
	}
}

func (l *lexer) syntax(tok lexeme) {
	message := "syntax error at end of input"
	if tok.raw != "" {
		message = fmt.Sprintf("syntax error at or near \"%s\"", tok.raw)
	}
	l.failAt("42601", message, tok.pos.Offset)
}

func (l *lexer) Error(_ string) { l.syntax(l.current) }

func (l *lexer) deferUnsupported(tok lexeme, message string) {
	if l.pendingMessage == "" {
		l.pendingMessage, l.pendingOffset = message, tok.pos.Offset
	}
}

// These are capability boundaries in explicit grammar positions, not lexical
// classifications. Quoted names never acquire keyword identity.
var unsupportedCommands = wordSet("truncate drop alter explain copy vacuum analyze analyse with execute deallocate discard listen notify unlisten cluster refresh reset")
var unsupportedCreateObjects = wordSet("temporary temp unlogged view index unique database function procedure trigger")
var unsupportedSuffixes = wordSet("union intersect except join inner left right full cross natural on returning group having window for using primary unique check references default constraint collate in")
var unsupportedCommentKinds = wordSet("type database index view collation conversion extension language publication subscription tablespace trigger policy rule statistics")

// Initial membership clauses are supported; legacy role options remain excluded.
var unsupportedRoleOptions = wordSet("group granted by sysid")

func (l *lexer) capability(tok lexeme, words map[string]bool, message string) {
	if tok.keyword != nil && words[tok.text] {
		l.failAt("0A000", message, tok.pos.Offset)
	} else {
		l.syntax(tok)
	}
}

// These C-keywords have dedicated release expression productions, not generic
// function-name legality. Recognizing the capability never executes a call.
var specialCalls = wordSet("coalesce greatest grouping least nullif row treat xmlconcat xmlelement xmlexists xmlforest xmlparse xmlpi xmlroot xmlserialize json json_array json_arrayagg json_exists json_object json_objectagg json_query json_scalar json_serialize json_value merge_action exists")

func (l *lexer) call(callee, opener lexeme) {
	if callee.keyword.typeFunctionName() {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else if callee.keyword != nil && specialCalls[callee.text] {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else {
		l.syntax(opener) // The name was valid as ColId; '(' is not.
	}
}

func ordinaryCastFunction(callee lexeme) bool {
	switch callee.text {
	case "date", "timestamptz", "timetz", "oid", "int4", "int8", "text", "bool", "count", "sum", "avg", "bool_and", "bool_or", "every", "any_value", "string_agg", "min", "max", "row_number":
		return true
	case "integer", "bigint", "boolean", "int":
		return callee.keyword == nil // Quoted alias diagnostics belong to binding.
	}
	return false
}

func (l *lexer) castFunction(callee, opener lexeme) bool {
	if ordinaryCastFunction(callee) {
		if callee.text == "date" || callee.text == "timestamptz" || callee.text == "timetz" {
			l.genericModifier = true
			l.dateLiteralModifier = true
		}
		return true
	}
	l.call(callee, opener)
	return false
}

func ordinaryAggregate(name string) bool {
	return name == "count" || name == "sum" || name == "avg" || name == "bool_and" || name == "bool_or" || name == "every" || name == "any_value" || name == "string_agg" || name == "min" || name == "max" || name == "row_number"
}

func (l *lexer) scalarInput(expr Expr) bool {
	// Raw qualification errors precede scalar consumer checks, including a
	// later invalid cast beneath an otherwise unsupported conditional child.
	if token, _ := nonqueryQualification(expr, false); token != nil {
		l.syntax(*token)
		return false
	}
	return l.scalarInputTree(expr)
}

func (l *lexer) scalarInputTree(expr Expr) bool {
	switch r := expr.(type) {
	case ArrayExpr:
		for _, element := range r.Elements {
			if !l.scalarInputTree(element) {
				return false
			}
		}
		return true
	case FunctionExpr:
		for _, arg := range r.Args {
			if !l.scalarInputTree(arg) {
				return false
			}
		}
		if r.Window != nil {
			for _, key := range r.Window.Partition {
				if !l.scalarInputTree(key) {
					return false
				}
			}
			for _, key := range r.Window.Order {
				if !l.scalarInputTree(key.Expr) {
					return false
				}
			}
		}
		return true
	case GroupingExpr:
		return true // Dedicated binder owns child analysis and clause diagnostics.
	case AggregateExpr:
		if r.Name == "sum" {
			// SUM analyzes children and FILTER before its VALUES clause error.
			// Other scalar consumers retain their existing admission boundary.
			return true
		}
		for _, arg := range r.Args {
			if !l.scalarInputTree(arg) {
				return false
			}
		}
		for _, key := range r.Order {
			if !l.scalarInputTree(key.Expr) {
				return false
			}
		}
		if r.Window != nil {
			for _, key := range r.Window.Partition {
				if !l.scalarInputTree(key) {
					return false
				}
			}
			for _, key := range r.Window.Order {
				if !l.scalarInputTree(key.Expr) {
					return false
				}
			}
		}
		return r.Filter == nil || l.scalarInputTree(r.Filter)
	case CaseExpr:
		if r.Test != nil && !l.scalarInputTree(r.Test) {
			return false
		}
		for _, arm := range r.Arms {
			if !l.scalarInputTree(arm.Condition) || !l.scalarInputTree(arm.Result) {
				return false
			}
		}
		return r.Else == nil || l.scalarInputTree(r.Else)
	case ConditionalExpr:
		for _, child := range r.Args {
			if !l.scalarInputTree(child) {
				return false
			}
		}
		return true
	case LogicalExpr:
		return l.scalarInputTree(r.Left) && (r.Right == nil || l.scalarInputTree(r.Right))
	case PredicateExpr:
		return l.scalarInputTree(r.Left) && (r.Right == nil || l.scalarInputTree(r.Right))
	case CollateExpr:
		return l.scalarInputTree(r.Child)
	case CastExpr:
		return l.scalarInputTree(r.Child)
	case TimeZoneExpr:
		return l.scalarInputTree(r.Value) && (r.Local || l.scalarInputTree(r.Zone))
	case OperatorExpr:
		for _, child := range r.Args {
			if !l.scalarInputTree(child) {
				return false
			}
		}
		return true
	case ColumnExpr:
		l.failAt("0A000", "column references are unsupported in scalar inputs", r.Pos.Offset)
		return false
	default:
		return true
	}
}

func columnType(tok lexeme) TypeName {
	if strings.HasPrefix(tok.raw, `"`) || strings.HasPrefix(tok.raw, `U&"`) || strings.HasPrefix(tok.raw, `u&"`) {
		return TypeName{Name: tok.text, Pos: tok.pos, Quoted: true}
	}
	switch tok.text {
	case "int2":
		return TypeName{Name: "smallint", Pos: tok.pos}
	case "int4":
		return TypeName{Name: "integer", Pos: tok.pos}
	case "int8":
		return TypeName{Name: "bigint", Pos: tok.pos}
	case "bool":
		return TypeName{Name: "boolean", Pos: tok.pos}
	}
	return TypeName{Name: tok.text, Pos: tok.pos}
}

func floatType(l sqlLexer, name, precision lexeme) (TypeName, bool) {
	raw := columnType(name)
	n, err := strconv.ParseUint(strings.ReplaceAll(precision.text, "_", ""), 0, 31)
	// Decimal precision with leading zero is still decimal, not Go octal.
	if !strings.HasPrefix(precision.text, "0x") && !strings.HasPrefix(precision.text, "0X") && !strings.HasPrefix(precision.text, "0o") && !strings.HasPrefix(precision.text, "0O") && !strings.HasPrefix(precision.text, "0b") && !strings.HasPrefix(precision.text, "0B") {
		n, err = strconv.ParseUint(strings.ReplaceAll(precision.text, "_", ""), 10, 31)
	}
	if err != nil {
		l.(*lexer).syntax(precision)
		return raw, false
	}
	if n == 0 {
		l.(*lexer).failAt("22023", "precision for type float must be at least 1 bit", precision.pos.Offset)
		return raw, false
	}
	if n > 53 {
		l.(*lexer).failAt("22023", "precision for type float must be less than 54 bits", precision.pos.Offset)
		return raw, false
	}
	raw.Name = "float8"
	if n <= 24 {
		raw.Name = "float4"
	}
	if l.(*lexer).typeInputMode {
		raw.System = true
	}
	return raw, true
}

func temporalPrecision(l sqlLexer, tok lexeme, negative bool) (TypeName, bool) {
	if l.(*lexer).characterPrecision && (negative || tok.numeric) {
		l.(*lexer).syntax(tok)
		return TypeName{}, false
	}
	if tok.numeric {
		text := tok.text
		if negative {
			text = "-" + text
		}
		return TypeName{Modifiers: []TypeModifier{{Raw: text, Pos: tok.pos}}}, true
	}
	text := strings.ReplaceAll(tok.text, "_", "")
	base := 10
	if len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])) {
		base = 0
	}
	if negative {
		text = "-" + text
	}
	n, err := strconv.ParseInt(text, base, 32)
	if err != nil {
		if tok.generic {
			raw := tok.text
			if negative {
				raw = "-" + raw
			}
			return TypeName{Modifiers: []TypeModifier{{Raw: raw, Pos: tok.pos}}}, true
		}
		l.(*lexer).syntax(tok)
		return TypeName{}, false
	}
	p := int32(n)
	return TypeName{Modifiers: []TypeModifier{{Integer: &p, Pos: tok.pos}}}, true
}
func intervalField(mask int32, pos Position) TypeName {
	return TypeName{Modifiers: []TypeModifier{{Integer: &mask, Pos: pos}}}
}
func intervalPrecision(l sqlLexer, mask int32, tok, open lexeme) (TypeName, bool) {
	if tok.numeric {
		l.(*lexer).syntax(tok)
		return TypeName{}, false
	}
	raw, ok := temporalPrecision(l, tok, false)
	if !ok {
		return raw, false
	}
	field := intervalField(mask, open.pos)
	field.Modifiers = append(field.Modifiers, raw.Modifiers...)
	return field, true
}
func typeModifierHead(l sqlLexer, tok, open lexeme, qualified bool) (TypeName, bool) {
	raw := columnType(tok)
	if l.(*lexer).typeInputMode {
		raw = inputColumnType(l, tok)
		if qualified || raw.Quoted || tok.keyword == nil || raw.Name != "float" && raw.Name != "char" && raw.Name != "character" && raw.Name != "varchar" && raw.Name != "character varying" && raw.Name != "bit" && raw.Name != "bit varying" {
			l.(*lexer).characterGeneric = true
			l.(*lexer).genericModifier = true
			l.(*lexer).multipleModifiers = true
			l.(*lexer).bitModifier = true
			return raw, true
		}
	}
	if raw.Name == "bit" || raw.Name == "varbit" || raw.Name == "bit varying" {
		l.(*lexer).bitModifier = true
		l.(*lexer).characterGeneric = true
		l.(*lexer).genericModifier = true
		l.(*lexer).multipleModifiers = true
		return raw, true
	}
	if qualified {
		// Only catalog resolution can distinguish a builtin-spelled user domain.
		l.(*lexer).genericModifier = true
		l.(*lexer).multipleModifiers = true
		return raw, true
	}
	if tok.keyword != nil && tok.text == "json" {
		l.(*lexer).syntax(open)
		return TypeName{}, false
	}
	if tok.keyword != nil && tok.text == "float" {
		l.(*lexer).floatPrecision = true
		return raw, true
	}
	if !raw.Quoted && (raw.Name == "char" || raw.Name == "character" || raw.Name == "varchar" || raw.Name == "character varying") {
		l.(*lexer).characterPrecision = true
		l.(*lexer).multipleModifiers = false
		return raw, true
	}
	if raw.Name == "bpchar" || raw.Name == "varchar" || raw.Name == "text" || raw.Name == "name" {
		l.(*lexer).characterGeneric = true
		l.(*lexer).genericModifier = true
		l.(*lexer).multipleModifiers = true
		return raw, true
	}
	if raw.Name == "timestamptz" || raw.Name == "timetz" {
		l.(*lexer).multipleModifiers = false
		return raw, true
	}
	switch raw.Name {
	case "smallserial", "serial2", "serial", "serial4", "bigserial", "serial8":
		l.(*lexer).genericModifier = true
		l.(*lexer).multipleModifiers = true
		return raw, true
	}
	switch raw.Name {
	case "smallint", "integer", "bigint", "int2", "int4", "int8", "boolean", "bool", "real", "float4", "float8", "double precision", "uuid", "oid", "regclass", "pg_node_tree", "void", "unknown", "_name", "char", "character", "character varying":
		l.(*lexer).failAt("0A000", "type modifiers are unsupported", open.pos.Offset)
		return TypeName{}, false
	}
	l.(*lexer).genericModifier = true
	l.(*lexer).multipleModifiers = raw.Name != "bytea" && raw.Name != "date" && raw.Name != "timestamp" && raw.Name != "time"
	return raw, true
}

func excludedTypeTail(first, tail lexeme) bool {
	if first.keyword == nil || tail.keyword == nil {
		return false
	}
	switch first.text {
	case "time", "timestamp":
		return tail.text == "with" || tail.text == "without"
	}
	return false
}

func validLikeOption(name string) bool {
	switch name {
	case "comments", "compression", "constraints", "defaults", "generated", "identity", "indexes", "statistics", "storage", "all":
		return true
	}
	return false
}

func settingCall(l sqlLexer, name lexeme, args []Expr) (Statement, bool) {
	n := name.text
	if n != "current_setting" && n != "set_config" {
		l.(*lexer).failAt("0A000", "functions are unsupported", name.pos.Offset)
		return nil, false
	}
	valid := n == "current_setting" && len(args) == 1 || n == "set_config" && len(args) == 3
	for i, raw := range args {
		switch v := raw.(type) {
		case ParameterExpr:
		case TypedLiteralExpr:
		case LiteralExpr:
			if v.Value.Kind != NullValue && v.Value.Kind != StringValue && !(i == 2 && v.Value.Kind == BooleanValue) {
				valid = false
			}
		default:
			valid = false
		}
	}
	if !valid {
		l.(*lexer).failAt("0A000", "unsupported setting function signature", name.pos.Offset)
		return nil, false
	}
	return &SettingCall{Name: n, Args: args, Pos: name.pos}, true
}

// Accumulate dotted names and join once, avoiding quadratic concatenation.
// Ordinary one-component settings do not allocate an intermediate slice.
type settingName struct {
	first lexeme
	parts []string
}

func (n settingName) identifier() Identifier {
	text := n.first.text
	if n.parts != nil {
		text = strings.Join(n.parts, ".")
	}
	return Identifier{Text: text, Pos: n.first.pos}
}
