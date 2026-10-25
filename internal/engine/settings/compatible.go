package settings

import "stuntgres/internal/sqlstate"

func normalizeBackslashQuote(input string) (string, *sqlstate.Error, error) {
	switch FoldName(input) {
	case "safe_encoding":
		return "safe_encoding", nil, nil
	case "on", "true", "yes", "1":
		return "on", nil, nil
	case "off", "false", "no", "0":
		return "off", nil, nil
	}
	return "", nil, (Definition{Name: "backslash_quote"}).invalid(input, "Available values: safe_encoding, on, off.")
}
