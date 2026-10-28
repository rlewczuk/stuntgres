package copyfmt

import (
	"context"
	"unsafe"

	"stuntgres/internal/sqltype/pgencoding18"
)

// encodedField borrows reusable statement-local scratch only until its caller
// appends this field into the complete row. No view is retained by rows or I/O.
func (o Options) encodedField(ctx context.Context, text string, scratch []byte) (string, []byte, error) {
	if !o.Transport {
		return text, scratch, nil
	}
	scratch, err := o.Conversion.Append(ctx, scratch[:0], text)
	if err != nil {
		return "", scratch, err
	}
	return unsafe.String(unsafe.SliceData(scratch), len(scratch)), scratch, nil
}
func (o Options) characterWidth(text string) int {
	id := 6
	if o.Transport {
		id = o.Encoding
	}
	return min(pgencoding18.Length(id, text), len(text))
}

func appendTextEncoding(dst []byte, text string, o Options) []byte {
	for i := 0; i < len(text); {
		n := o.characterWidth(text[i:])
		if n > 1 {
			dst = append(dst, text[i:i+n]...)
			i += n
			continue
		}
		b := text[i]
		i++
		switch b {
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\v':
			dst = append(dst, '\\', 'v')
		default:
			if b == o.Delimiter {
				dst = append(dst, '\\')
			}
			dst = append(dst, b)
		}
	}
	return dst
}

func appendCSVEncoding(dst []byte, text string, o Options, force, single, quoteNull bool) []byte {
	quote := force || quoteNull || single && text == `\.`
	for i := 0; !quote && i < len(text); {
		n := o.characterWidth(text[i:])
		b := text[i]
		if n == 1 && (b == o.Delimiter || b == o.Quote || b == '\r' || b == '\n') {
			quote = true
		}
		i += n
	}
	if !quote {
		return append(dst, text...)
	}
	dst = append(dst, o.Quote)
	for i := 0; i < len(text); {
		n := o.characterWidth(text[i:])
		b := text[i]
		if n == 1 && (b == o.Quote || b == o.Escape) {
			dst = append(dst, o.Escape)
		}
		dst = append(dst, text[i:i+n]...)
		i += n
	}
	return append(dst, o.Quote)
}
