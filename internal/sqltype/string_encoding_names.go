package sqltype

import "stuntgres/internal/sqltype/pgencoding18"

// ClientEncodingName uses the release GUC hook's NAME-sized candidate on every
// entry point, including set_config TEXT input. Identity lookup itself is shared
// with the existing binary conversion functions.
func ClientEncodingName(input string) (candidate, canonical string) {
	candidate = clipName(input)
	id := pgencoding18.ID(candidate)
	if id >= 0 {
		canonical = pgencoding18.Name(id)
	}
	return candidate, canonical
}
