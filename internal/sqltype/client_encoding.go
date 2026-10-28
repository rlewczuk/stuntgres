package sqltype

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/sqltype/pgencoding18"
)

// EncodingConversion owns only a copied concrete release capability. Catalog
// resolution belongs to core; absence is retained, never repaired by a kernel
// lookup. COPY may submit its response before a nonempty field needs this pair.
type EncodingConversion struct {
	source, destination int
	handler             EncodingConversionHandler
}

func NewEncodingConversion(source, destination int, handler EncodingConversionHandler) EncodingConversion {
	return EncodingConversion{source: source, destination: destination, handler: handler}
}
func (c EncodingConversion) Source() int      { return c.source }
func (c EncodingConversion) Destination() int { return c.destination }
func (c EncodingConversion) Available() bool {
	return c.source == c.destination || c.source == 0 || c.destination == 0 || c.handler.Supports(c.source, c.destination)
}
func (c EncodingConversion) Convert(ctx context.Context, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	validation := c.source
	if c.source == 0 {
		validation = c.destination
	}
	if err := binaryEncodingValidate(ctx, raw, validation); err != nil {
		return "", err
	}
	if raw == "" || c.source == c.destination || c.source == 0 || c.destination == 0 {
		return raw, nil
	}
	if !c.Available() {
		return "", MissingBinaryConversion(c.source, c.destination)
	}
	output, consumed, err := c.handler.ConvertValidated(ctx, raw, c.source, c.destination)
	if err == nil && consumed != len(raw) {
		return "", failure("XX000", "encoding conversion did not consume complete input")
	}
	return output, err
}

// Append validates/counts before growing dst. An error exposes none of the
// current datum, including cancellation during emission. Existing scratch is
// reused without a temporary converted string or per-character allocation.
func (c EncodingConversion) Append(ctx context.Context, dst []byte, raw string) ([]byte, error) {
	start := len(dst)
	validation := c.source
	if c.source == 0 {
		validation = c.destination
	}
	if err := binaryEncodingValidate(ctx, raw, validation); err != nil {
		return dst, err
	}
	if raw == "" || c.source == c.destination || c.source == 0 || c.destination == 0 {
		return append(dst, raw...), nil
	}
	if !c.Available() {
		return dst, MissingBinaryConversion(c.source, c.destination)
	}
	if !binaryEncodingInputSize(len(raw)) {
		return dst, binaryEncodingSizeError(len(raw))
	}
	size, err := binaryEncodingPass(ctx, raw, c.source, c.destination, nil)
	if err != nil {
		return dst, err
	}
	if size > int(^uint(0)>>1)-start {
		return dst, binaryEncodingSizeError(len(raw))
	}
	dst = slices.Grow(dst, size)
	dst = dst[:start+size]
	pos, next := start, 0
	for i := 0; i < len(raw); {
		if i >= next {
			if err := ctx.Err(); err != nil {
				return dst[:start], err
			}
			next = i + 4096
		}
		a, b, n, _ := pgencoding18.Step(c.source, c.destination, raw[i:])
		for _, word := range [2]uint32{a, b} {
			for shift := 24; shift >= 0; shift -= 8 {
				if v := byte(word >> uint(shift)); v != 0 {
					dst[pos] = v
					pos++
				}
			}
		}
		i += n
	}
	if err := ctx.Err(); err != nil {
		return dst[:start], err
	}
	return dst, nil
}

// ClientEncodingPolicy is an immutable admitted pair; zero is UTF8. It has no
// descriptor, authorization, operation, frame, borrowed buffer or callbacks.
type ClientEncodingPolicy struct {
	id                 int
	admitted           bool
	incoming, outgoing EncodingConversion
	defaultIdentity    [3]uint64 // Incoming/outgoing default OIDs and catalog epoch.
}

func NewClientEncodingPolicy(id int, incoming, outgoing EncodingConversionHandler) (ClientEncodingPolicy, error) {
	p := ClientEncodingPolicy{id: id, admitted: true, incoming: NewEncodingConversion(id, 6, incoming), outgoing: NewEncodingConversion(6, id, outgoing)}
	if !p.incoming.Available() || !p.outgoing.Available() {
		return ClientEncodingPolicy{}, failure("0A000", "conversion between "+EncodingName(id)+" and UTF8 is not supported")
	}
	return p, nil
}
func (p ClientEncodingPolicy) ID() int {
	if !p.admitted {
		return 6
	}
	return p.id
}
func (p ClientEncodingPolicy) Incoming() EncodingConversion {
	if !p.admitted {
		return NewEncodingConversion(6, 6, EncodingConversionHandler{})
	}
	return p.incoming
}
func (p ClientEncodingPolicy) Outgoing() EncodingConversion {
	if !p.admitted {
		return NewEncodingConversion(6, 6, EncodingConversionHandler{})
	}
	return p.outgoing
}
func (p ClientEncodingPolicy) Decode(ctx context.Context, raw string) (string, error) {
	return p.Incoming().Convert(ctx, raw)
}
func (p ClientEncodingPolicy) Encode(ctx context.Context, raw string) (string, error) {
	return p.Outgoing().Convert(ctx, raw)
}
func (p ClientEncodingPolicy) Append(ctx context.Context, dst []byte, raw string) ([]byte, error) {
	return p.Outgoing().Append(ctx, dst, raw)
}

func (p ClientEncodingPolicy) WithDefaultIdentity(incoming, outgoing, epoch uint64) ClientEncodingPolicy {
	p.defaultIdentity = [3]uint64{incoming, outgoing, epoch}
	return p
}
func (p ClientEncodingPolicy) DefaultIdentity() [3]uint64 { return p.defaultIdentity }

func (env InputEnvironment) DecodeTransport(ctx context.Context, raw string) (string, error) {
	if env.ByteTransport {
		return env.Transport.Decode(ctx, raw)
	}
	if err := ValidateTextInput(raw); err != nil {
		return "", err
	}
	return raw, ctx.Err()
}
func (env OutputEnvironment) AppendTransport(ctx context.Context, dst []byte, raw string) ([]byte, error) {
	if env.ByteTransport {
		return env.Transport.Append(ctx, dst, raw)
	}
	return append(dst, raw...), ctx.Err()
}

// CleanApplicationName is pg_clean_ascii: sanitize every byte, not every rune.
func CleanApplicationName(raw string) string {
	const hex = "0123456789abcdef"
	first := 0
	for first < len(raw) && raw[first] >= 32 && raw[first] <= 126 {
		first++
	}
	if first == len(raw) {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw))
	b.WriteString(raw[:first])
	for i := first; i < len(raw); i++ {
		c := raw[i]
		if c >= 32 && c <= 126 {
			b.WriteByte(c)
		} else {
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
