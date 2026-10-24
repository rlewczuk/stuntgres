package pgcatalog

import (
	"strconv"
	"stuntgres/internal/engine/ids"
	"unicode/utf8"
)

// PostgreSQL's nameconcatoid preserves the complete suffix within NAME's
// 63-byte payload, clipping only the prefix on a UTF8 character boundary.
func routineSpecificName(name string, oid ids.OID) string {
	suffix := "_" + strconv.FormatUint(uint64(oid), 10)
	end := 63 - len(suffix)
	if len(name) > end {
		for !utf8.RuneStart(name[end]) {
			end--
		}
		name = name[:end]
	}
	return name + suffix
}
