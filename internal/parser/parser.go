package parser

//go:generate go run golang.org/x/tools/cmd/goyacc@v0.50.0 -l -p sql -o grammar_gen.go -v /dev/null grammar.y

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
	// RestoreDefaultValues is private canonical-control syntax, not SQL defaults.
	RestoreDefaultValues bool
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
	return l.statements, nil
}

type lexeme struct {
	text    string
	raw     string
	pos     Position
	keyword *keyword
}

func integerValue(l sqlLexer, tok lexeme, sign string) (Value, bool) {
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
			l.(*lexer).failAt("22003", "integer literal out of range", tok.pos.Offset)
			return Value{}, false
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
	return Value{Kind: IntegerValue, Integer: n, Pos: tok.pos}, true
}

func settingInteger(l sqlLexer, tok lexeme, negative bool) (string, bool) {
	text := tok.text
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
var unsupportedCommands = wordSet("update delete merge truncate drop alter explain copy vacuum analyze analyse grant revoke with execute deallocate discard listen notify unlisten reindex cluster refresh reset")
var unsupportedCreateObjects = wordSet("temporary temp unlogged view index database sequence function procedure trigger")
var unsupportedSuffixes = wordSet("union intersect except join inner left right full cross natural on returning limit offset fetch group having window for using primary unique check references default constraint collate and or is in between like ilike similar")
var unsupportedCommentKinds = wordSet("type database index sequence view collation conversion extension language publication subscription tablespace trigger policy rule statistics")

// LOGIN attributes are contextual role-option words, not PostgreSQL keywords.
var unsupportedRoleOptions = wordSet("superuser nosuperuser createrole nocreaterole createdb nocreatedb inherit noinherit replication noreplication bypassrls nobypassrls password connection valid admin in role user group granted by")

func (l *lexer) capability(tok lexeme, words map[string]bool, message string) {
	if tok.keyword != nil && words[tok.text] {
		l.failAt("0A000", message, tok.pos.Offset)
	} else {
		l.syntax(tok)
	}
}

// These C-keywords have dedicated release expression productions, not generic
// function-name legality. Recognizing the capability never executes a call.
var specialCalls = wordSet("coalesce extract greatest grouping least normalize nullif overlay position row substring treat trim xmlconcat xmlelement xmlexists xmlforest xmlparse xmlpi xmlroot xmlserialize json json_array json_arrayagg json_exists json_object json_objectagg json_query json_scalar json_serialize json_value merge_action exists")

func (l *lexer) call(callee, opener lexeme) {
	if callee.keyword.typeFunctionName() {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else if callee.keyword != nil && specialCalls[callee.text] {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else {
		l.syntax(opener) // The name was valid as ColId; '(' is not.
	}
}

func columnType(tok lexeme) TypeName {
	if strings.HasPrefix(tok.raw, `"`) || strings.HasPrefix(tok.raw, `U&"`) || strings.HasPrefix(tok.raw, `u&"`) {
		return TypeName{Name: tok.text, Pos: tok.pos, Quoted: true}
	}
	switch tok.text {
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
	return raw, true
}

func timestampPrecision(l sqlLexer, tok lexeme, negative bool) (TypeName, bool) {
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
		l.(*lexer).syntax(tok)
		return TypeName{}, false
	}
	p := int32(n)
	return TypeName{Precision: &p, PrecisionPos: tok.pos}, true
}
func typeModifierHead(l sqlLexer, tok, open lexeme) (TypeName, bool) {
	raw := columnType(tok)
	if tok.keyword != nil && tok.text == "float" {
		l.(*lexer).floatPrecision = true
		return raw, true
	}
	if !raw.Quoted || raw.Name != "timestamp" {
		l.(*lexer).failAt("0A000", "type modifiers are unsupported", open.pos.Offset)
		return TypeName{}, false
	}
	return raw, true
}

func excludedTypeTail(first, tail lexeme) bool {
	if first.keyword == nil || tail.keyword == nil {
		return false
	}
	switch first.text {
	case "character", "bit":
		return tail.text == "varying"
	case "time", "timestamp":
		return tail.text == "with" || tail.text == "without"
	case "interval":
		switch tail.text {
		case "year", "month", "day", "hour", "minute", "second":
			return true
		}
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
