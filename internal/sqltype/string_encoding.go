package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

func (p StringPlan) evaluateEncoding(ctx context.Context, texts [4]string, values [4]Value) (Value, error) {
	switch p.name {
	case "ascii":
		if texts[0] == "" {
			return Integer(p.result.Type, 0)
		}
		r, _ := utf8.DecodeRuneInString(texts[0])
		return Integer(p.result.Type, int64(r))
	case "chr":
		n, err := values[0].Integer()
		if err != nil {
			return Value{}, err
		}
		if n < 0 {
			return Value{}, failure("22023", "character number must be positive")
		}
		if n == 0 {
			return Value{}, failure("54000", "null character not permitted")
		}
		if n > utf8.MaxRune {
			return Value{}, failure("54000", fmt.Sprintf("requested character too large for encoding: %d", n))
		}
		if n >= 0xD800 && n <= 0xDFFF {
			return Value{}, failure("54000", fmt.Sprintf("requested character not valid for encoding: %d", n))
		}
		return String(p.result.Type, string(rune(n))), nil
	case "unicode_assigned":
		yes, err := unicode16.Assigned(ctx, texts[0])
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result.Type, yes), nil
	case "unistr":
		text, err := stringUnistr(ctx, texts[0])
		if err != nil {
			return Value{}, err
		}
		return String(p.result.Type, text), nil
	case "to_ascii":
		encoding := int64(6) // The existing database encoding is fixed UTF8.
		if p.count == 2 {
			if p.formal[1].Type.ID() == Int4 {
				var err error
				encoding, err = values[1].Integer()
				if err != nil {
					return Value{}, err
				}
				if encoding < 0 || encoding >= int64(len(stringEncodingNames)) {
					return Value{}, failure("42704", fmt.Sprintf("%d is not a valid encoding code", encoding))
				}
			} else {
				encoding = int64(stringEncodingID(texts[1]))
				if encoding < 0 {
					return Value{}, failure("42704", texts[1]+" is not a valid encoding name")
				}
			}
		}
		text, err := stringToASCII(ctx, texts[0], int(encoding))
		if err != nil {
			return Value{}, err
		}
		return String(p.result.Type, text), nil
	default:
		return Value{}, fmt.Errorf("invalid encoding operation")
	}
}

// Translation constants from PostgreSQL REL_18_0 src/backend/utils/adt/ascii.c
// (Copyright 1999-2025 PostgreSQL Global Development Group). These map BYTES,
// not Unicode scalars, and intentionally preserve the release's unusual UTF8
// source behavior when a legacy encoding is explicitly requested.
const asciiLatin1 = "  cL Y  \"Ca  -R     'u .,      ?AAAAAAACEEEEIIII NOOOOOxOUUUUYTBaaaaaaaceeeeiiii nooooo/ouuuuyty"
const asciiLatin2 = " A L LS \"SSTZ-ZZ a,l'ls ,sstz\"zzRAAAALCCCEEEEIIDDNNOOOOxRUUUUYTBraaaalccceeeeiiddnnoooo/ruuuuyt."
const asciiLatin9 = "  cL YS sCa  -R     Zu .z   EeY?AAAAAAACEEEEIIII NOOOOOxOUUUUYTBaaaaaaaceeeeiiii nooooo/ouuuuyty"
const asciiWin1250 = "  ' \"    %S<STZZ `'\"\".--  s>stzz   L A  \"CS  -RZ  ,l'u .,as L\"lzRAAAALCCCEEEEIIDDNNOOOOxRUUUUYTBraaaalccceeeeiiddnnoooo/ruuuuyt "

func stringToASCII(ctx context.Context, source string, encoding int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var table string
	start := byte(160)
	switch encoding {
	case 8:
		table = asciiLatin1
	case 9:
		table = asciiLatin2
	case 16:
		table = asciiLatin9
	case 29:
		table, start = asciiWin1250, 128
	default:
		return "", failure("0A000", "encoding conversion from "+stringEncodingNames[encoding]+" to ASCII not supported")
	}
	var out strings.Builder
	changed := false
	for i := range len(source) {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		ch := source[i]
		mapped := ch
		if ch >= 128 {
			mapped = ' '
			if ch >= start {
				mapped = table[int(ch-start)]
			}
		}
		if !changed && mapped != ch {
			out.Grow(len(source))
			out.WriteString(source[:i])
			changed = true
		}
		if changed {
			out.WriteByte(mapped)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !changed {
		return source, nil
	}
	return out.String(), nil
}

func unicodeEscapeFailure() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid Unicode escape", Hint: `Unicode escapes must be \XXXX, \+XXXXXX, \uXXXX, or \UXXXXXXXX.`}
}

func stringUnistr(ctx context.Context, source string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	changed, copied := false, 0
	var high uint32
	var work uint32
	for i := 0; i < len(source); {
		work++
		if work&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if source[i] != '\\' {
			if high != 0 {
				return "", failure("42601", "invalid Unicode surrogate pair")
			}
			i++
			continue
		}
		if i+1 == len(source) {
			return "", unicodeEscapeFailure()
		}
		end := i + 2
		var code uint32
		slash := source[i+1] == '\\'
		if !slash {
			digits, first := 4, i+1
			switch source[i+1] {
			case '+':
				digits, first = 6, i+2
			case 'u':
				first = i + 2
			case 'U':
				digits, first = 8, i+2
			}
			end = first + digits
			if end > len(source) {
				return "", unicodeEscapeFailure()
			}
			for j := first; j < end; j++ {
				ch := source[j]
				var nibble byte
				switch {
				case ch >= '0' && ch <= '9':
					nibble = ch - '0'
				case ch >= 'a' && ch <= 'f':
					nibble = ch - 'a' + 10
				case ch >= 'A' && ch <= 'F':
					nibble = ch - 'A' + 10
				default:
					return "", unicodeEscapeFailure()
				}
				code = code<<4 | uint32(nibble)
			}
			if code == 0 || code > utf8.MaxRune {
				return "", failure("22023", fmt.Sprintf("invalid Unicode code point: %04X", code))
			}
		}
		// Syntax and scalar-range diagnostics precede pending-pair diagnostics.
		if high != 0 {
			if slash || code < 0xDC00 || code > 0xDFFF {
				return "", failure("42601", "invalid Unicode surrogate pair")
			}
			code = 0x10000 + (high-0xD800)*0x400 + (code - 0xDC00)
			high = 0
		} else if !slash && code >= 0xDC00 && code <= 0xDFFF {
			return "", failure("42601", "invalid Unicode surrogate pair")
		}
		if !changed {
			out.Grow(len(source))
			changed = true
		}
		out.WriteString(source[copied:i])
		if slash {
			out.WriteByte('\\')
		} else if code >= 0xD800 && code <= 0xDBFF {
			high = code
		} else {
			out.WriteRune(rune(code))
		}
		i, copied = end, end
	}
	if high != 0 {
		return "", failure("42601", "invalid Unicode surrogate pair")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !changed {
		return source, nil
	}
	out.WriteString(source[copied:])
	return out.String(), nil
}
