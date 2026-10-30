package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/pgencoding18"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// ConvertBinaryEncoding is pg_convert's raw immutable-string provider. NAME
// coercion belongs to the caller. This layer never resolves indirect paths.
func ConvertBinaryEncoding(ctx context.Context, raw, sourceName, destinationName string) (string, error) {
	source, destination, unchanged, err := PrepareBinaryEncoding(ctx, raw, sourceName, destinationName)
	if err != nil {
		return "", err
	}
	if unchanged {
		return raw, nil
	}
	routine := pgencoding18.ConversionRoutine(source, destination)
	if routine == "" {
		return "", MissingBinaryConversion(source, destination)
	}
	return emitBinaryEncoding(ctx, raw, source, destination)
}

// PrepareBinaryEncoding validates the entire source before any default lookup.
// Identity/SQL_ASCII/empty cases intentionally need no conversion object.
func PrepareBinaryEncoding(ctx context.Context, raw, sourceName, destinationName string) (source, destination int, unchanged bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	source = pgencoding18.ID(sourceName)
	if source < 0 {
		err = failure("22023", `invalid source encoding name "`+sourceName+`"`)
		return
	}
	destination = pgencoding18.ID(destinationName)
	if destination < 0 {
		err = failure("22023", `invalid destination encoding name "`+destinationName+`"`)
		return
	}
	if err = binaryEncodingValidate(ctx, raw, source); err != nil {
		return
	}
	if raw == "" || source == destination || destination == 0 {
		unchanged = true
		err = ctx.Err()
		return
	}
	if source == 0 {
		err = binaryEncodingValidate(ctx, raw, destination)
		unchanged = err == nil
		return
	}
	return
}

func MissingBinaryConversion(source, destination int) error {
	return failure("42883", fmt.Sprintf("default conversion function for encoding %q to %q does not exist", pgencoding18.Name(source), pgencoding18.Name(destination)))
}

func emitBinaryEncoding(ctx context.Context, raw string, source, destination int) (string, error) {
	if !binaryEncodingInputSize(len(raw)) {
		return "", binaryEncodingSizeError(len(raw))
	}
	size, err := binaryEncodingPass(ctx, raw, source, destination, nil)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	if _, err = binaryEncodingPass(ctx, raw, source, destination, &out); err != nil {
		return "", err
	}
	return out.String(), nil
}
func binaryEncodingInputSize(n int) bool { return n < int(^uint(0)>>1)/4 }
func binaryEncodingSizeError(n int) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "out of memory", Detail: fmt.Sprintf("String of %d bytes is too long for encoding conversion.", n)}
}
func binaryEncodingValidate(ctx context.Context, raw string, id int) error {
	next := 0
	for i := 0; i < len(raw); {
		if i >= next {
			if err := ctx.Err(); err != nil {
				return err
			}
			next = i + 4096
		}
		n := pgencoding18.Verify(id, raw[i:])
		if n < 0 {
			return binaryEncodingInvalid(raw[i:], id)
		}
		i += n
	}
	return ctx.Err()
}
func binaryEncodingBytes(raw string, id int) string {
	n := min(pgencoding18.Length(id, raw), len(raw), 8)
	const hex = "0123456789abcdef"
	var buf [39]byte
	pos := 0
	for i := range n {
		if i != 0 {
			buf[pos] = ' '
			pos++
		}
		buf[pos] = '0'
		buf[pos+1] = 'x'
		buf[pos+2] = hex[raw[i]>>4]
		buf[pos+3] = hex[raw[i]&15]
		pos += 4
	}
	return string(buf[:pos])
}
func binaryEncodingInvalid(raw string, id int) error {
	return failure("22021", fmt.Sprintf("invalid byte sequence for encoding %q: %s", pgencoding18.Name(id), binaryEncodingBytes(raw, id)))
}
func binaryEncodingPass(ctx context.Context, raw string, source, destination int, out *strings.Builder) (int, error) {
	if err := util.ExecutionCheckpoint(ctx, "encoding.convert"); err != nil {
		return 0, err
	}
	size, next := 0, 0
	remaining := 0
	for i := 0; i < len(raw); {
		if i >= next {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			next = i + 4096
		}
		a, b, n, invalid := pgencoding18.Step(source, destination, raw[i:])
		if invalid {
			return 0, binaryEncodingInvalid(raw[i:], source)
		}
		if n == 0 {
			return 0, failure("22P05", fmt.Sprintf("character with byte sequence %s in encoding %q has no equivalent in encoding %q", binaryEncodingBytes(raw[i:], source), pgencoding18.Name(source), pgencoding18.Name(destination)))
		}
		// A conversion step consumes at most eight input bytes. Keep each
		// granted pass quantum within 4096 bytes, including combined maps.
		if n > remaining {
			if err := workadmission.Admit(ctx, workadmission.WorkEncodingConvert); err != nil {
				return 0, err
			}
			remaining = 4096
		}
		remaining -= n
		width := pgencoding18.Width(a) + pgencoding18.Width(b)
		if size >= 0x3fffffff-width {
			return 0, binaryEncodingSizeError(len(raw))
		}
		size += width
		if out != nil {
			binaryEncodingWrite(out, a)
			binaryEncodingWrite(out, b)
		}
		i += n
	}
	return size, ctx.Err()
}
func binaryEncodingWrite(out *strings.Builder, word uint32) {
	for shift := 24; shift >= 0; shift -= 8 {
		b := byte(word >> uint(shift))
		if b != 0 {
			out.WriteByte(b)
		}
	}
}
