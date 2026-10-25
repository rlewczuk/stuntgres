package settings

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// normalizeCatalogName implements GUC_IS_NAME, without SQL identifier folding.
// Catalog hooks see the clipped candidate; its NOTICE precedes hook failure too.
func normalizeCatalogName(input string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
		return "", nil, errors.New("stuntgres: invalid UTF-8 or NUL setting")
	}
	if len(input) <= 63 {
		return input, nil, nil
	}
	n := 63
	for !utf8.RuneStart(input[n]) {
		n--
	}
	value := input[:n]
	return value, &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, input, value)}, nil
}
