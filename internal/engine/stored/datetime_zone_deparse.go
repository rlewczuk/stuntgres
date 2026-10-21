package stored

import (
	"context"
	"stuntgres/internal/sqltype"
)

func datetimeZoneTimetzDeparse(ctx context.Context, value sqltype.Value, typ, text string, quote func(string) string) (string, error) {
	west, err := value.TimeZoneOffset()
	if err != nil {
		return "", err
	}
	// Ordinary and existing POSIX-derived values retain the current Deparser
	// output. Only conversion-created displacements need executable reconstruction.
	if west < -169*3600 || west > 168*3600 {
		literal, err := value.Type().SQLLiteral(ctx, value)
		if err != nil {
			return "", err
		}
		return "(" + literal + ")::" + typ, nil
	}
	return quote(text) + "::" + typ, nil
}
