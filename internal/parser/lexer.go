package parser

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

type lexer struct {
	input              string
	offset             int
	positionOffset     int
	positionCharacters int
	current            lexeme
	// Scanner/lookahead reuse current; this records only tokens returned to yacc.
	previousToken int
	// SQL EXTRACT admits a field word, not an ordinary scalar call head.
	extractField bool
	err          error
	statements   []Statement
	// Exact grammar row-end evidence bounds private INSERT scalar replay.
	queryRowEnd       int
	insertScalarEntry bool
	insertScalarEnd   int
	typeInputEntry    bool
	typeInputMode     bool
	typeInputResult   TypeName
	typeInputNotice   func(sqlstate.Error)
	options           Options
	// The raw scanner defaults to standard strings; only explicit parse options
	// enable legacy backslash escapes and prohibit Unicode string openers.
	backslashEscapes bool
	// Failure-only query recognition defers its capability error until the
	// entire script is syntactically valid. It never publishes a query AST.
	pendingMessage     string
	pendingOffset      int
	lookahead          rawToken
	hasLookahead       bool
	rawUnicode         bool
	floatPrecision     bool
	characterPrecision bool
	characterGeneric   bool
	bitModifier        bool // Bit modifiers retain nonsimple arithmetic for provider diagnostics.
	// Temporal keyword forms retain their unsigned-Iconst grammar; generic
	// descriptor modifiers retain numeric spellings for subsequent int4 input.
	temporalLead        bool
	temporalModifier    bool
	genericModifier     bool
	dateLiteralModifier bool // Distinguishes raw DATE precision from cast operands.
	multipleModifiers   bool
	// CHECK alone admits existing selected scalar calls as operand leaves.
	// Depth changes only for tokens returned to yacc, never raw lookahead.
	checkPending bool
	checkDepth   int
	// Entered by an actual format call-head reduction, not a format-named
	// declaration. Only returned nested parentheses alter argument depth.
	formatDepth                                           int
	indexExpression                                       bool
	generationPending                                     bool
	generationDepth                                       int
	generationModePos                                     Position
	generationPrevious                                    lexeme
	generationRestriction, generationCompletedRestriction *GenerationRestriction
	generationWindowPending                               bool
	generationWindowDepth                                 int
}

// rawToken retains scanner evidence independently of the parser-facing token.
// Presence is separate from code so end-of-input can be buffered.
type rawToken struct {
	code    int
	lexeme  lexeme
	unicode bool
}

func (l *lexer) formatCallHead(callee lexeme) {
	if l.formatDepth == 0 && strings.EqualFold(callee.text, "format") {
		l.formatDepth = 1
	}
}

func wordSet(words string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func initial(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128
}
func continuation(c byte) bool { return initial(c) || c >= '0' && c <= '9' || c == '$' }
func digit(c byte) bool        { return c >= '0' && c <= '9' }
func fold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
func truncateName(s string) string {
	if len(s) <= 63 {
		return s
	}
	n := 63
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (l *lexer) identifierName(s string) string {
	clipped := truncateName(s)
	if clipped != s && l.typeInputNotice != nil {
		l.typeInputNotice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier \"%s\" will be truncated to \"%s\"", s, clipped), Context: fmt.Sprintf("invalid type name \"%s\"", l.input)})
	}
	return clipped
}

// skip returns whether the separator contained a newline, for SQL string
// continuation. Nested block comments are counted rather than split.
func (l *lexer) skip(allowBlock bool) bool {
	newline := false
	for l.offset < len(l.input) {
		c := l.input[l.offset]
		if space(c) {
			newline = newline || c == '\n' || c == '\r'
			l.offset++
			continue
		}
		if strings.HasPrefix(l.input[l.offset:], "--") {
			l.offset += 2
			for l.offset < len(l.input) && l.input[l.offset] != '\n' && l.input[l.offset] != '\r' {
				l.offset++
			}
			continue
		}
		if allowBlock && strings.HasPrefix(l.input[l.offset:], "/*") {
			opener := l.offset
			l.offset += 2
			depth := 1
			for depth > 0 && l.offset < len(l.input) {
				if strings.HasPrefix(l.input[l.offset:], "/*") {
					depth++
					l.offset += 2
				} else if strings.HasPrefix(l.input[l.offset:], "*/") {
					depth--
					l.offset += 2
				} else {
					c = l.input[l.offset]
					newline = newline || c == '\n' || c == '\r'
					l.offset++
				}
			}
			if depth != 0 {
				l.failAt("42601", fmt.Sprintf("unterminated /* comment at or near \"%s\"", l.input[opener:]), opener)
				return newline
			}
			continue
		}
		break
	}
	return newline
}

func (l *lexer) quotedIdentifier(opener int) (string, bool) {
	l.offset++
	var b strings.Builder
	for l.offset < len(l.input) {
		c := l.input[l.offset]
		l.offset++
		if c == 0 {
			l.fail("42601", "NUL in quoted input")
			return "", false
		}
		if c == '"' {
			if l.offset < len(l.input) && l.input[l.offset] == '"' {
				b.WriteByte(c)
				l.offset++
				continue
			}
			return b.String(), true
		}
		b.WriteByte(c)
	}
	l.scannerError("unterminated quoted identifier", opener, len(l.input))
	return "", false
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func (l *lexer) scannerError(message string, start, end int) {
	if start == len(l.input) {
		message += " at end of input"
	} else {
		message += " at or near \"" + l.input[start:end] + "\""
	}
	l.failAt("42601", message, start)
}

func (l *lexer) unicodeEscape() (rune, bool) {
	start := l.offset
	width := 4
	if l.input[start+1] == 'U' {
		width = 8
	}
	l.offset += 2
	var value uint32
	for range width {
		if l.offset == len(l.input) || hexDigit(l.input[l.offset]) < 0 {
			l.failDiagnostic("22025", "invalid Unicode escape", `Unicode escapes must be \uXXXX or \UXXXXXXXX.`, start)
			return 0, false
		}
		value = value*16 + uint32(hexDigit(l.input[l.offset]))
		l.offset++
	}
	return rune(value), true
}

// stringLiteral shares one builder across all continued segments. Octal/hex
// escapes are bytes; Unicode escapes are scalars. Verify bytes only at token end.
func (l *lexer) stringLiteral(first int, escape bool) (string, bool) {
	var b strings.Builder
	for {
		l.offset++
		closed := false
		for l.offset < len(l.input) {
			start := l.offset
			c := l.input[start]
			l.offset++
			if c == '\'' {
				if l.offset < len(l.input) && l.input[l.offset] == '\'' {
					b.WriteByte(c)
					l.offset++
					continue
				}
				closed = true
				break
			}
			if !escape || c != '\\' {
				b.WriteByte(c)
				continue
			}
			if l.offset == len(l.input) {
				break
			}
			c = l.input[l.offset]
			if c == 'u' || c == 'U' {
				l.offset = start
				r, ok := l.unicodeEscape()
				if !ok {
					return "", false
				}
				if r >= 0xD800 && r <= 0xDBFF {
					second := l.offset
					if second+1 >= len(l.input) || l.input[second] != '\\' || (l.input[second+1] != 'u' && l.input[second+1] != 'U') {
						end := second
						if end < len(l.input) {
							// PostgreSQL's unmatched surrogate rule matches one
							// source byte, even inside a multibyte UTF8 character.
							end++
						}
						l.scannerError("invalid Unicode surrogate pair", second, end)
						return "", false
					}
					low, ok := l.unicodeEscape()
					if !ok {
						return "", false
					}
					if low < 0xDC00 || low > 0xDFFF {
						l.scannerError("invalid Unicode surrogate pair", second, l.offset)
						return "", false
					}
					r = 0x10000 + (r-0xD800)*0x400 + low - 0xDC00
				} else if r >= 0xDC00 && r <= 0xDFFF {
					l.scannerError("invalid Unicode surrogate pair", start, l.offset)
					return "", false
				}
				if r <= 0 || r > utf8.MaxRune {
					l.scannerError("invalid Unicode escape value", start, l.offset)
					return "", false
				}
				b.WriteRune(r)
				continue
			}
			l.offset++
			if c >= '0' && c <= '7' {
				value := int(c - '0')
				for n := 1; n < 3 && l.offset < len(l.input) && l.input[l.offset] >= '0' && l.input[l.offset] <= '7'; n++ {
					value = value*8 + int(l.input[l.offset]-'0')
					l.offset++
				}
				b.WriteByte(byte(value))
				continue
			}
			if c == 'x' && l.offset < len(l.input) && hexDigit(l.input[l.offset]) >= 0 {
				value := hexDigit(l.input[l.offset])
				l.offset++
				if l.offset < len(l.input) && hexDigit(l.input[l.offset]) >= 0 {
					value = value*16 + hexDigit(l.input[l.offset])
					l.offset++
				}
				b.WriteByte(byte(value))
				continue
			}
			switch c {
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'v':
				c = '\v'
			}
			b.WriteByte(c)
		}
		if !closed {
			l.scannerError("unterminated quoted string", first, len(l.input))
			return "", false
		}
		saved := l.offset
		if !l.skip(false) || l.offset == len(l.input) || l.input[l.offset] != '\'' {
			l.offset = saved
			break
		}
	}
	s := b.String()
	if escape {
		for i := 0; i < len(s); {
			r, size := utf8.DecodeRuneInString(s[i:])
			if s[i] == 0 || r == utf8.RuneError && size == 1 {
				n := 1
				switch c := s[i]; {
				case c >= 0xC0 && c <= 0xDF:
					n = 2
				case c >= 0xE0 && c <= 0xEF:
					n = 3
				case c >= 0xF0 && c <= 0xF7:
					n = 4
				}
				if n > len(s)-i {
					n = len(s) - i
				}
				const digits = "0123456789abcdef"
				var message strings.Builder
				message.WriteString(`invalid byte sequence for encoding "UTF8":`)
				for j := range n {
					c := s[i+j]
					message.WriteString(" 0x")
					message.WriteByte(digits[c>>4])
					message.WriteByte(digits[c&15])
				}
				l.failDiagnostic("22021", message.String(), "", -1)
				return "", false
			}
			i += size
		}
	}
	return s, true
}

// Bit literals retain their input marker and raw body; unlike ordinary strings,
// neither doubled quotes nor backslashes have an escape interpretation.
func (l *lexer) bitLiteral(first int, prefix byte) (string, bool) {
	var b strings.Builder
	b.WriteByte(prefix)
	for {
		l.offset++ // Opening quote (the prefix has already been consumed).
		start := l.offset
		for l.offset < len(l.input) && l.input[l.offset] != '\'' {
			if l.input[l.offset] == 0 {
				l.failAt("42601", "NUL in quoted input", first)
				return "", false
			}
			l.offset++
		}
		if l.offset == len(l.input) {
			message := "unterminated bit string literal"
			if prefix == 'x' {
				message = "unterminated hexadecimal string literal"
			}
			l.scannerError(message, first, len(l.input))
			return "", false
		}
		b.WriteString(l.input[start:l.offset])
		l.offset++
		saved := l.offset
		if !l.skip(false) || l.offset == len(l.input) || l.input[l.offset] != '\'' {
			l.offset = saved
			break
		}
	}
	return b.String(), true
}

func (l *lexer) parameter(s string) int { n, _ := strconv.ParseInt(s, 10, 32); return int(n) }
func (l *lexer) scan(v *sqlSymType) int {
	l.rawUnicode = false
	if l.options.Context != nil {
		if err := l.options.Context.Err(); err != nil {
			l.err = err
			return 0
		}
	}
	if l.err != nil {
		return 0
	}
	l.skip(true)
	if l.err != nil {
		return 0
	}
	start := l.offset
	l.positionCharacters += utf8.RuneCountInString(l.input[l.positionOffset:start])
	l.positionOffset = start
	l.current = lexeme{pos: Position{Offset: start, Character: l.positionCharacters + 1}}
	if start == len(l.input) {
		return 0
	}
	c := l.input[start]
	token := 0
	switch {
	case (c == 'b' || c == 'B' || c == 'x' || c == 'X') && start+1 < len(l.input) && l.input[start+1] == '\'':
		prefix := c | 32
		l.offset++
		s, ok := l.bitLiteral(start, prefix)
		if !ok {
			return 0
		}
		l.current.text = s
		token = BITSTRING
	case (c == 'u' || c == 'U') && start+2 < len(l.input) && l.input[start+1] == '&' && (l.input[start+2] == '\'' || l.input[start+2] == '"'):
		if l.input[start+2] == '\'' && l.backslashEscapes {
			l.failAt("0A000", "unsafe use of string constant with Unicode escapes", start)
			l.err.(*sqlstate.Error).Detail = `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`
			return 0
		}
		l.rawUnicode = true
		l.offset += 2
		var s string
		var ok bool
		if l.input[l.offset] == '\'' {
			s, ok = l.stringLiteral(start, false)
			token = STRING
		} else {
			s, ok = l.quotedIdentifier(start)
			token = IDENT
			if ok && s == "" {
				l.scannerError("zero-length delimited identifier", start, l.offset)
				return 0
			}
		}
		if !ok {
			return 0
		}
		l.current.text = s
	case c == '"':
		s, ok := l.quotedIdentifier(start)
		if !ok {
			return 0
		}
		if s == "" {
			l.fail("42601", fmt.Sprintf("zero-length delimited identifier at or near \"%s\"", l.input[start:l.offset]))
			return 0
		}
		l.current.text = l.identifierName(s)
		token = IDENT
		if s == "current_setting" || s == "set_config" {
			token = SETTING_FUNC
		} else if s == "nextval" || s == "currval" || s == "lastval" || s == "setval" {
			token = SEQUENCE_FUNC
		}
	case c == '\'' || (c == 'e' || c == 'E') && start+1 < len(l.input) && l.input[start+1] == '\'':
		escape := c != '\''
		if escape {
			l.offset++
		}
		s, ok := l.stringLiteral(start, escape || l.backslashEscapes)
		if !ok {
			return 0
		}
		l.current.text = s
		token = STRING
	case c == '$':
		l.offset++
		end := l.offset
		if end < len(l.input) && initial(l.input[end]) {
			end++
			for end < len(l.input) && (initial(l.input[end]) || digit(l.input[end])) {
				end++
			}
		}
		if end < len(l.input) && l.input[end] == '$' {
			delimiter := l.input[start : end+1]
			bodyStart := end + 1
			close := strings.Index(l.input[bodyStart:], delimiter)
			if close < 0 {
				l.scannerError("unterminated dollar-quoted string", start, len(l.input))
				return 0
			}
			l.current.text = l.input[bodyStart : bodyStart+close]
			l.offset = bodyStart + close + len(delimiter)
			token = STRING
			break
		}
		if l.offset == len(l.input) || !digit(l.input[l.offset]) {
			l.current.text = "$"
			token = int('$')
			break
		}
		begin := l.offset
		for l.offset < len(l.input) && digit(l.input[l.offset]) {
			l.offset++
		}
		s := l.input[begin:l.offset]
		if s == "" || l.offset < len(l.input) && continuation(l.input[l.offset]) {
			l.fail("42601", "invalid parameter")
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			l.fail("42601", "parameter number too large")
			return 0
		}
		if n == 0 {
			l.fail("42P02", "there is no parameter $0")
			return 0
		}
		l.current.text = s
		token = PARAM
	case c == '.' && start+1 < len(l.input) && digit(l.input[start+1]):
		if !l.floatNumericSyntax(start) {
			return 0
		}
		l.current.text = l.input[start:l.offset]
		token = NUMBER
	case digit(c):
		if !l.integerToken(start) {
			return 0
		}
		l.current.text = l.input[start:l.offset]
		token = NUMBER
		l.current.generic = l.genericModifier
	case initial(c):
		for l.offset < len(l.input) && continuation(l.input[l.offset]) {
			l.offset++
		}
		raw := fold(l.input[start:l.offset])
		if l.offset < len(l.input) && (raw == "n" && l.input[l.offset] == '\'' || raw == "u" && strings.HasPrefix(l.input[l.offset:], "&")) {
			l.fail("0A000", "special quoted syntax is unsupported")
			return 0
		}
		l.current.text = l.identifierName(raw)
		l.current.keyword = lookupKeyword(raw)
		if k := l.current.keyword; k != nil {
			token = k.token
			if token == 0 {
				switch k.category {
				case Unreserved:
					token = KW_U
				case ColumnName:
					token = KW_C
				case TypeFunctionName:
					token = KW_T
				case Reserved:
					token = KW_R
				}
			}
		} else {
			token = IDENT
		}
	default:
		l.offset++
		l.current.text = string(c)
		token = int(c)
		if c == 0 {
			l.fail("42601", "NUL in SQL")
			return 0
		}
		if c == '.' && l.offset < len(l.input) && l.input[l.offset] == '.' {
			// Preserve the release DOT_DOT diagnostic spelling.
			l.current.text = ".."
			l.offset++
			token = DOT_DOT
		}
		if c == ':' && l.offset < len(l.input) && l.input[l.offset] == ':' {
			l.current.text = "::"
			l.offset++
		} else if c == ':' && l.offset < len(l.input) && l.input[l.offset] == '=' {
			l.current.text = ":="
			l.offset++
			token = EQUALS_GREATER
		}
		// PostgreSQL maximal-munch operators stop before comment openers.
		// Trailing signs survive only with a nonstandard operator character.
		if strings.ContainsRune("+-*/%<>=~!@#^&|`?", rune(c)) {
			end := l.offset
			for end < len(l.input) && strings.ContainsRune("+-*/%<>=~!@#^&|`?", rune(l.input[end])) {
				if end+1 < len(l.input) && (l.input[end:end+2] == "/*" || l.input[end:end+2] == "--") {
					break
				}
				end++
			}
			if !strings.ContainsAny(l.input[start:end], "~!@#^&|`?%") {
				for end > start+1 && (l.input[end-1] == '+' || l.input[end-1] == '-') {
					end--
				}
			}
			l.offset = end
			l.current.text = l.input[start:end]
			switch l.current.text {
			case "<=":
				token = LESS_EQUAL
			case ">=":
				token = GREATER_EQUAL
			case "<>", "!=":
				token = NOT_EQUAL
				l.current.text = "<>"
			case "=>":
				// Named arguments are not generic binary operators.
				token = EQUALS_GREATER
			case "+", "-", "*", "/", "%", "^", "<", ">", "=":
				token = int(c)
			default:
				token = SYMBOLIC_OPERATOR
			}
		}
	}
	if l.current.raw == "" {
		l.current.raw = l.input[start:l.offset]
	}
	v.tok = l.current
	return token
}

func (l *lexer) nextRaw() rawToken {
	if l.hasLookahead {
		l.hasLookahead = false
		return l.lookahead
	}
	var v sqlSymType
	code := l.scan(&v)
	return rawToken{code: code, lexeme: l.current, unicode: l.rawUnicode}
}

func (l *lexer) Lex(v *sqlSymType) int {
	if l.err != nil {
		return 0
	}
	if l.typeInputEntry {
		l.typeInputEntry = false
		return TYPE_INPUT
	}
	if l.insertScalarEntry {
		l.insertScalarEntry = false
		return INSERT_SCALAR_VALUES
	}
	if l.insertScalarEnd != 0 && !l.hasLookahead && l.offset >= l.insertScalarEnd {
		return 0
	}
	token := l.nextRaw()
	if l.typeInputMode && token.lexeme.keyword != nil && token.lexeme.text == "setof" {
		token.code = SETOF_TYPE
	}
	if l.options.Context != nil {
		if err := l.options.Context.Err(); err != nil {
			l.err = err
			return 0
		}
	}
	if l.err == nil && token.code == AT {
		next := l.nextRaw()
		if l.err != nil {
			return 0
		}
		l.lookahead, l.hasLookahead = next, true
		if next.code != TIME && next.code != LOCAL {
			// AT remains an unreserved label unless it starts zone conversion.
			token.code = KW_U
		}
	}
	if l.err == nil && token.code == NOT {
		next := l.nextRaw()
		if l.err != nil {
			return 0
		}
		l.lookahead, l.hasLookahead = next, true
		if next.code == DEFERRABLE && next.lexeme.keyword != nil {
			token.code = NOT_DEFERRABLE_LA
		} else if next.code == IN {
			// Distinguish the comparison continuation from NOT LIKE/BETWEEN
			// before the grammar reduces the shared comparison operand.
			token.code = NOT_IN_LA
		}
	}
	if l.err == nil && token.code == KW_U && token.lexeme.keyword != nil && token.lexeme.text == "nulls" {
		// NULLS and Unicode filtering share one raw-token cache. Neither
		// lookahead decodes Unicode bodies or changes keyword identity.
		next := l.nextRaw()
		if l.err != nil {
			return 0
		}
		l.lookahead, l.hasLookahead = next, true
		if next.lexeme.keyword != nil && (next.lexeme.text == "first" || next.lexeme.text == "last" || next.lexeme.text == "distinct" || next.lexeme.text == "not") {
			token.code = NULLS_LA
		}
	} else if token.unicode && l.err == nil {
		next := l.nextRaw()
		if l.err != nil {
			return 0
		}
		escape := byte('\\')
		if next.lexeme.keyword != nil && next.lexeme.text == "uescape" {
			arg := l.nextRaw()
			if l.err != nil {
				return 0
			}
			if arg.code != STRING || arg.unicode {
				l.scannerError("UESCAPE must be followed by a simple string literal", arg.lexeme.pos.Offset, arg.lexeme.pos.Offset+len(arg.lexeme.raw))
				return 0
			}
			s := arg.lexeme.text
			if len(s) != 1 || hexDigit(s[0]) >= 0 || s[0] == '+' || s[0] == '\'' || s[0] == '"' || space(s[0]) {
				l.scannerError("invalid Unicode escape character", arg.lexeme.pos.Offset, arg.lexeme.pos.Offset+len(arg.lexeme.raw))
				return 0
			}
			escape = s[0]
		} else {
			l.lookahead, l.hasLookahead = next, true
		}
		text, ok := l.decodeUnicode(token.lexeme.text, escape, token.lexeme.pos.Offset)
		if !ok {
			return 0
		}
		if token.code == IDENT {
			text = l.identifierName(text)
		}
		token.lexeme.text = text
	}
	extractFieldIdentifier := l.extractField && token.code == IDENT
	if IdentityFunctionName(token.lexeme.text) && (token.code == IDENT || l.previousToken == int('.') || token.code == CURRENT_SCHEMA) {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == '(' {
			token.code = IDENTITY_FUNC
			if token.lexeme.keyword != nil {
				token.code = IDENTITY_KEYWORD_FUNC
			}
		}
	}
	if (token.code == IDENT || l.previousToken == int('.')) && currentDateTimeOrdinaryName(token.lexeme.text) {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == int('(') {
			token.code = CURRENT_DATETIME_FUNC
			if token.lexeme.keyword != nil {
				token.code = CURRENT_DATETIME_KEYWORD_FUNC
			}
		}
	}
	// Keep qualified keyword identifiers distinct from actual call syntax.
	// The grammar admits qualified OVERLAPS calls without erasing its category.
	if token.code == OVERLAPS && l.previousToken != int(')') && l.previousToken != int('.') {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == int('(') {
			token.code = SELECTED_FUNC
		}
	}
	if token.code == IDENT && (sizeFunction(token.lexeme.text) || binaryConversionFunction(token.lexeme.text) || binaryHashFunction(token.lexeme.text) || stringNumericBaseFunction(token.lexeme.text) || stringReplacementFunction(token.lexeme.text) || stringPaddingFunction(token.lexeme.text) || stringEncodingFunction(token.lexeme.text) || indexPropertyCallName(token.lexeme.text) || datetimeFunctionName(token.lexeme.text) || strings.EqualFold(token.lexeme.text, "format") || strings.HasPrefix(strings.ToLower(token.lexeme.text), "pg_get_")) {
		token.code = SELECTED_FUNC
	}
	// REPLACE and FORMAT are unreserved keywords outside call syntax.
	// Preserve bare identifiers and output labels.
	if token.lexeme.keyword != nil && (token.lexeme.text == "replace" || token.lexeme.text == "format") {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == int('(') {
			token.code = SELECTED_FUNC
		}
	}
	switch token.lexeme.text {
	case "btrim", "ltrim", "rtrim", "is_normalized", "lower", "upper", "casefold", "initcap":
		if token.code == IDENT {
			token.code = STRING_FUNC
		}
	case "normalize", "trim":
		if token.code == IDENT || token.lexeme.keyword != nil {
			token.code = STRING_FUNC
			if token.lexeme.keyword != nil && l.previousToken != int('.') {
				if token.lexeme.text == "normalize" {
					token.code = NORMALIZE
				} else {
					token.code = TRIM
				}
			}
		}
	case "normalized", "nfc", "nfd", "nfkc", "nfkd", "leading", "trailing", "both":
		if token.lexeme.keyword != nil {
			switch token.lexeme.text {
			case "normalized":
				token.code = NORMALIZED
			case "nfc":
				token.code = NFC
			case "nfd":
				token.code = NFD
			case "nfkc":
				token.code = NFKC
			case "nfkd":
				token.code = NFKD
			case "leading":
				token.code = LEADING
			case "trailing":
				token.code = TRAILING
			case "both":
				token.code = BOTH
			}
		}
	case "operator":
		if token.lexeme.keyword != nil {
			next := l.nextRaw()
			l.lookahead, l.hasLookahead = next, true
			if next.code == '(' {
				token.code = OPERATOR
			}
		}
	case "extract", "overlay", "position", "substring":
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == '(' {
			if token.lexeme.keyword == nil || l.previousToken == int('.') {
				token.code = CHECK_FUNC
			} else {
				switch token.lexeme.text {
				case "extract":
					token.code = EXTRACT
				case "overlay":
					token.code = OVERLAY
				case "position":
					token.code = POSITION
				case "substring":
					token.code = SUBSTRING
				}
			}
		}
	case "placing":
		if token.lexeme.keyword != nil {
			token.code = PLACING
		}
	case "bit_length", "octet_length", "length", "char_length", "character_length", "strpos", "substr", "like_escape":
		if token.code == IDENT {
			token.code = SELECTED_FUNC
		}
	case "gen_random_uuid", "uuidv4", "uuidv7":
		if token.code == IDENT {
			token.code = SELECTED_FUNC
		}
	case "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "col_description", "obj_description", "shobj_description", "num_nulls", "num_nonnulls", "current_schemas", "random", "random_normal", "setseed", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod", "abs", "ceil", "ceiling", "div", "exp", "factorial", "floor":
		if token.code == IDENT {
			token.code = SELECTED_FUNC
		}
	case "variadic":
		if token.lexeme.keyword != nil {
			token.code = VARIADIC
		}
	case "current_setting", "set_config":
		if token.code == IDENT {
			token.code = SETTING_FUNC
		}
	case "nextval", "currval", "lastval", "setval":
		if token.code == IDENT {
			token.code = SEQUENCE_FUNC
		}
	case "reset":
		if token.lexeme.keyword != nil {
			token.code = RESET
		}
	case "all":
		if token.lexeme.keyword != nil {
			token.code = ALL
		}
	case "local":
		if token.lexeme.keyword != nil {
			token.code = LOCAL
		}
	}
	if l.dateLiteralModifier {
		switch token.code {
		case NUMBER:
			token.code = DATE_MODIFIER_NUMBER
		case int('+'), int('-'), int(')'):
		default:
			l.syntax(token.lexeme)
			return 0
		}
	}
	if token.code == SELECTED_FUNC && (l.checkDepth > 0 || l.indexExpression || l.generationDepth > 0 || binaryScalarFunction(token.lexeme.text) || indexPropertyCallName(token.lexeme.text) || datetimeFunctionName(token.lexeme.text) || token.lexeme.text == "current_schemas" || token.lexeme.text == "gen_random_uuid" || token.lexeme.text == "uuidv4" || token.lexeme.text == "uuidv7") {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if l.err != nil {
			return 0
		}
		if next.code == int('(') {
			token.code = CHECK_FUNC
		}
	}
	// Otherwise-unselected calls inside format arguments reach resolution only.
	// Outside that actual call context, existing capability guards remain.
	if l.formatDepth > 0 && token.code == IDENT && token.lexeme.keyword == nil && !ordinaryCastFunction(token.lexeme) {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if next.code == int('(') {
			token.code = CHECK_FUNC
			token.lexeme.resolutionOnly = true
		}
	}
	if extractFieldIdentifier {
		// Preserve the word so a following '(' is diagnosed at that token,
		// rather than treating e.g. lower as a scalar function in field position.
		token.code = IDENT
	}
	// VALUES remains a forbidden stored-expression wrapper. SELECT subqueries
	// use the shared query AST and consumer binder diagnostics.
	if l.indexExpression && token.code == int('(') {
		next := l.nextRaw()
		l.lookahead, l.hasLookahead = next, true
		if l.err != nil {
			return 0
		}
		if next.code == VALUES {
			token.code = INDEX_SUBQUERY_OPEN
		}
	}
	if l.generationDepth > 0 && !extractFieldIdentifier {
		forbiddenCall := false
		switch token.lexeme.text {
		case "sum", "avg", "count", "min", "max", "array_agg", "string_agg", "grouping", "row_number", "rank", "dense_rank", "generate_series", "unnest":
			next := l.nextRaw()
			l.lookahead, l.hasLookahead = next, true
			forbiddenCall = next.code == int('(')
		}
		if forbiddenCall {
			token.code = CHECK_FUNC
			code, message := "", ""
			switch token.lexeme.text {
			case "sum", "avg", "count", "min", "max", "array_agg", "string_agg":
				code, message = "42803", "aggregate functions are not allowed in column generation expressions"
			case "grouping":
				code, message = "42803", "grouping operations are not allowed in column generation expressions"
			case "row_number", "rank", "dense_rank":
				code, message = "42809", "window function "+token.lexeme.text+" requires an OVER clause"
			case "generate_series", "unnest":
				code, message = "0A000", "set-returning functions are not allowed in column generation expressions"
			}
			if l.generationRestriction == nil {
				l.generationRestriction = &GenerationRestriction{code, message, token.lexeme.pos}
			}
		}
		if token.lexeme.text == "over" && token.lexeme.keyword != nil && l.generationPrevious.raw == ")" {
			token.code = GENERATION_OVER
			l.generationWindowPending = true
		}
		if token.lexeme.text == "partition" && token.lexeme.keyword != nil && l.generationWindowDepth == l.generationDepth && l.generationPrevious.raw == "(" {
			token.code = GENERATION_PARTITION
		}
		if token.code == int('(') {
			l.generationDepth++
			if l.generationWindowPending {
				l.generationWindowDepth = l.generationDepth
				l.generationWindowPending = false
			}
		}
		if token.code == int(')') {
			l.generationDepth--
			if l.generationDepth < l.generationWindowDepth {
				l.generationWindowDepth = 0
			}
			if l.generationDepth == 0 {
				l.generationCompletedRestriction = l.generationRestriction
			}
		}
	} else if l.generationPending {
		if token.code == ALWAYS || token.code == BY {
			l.generationModePos = token.lexeme.pos
		}
		if token.code == int('(') && l.generationPrevious.text == "as" {
			l.generationDepth = 1
			l.generationPending = false
			l.generationRestriction = nil
		}
		if token.code == IDENTITY {
			l.generationPending = false
		}
	}
	if token.code == GENERATED {
		l.generationPending = true
	}
	l.generationPrevious = token.lexeme
	if l.checkDepth > 0 {
		switch token.code {
		case int('('):
			l.checkDepth++
		case int(')'):
			l.checkDepth--
		}
	} else {
		if l.checkPending && token.code == int('(') {
			l.checkDepth = 1
		}
		l.checkPending = token.code == CHECK
	}
	if l.formatDepth > 0 {
		switch token.code {
		case int('('):
			l.formatDepth++
		case int(')'):
			l.formatDepth--
		}
	}
	l.extractField = token.code == int('(') && l.previousToken == EXTRACT
	l.current = token.lexeme
	v.tok = token.lexeme
	if token.code == int('(') && l.temporalLead {
		l.temporalModifier = true
	}
	if token.code == int(')') || token.code == int(',') || token.code == int(';') {
		l.temporalModifier = false
		if token.code != int(',') {
			l.genericModifier = false
			l.dateLiteralModifier = false
			l.multipleModifiers = false
		}
	}
	l.temporalLead = token.code == TIMESTAMP || token.code == TIME || token.code == IDENT && columnType(token.lexeme).Quoted && (token.lexeme.text == "timestamp" || token.lexeme.text == "time")
	l.previousToken = token.code
	return token.code
}

// PostgreSQL positions Unicode-filter errors in the dequoted, concatenated
// body, not with a raw-source escape map. Keep that intentional cursor drift.
func (l *lexer) decodeUnicode(body string, escape byte, opener int) (string, bool) {
	first := strings.IndexByte(body, escape)
	if first < 0 {
		return body, true
	}
	var b strings.Builder
	b.Grow(len(body))
	b.WriteString(body[:first])
	var high rune
	fail := func(message, hint string, i int) (string, bool) {
		l.failDiagnostic("42601", message, hint, opener+3+i)
		return "", false
	}
	for i := first; i < len(body); {
		start := i
		if body[i] != escape {
			if high != 0 {
				return fail("invalid Unicode surrogate pair", "", i)
			}
			b.WriteByte(body[i])
			i++
			continue
		}
		i++
		if i < len(body) && body[i] == escape {
			if high != 0 {
				return fail("invalid Unicode surrogate pair", "", start)
			}
			b.WriteByte(escape)
			i++
			continue
		}
		width := 4
		if i < len(body) && body[i] == '+' {
			width = 6
			i++
		}
		var value rune
		for range width {
			if i == len(body) || hexDigit(body[i]) < 0 {
				return fail("invalid Unicode escape", `Unicode escapes must be \XXXX or \+XXXXXX.`, start)
			}
			value = value*16 + rune(hexDigit(body[i]))
			i++
		}
		if value == 0 || value > utf8.MaxRune {
			return fail("invalid Unicode escape value", "", start)
		}
		if high != 0 {
			if value < 0xDC00 || value > 0xDFFF {
				return fail("invalid Unicode surrogate pair", "", start)
			}
			b.WriteRune(0x10000 + (high-0xD800)*0x400 + value - 0xDC00)
			high = 0
		} else if value >= 0xD800 && value <= 0xDBFF {
			high = value
		} else if value >= 0xDC00 && value <= 0xDFFF {
			return fail("invalid Unicode surrogate pair", "", start)
		} else {
			b.WriteRune(value)
		}
	}
	if high != 0 {
		return fail("invalid Unicode surrogate pair", "", len(body))
	}
	return b.String(), true
}

func numericDigit(c byte) uint64 {
	switch {
	case c >= '0' && c <= '9':
		return uint64(c - '0')
	case c >= 'a' && c <= 'f':
		return uint64(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return uint64(c - 'A' + 10)
	default:
		return 16
	}
}

// digitEnd recognizes one optional separator before each digit. The caller
// supplies the mandatory first decimal digit or the radix prefix.
func digitEnd(input string, offset int, base uint64) int {
	for offset < len(input) {
		next := offset
		if input[next] == '_' {
			next++
		}
		if next == len(input) || numericDigit(input[next]) >= base {
			break
		}
		offset = next + 1
	}
	return offset
}

func (l *lexer) integerToken(start int) bool {
	decimalEnd := digitEnd(l.input, start+1, 10)
	end := decimalEnd
	reason := ""
	if l.input[start] == '0' && start+1 < len(l.input) {
		var base uint64
		var name string
		switch l.input[start+1] {
		case 'b', 'B':
			base, name = 2, "binary"
		case 'o', 'O':
			base, name = 8, "octal"
		case 'x', 'X':
			base, name = 16, "hexadecimal"
		}
		if base != 0 {
			end = digitEnd(l.input, start+2, base)
			if end == start+2 {
				if end < len(l.input) && l.input[end] == '_' {
					end++
				}
				reason = "invalid " + name + " integer"
			}
		}
	}
	// PostgreSQL integer_junk is a decimal integer followed by an identifier,
	// even when the spelling also starts a radix integer. Longest match wins;
	// valid radix and missing-digit rules win ties.
	junkEnd := decimalEnd
	if junkEnd < len(l.input) && initial(l.input[junkEnd]) {
		for junkEnd < len(l.input) && continuation(l.input[junkEnd]) {
			junkEnd++
		}
		if junkEnd > end {
			end, reason = junkEnd, "trailing junk after numeric literal"
		}
	}
	// Hexadecimal e is a digit; only decimal tokens enter real syntax.
	if end == decimalEnd || reason != "" && decimalEnd < len(l.input) && (l.input[decimalEnd] == 'e' || l.input[decimalEnd] == 'E') {
		if decimalEnd < len(l.input) && l.input[decimalEnd] == '.' {
			if decimalEnd+1 < len(l.input) && l.input[decimalEnd+1] == '.' {
				// Release numericfail throws back both dots and returns Iconst.
				l.offset = decimalEnd
				return true
			}
			return l.floatNumericSyntax(start)
		}
		if decimalEnd < len(l.input) && (l.input[decimalEnd] == 'e' || l.input[decimalEnd] == 'E') {
			exponent := decimalEnd + 1
			if exponent < len(l.input) && (l.input[exponent] == '+' || l.input[exponent] == '-') {
				exponent++
			}
			if exponent < len(l.input) && digit(l.input[exponent]) {
				exponentEnd := digitEnd(l.input, exponent+1, 10)
				realJunkEnd := exponentEnd
				if realJunkEnd < len(l.input) && initial(l.input[realJunkEnd]) {
					for realJunkEnd < len(l.input) && continuation(l.input[realJunkEnd]) {
						realJunkEnd++
					}
				}
				// A complete valid exponent beats equal-length integer junk,
				// but not a longer identifier/separator suffix. Signed
				// exponents also need the real_junk match beyond the sign.
				if realJunkEnd == exponentEnd && exponentEnd >= end {
					if l.genericModifier {
						l.offset = exponentEnd
						l.current.numeric = true
						return true
					}
					if l.floatPrecision || l.temporalModifier {
						l.failAt("42601", "syntax error at or near \""+l.input[start:exponentEnd]+"\"", start)
						return false
					}
					l.offset = exponentEnd
					l.current.numeric = true
					return true
				}
				if realJunkEnd > end {
					end, reason = realJunkEnd, "trailing junk after numeric literal"
				}
			}
			if exponent > end {
				end, reason = exponent, "trailing junk after numeric literal"
			}
		}
	}
	l.offset = end
	if reason != "" {
		l.failAt("42601", reason+" at or near \""+l.input[start:end]+"\"", start)
		return false
	}
	return true
}

// Preserve release longest-match numeric diagnostics. FLOAT and keyword
// timestamp modifiers reject real tokens; generic modifiers and scalars retain
// their spelling for type-owned input.
func (l *lexer) floatNumericSyntax(start int) bool {
	end := start
	if digit(l.input[end]) {
		end = digitEnd(l.input, end+1, 10)
	}
	if end < len(l.input) && l.input[end] == '.' {
		end++
		if end < len(l.input) && digit(l.input[end]) {
			end = digitEnd(l.input, end+1, 10)
		}
	}
	numericEnd := end
	junk := false
	// numeric_junk can outmatch a real token, or tie a complete exponent
	// (where the earlier real rule wins). Preserve the scanner's longest match.
	if end < len(l.input) && initial(l.input[end]) {
		junk = true
		for end < len(l.input) && continuation(l.input[end]) {
			end++
		}
	}
	if numericEnd < len(l.input) && (l.input[numericEnd] == 'e' || l.input[numericEnd] == 'E') {
		exponent := numericEnd + 1
		signed := exponent < len(l.input) && (l.input[exponent] == '+' || l.input[exponent] == '-')
		if signed {
			exponent++
		}
		if exponent < len(l.input) && digit(l.input[exponent]) {
			realEnd := digitEnd(l.input, exponent+1, 10)
			realJunk := false
			if realEnd < len(l.input) && initial(l.input[realEnd]) {
				realJunk = true
				for realEnd < len(l.input) && continuation(l.input[realEnd]) {
					realEnd++
				}
			}
			if realEnd >= end {
				end, junk = realEnd, realJunk
			}
		} else if signed && exponent > end {
			// realfail consumes the incomplete exponent including its sign.
			end, junk = exponent, true
		}
	}
	if junk {
		l.failAt("42601", "trailing junk after numeric literal at or near \""+l.input[start:end]+"\"", start)
	} else if l.genericModifier || !l.floatPrecision && !l.temporalModifier {
		l.offset = end
		l.current.numeric = true
		return true
	} else {
		l.failAt("42601", "syntax error at or near \""+l.input[start:end]+"\"", start)
	}
	return false
}

// Selected string/raw-octet calls and LIKE escape normalization use the existing
// scalar-call production; unrelated projection functions retain their boundary.
func binaryScalarFunction(name string) bool {
	switch name {
	case "bit_length", "octet_length", "length", "char_length", "character_length", "strpos", "btrim", "ltrim", "rtrim", "overlay", "position", "substring", "substr", "like_escape", "format":
		return true
	}
	return sizeFunction(name) || binaryConversionFunction(name) || binaryHashFunction(name) || stringNumericBaseFunction(name) || stringReplacementFunction(name) || stringPaddingFunction(name) || stringEncodingFunction(name) || strings.EqualFold(name, "format")
}

// Admission recognizes quoted hash spellings without normalizing the AST.
// Resolution remains exact, so quoted mixed/uppercase names can fail 42883.
func binaryHashFunction(name string) bool {
	switch len(name) {
	case 3:
		return strings.EqualFold(name, "md5")
	case 5:
		return strings.EqualFold(name, "crc32")
	case 6:
		return strings.EqualFold(name, "crc32c") || strings.EqualFold(name, "sha224") ||
			strings.EqualFold(name, "sha256") || strings.EqualFold(name, "sha384") || strings.EqualFold(name, "sha512")
	}
	return false
}

func binaryConversionFunction(name string) bool {
	return strings.EqualFold(name, "convert") || strings.EqualFold(name, "convert_from") ||
		strings.EqualFold(name, "convert_to") || strings.EqualFold(name, "encode") || strings.EqualFold(name, "decode")
}

func stringNumericBaseFunction(name string) bool {
	return strings.EqualFold(name, "to_bin") || strings.EqualFold(name, "to_oct") || strings.EqualFold(name, "to_hex")
}

func sizeFunction(name string) bool {
	return strings.EqualFold(name, "pg_size_bytes") || strings.EqualFold(name, "pg_size_pretty")
}

// Admission preserves quoted case in the AST; routine resolution stays exact.
func stringReplacementFunction(name string) bool {
	return strings.EqualFold(name, "replace") || strings.EqualFold(name, "translate") || strings.EqualFold(name, "starts_with")
}

func stringPaddingFunction(name string) bool {
	return strings.EqualFold(name, "lpad") || strings.EqualFold(name, "rpad") || strings.EqualFold(name, "repeat") || strings.EqualFold(name, "reverse")
}

// Admit selected spellings without changing case-sensitive routine resolution.
func stringEncodingFunction(name string) bool {
	return strings.EqualFold(name, "ascii") || strings.EqualFold(name, "chr") ||
		strings.EqualFold(name, "to_ascii") || strings.EqualFold(name, "unistr") ||
		strings.EqualFold(name, "unicode_assigned")
}

// Preserve quoted case for ordinary signature diagnostics. Prefix admission
// parses unresolved inquiry-family names; it does not grant builtin dispatch.
func indexPropertyCallName(name string) bool {
	return strings.EqualFold(name, "pg_indexam_has_property") ||
		len(name) >= len("pg_index_") && strings.EqualFold(name[:len("pg_index_")], "pg_index_")
}

// Recognize quoted spellings for admission without changing exact resolution.
func datetimeFunctionName(name string) bool {
	return strings.EqualFold(name, "extract") || strings.EqualFold(name, "date_part") ||
		strings.EqualFold(name, "age") || strings.EqualFold(name, "isfinite") || strings.EqualFold(name, "overlaps") ||
		strings.EqualFold(name, "justify_days") || strings.EqualFold(name, "justify_hours") ||
		strings.EqualFold(name, "justify_interval") || strings.EqualFold(name, "to_char") ||
		strings.EqualFold(name, "to_date") || strings.EqualFold(name, "to_timestamp") || strings.EqualFold(name, "to_number") ||
		datetimeMakeFunctionName(name)
}
