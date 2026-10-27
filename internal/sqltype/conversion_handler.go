package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqltype/pgencoding18"
)

// EncodingConversionHandler is an immutable release capability, independent of
// SQL routine/default metadata. Its consumed count is the real conversion ABI.
type EncodingConversionHandler struct {
	name                string
	source, destination int
}

func RegisteredEncodingConversion(name string) (EncodingConversionHandler, bool) {
	source, destination, ok := pgencoding18.ConversionRoutineArguments(name)
	return EncodingConversionHandler{name: name, source: source, destination: destination}, ok
}

func (h EncodingConversionHandler) Name() string { return h.name }

func (h EncodingConversionHandler) Supports(source, destination int) bool {
	return h.name != "" && pgencoding18.ConversionRoutine(source, destination) == h.name
}

func (h EncodingConversionHandler) Convert(ctx context.Context, raw string, source, destination int) (output string, consumed int, err error) {
	return h.convert(ctx, raw, source, destination, false)
}

// ConvertValidated consumes the entire-source validation performed at the SQL
// execution frontier, avoiding a second full pass over immutable input bytes.
func (h EncodingConversionHandler) ConvertValidated(ctx context.Context, raw string, source, destination int) (output string, consumed int, err error) {
	return h.convert(ctx, raw, source, destination, true)
}

func (h EncodingConversionHandler) convert(ctx context.Context, raw string, source, destination int, validated bool) (output string, consumed int, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if h.source >= 0 && source != h.source {
		err = failure("XX000", fmt.Sprintf("expected source encoding %q, but got %q", EncodingName(h.source), EncodingName(source)))
		return
	}
	if h.destination >= 0 && destination != h.destination {
		err = failure("XX000", fmt.Sprintf("expected destination encoding %q, but got %q", EncodingName(h.destination), EncodingName(destination)))
		return
	}
	if !h.Supports(source, destination) {
		encoding := destination
		if h.source < 0 {
			encoding = source
		}
		switch h.name {
		case "utf8_to_win", "win_to_utf8":
			err = failure("XX000", fmt.Sprintf("unexpected encoding ID %d for WIN character sets", encoding))
		case "utf8_to_iso8859", "iso8859_to_utf8":
			err = failure("XX000", fmt.Sprintf("unexpected encoding ID %d for ISO 8859 character sets", encoding))
		default:
			err = failure("XX000", "unregistered encoding conversion provider")
		}
		return
	}
	if !validated {
		if err = binaryEncodingValidate(ctx, raw, source); err != nil {
			return
		}
	}
	if raw == "" {
		return "", 0, nil
	}
	output, err = emitBinaryEncoding(ctx, raw, source, destination)
	if err == nil {
		consumed = len(raw)
	}
	return
}

// EncodingID preserves CREATE's raw Sconst length gate, unlike NAME coercion.
func EncodingID(name string) int { return pgencoding18.ID(name) }

func EncodingName(id int) string { return pgencoding18.Name(id) }
