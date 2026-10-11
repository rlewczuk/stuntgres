package sqltype

import (
	"context"
	"strings"
)

// JSONPath's XQuery flags remain an adapter, not PostgreSQL SQL options.
func validateJSONPathRegex(ctx context.Context, pattern, flags string) (uint8, error) {
	var bits uint8
	for _, c := range flags {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		i := strings.IndexRune("ismxq", c)
		if i < 0 {
			return 0, jsonpathDetail("42601", "Unrecognized flag character \""+string(c)+"\" in LIKE_REGEX predicate.")
		}
		bits |= 1 << i
	}
	if bits&16 != 0 {
		return bits, ctx.Err()
	}
	if bits&8 != 0 {
		return 0, failure("0A000", `XQuery "x" flag (expanded regular expressions) is not implemented`)
	}
	_, err := compilePGRegexBudget(ctx, pattern, PGRegexOptions{CaseInsensitive: bits&1 != 0}, CollationRef{ID: 100, Derivation: Default}, pgRegexMemoryLimit, true)
	if err != nil {
		return 0, err
	}
	return bits, nil
}
