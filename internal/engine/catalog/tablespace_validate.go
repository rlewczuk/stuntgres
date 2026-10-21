package catalog

import (
	"path"
	"strings"

	"stuntgres/internal/sqlstate"
)

const tablespaceLocationLimit = 970

// The release reserves 54 bytes for the version directory, separators, OIDs
// and fork name. Validate canonical POSIX bytes without probing the host.
func ValidateTablespaceLocation(location string) (string, error) {
	if location != "" {
		location = path.Clean(location)
	}
	if strings.ContainsRune(location, '\'') {
		return location, sqlError("42602", "tablespace location cannot contain single quotes")
	}
	if !path.IsAbs(location) {
		return location, sqlError("42P17", "tablespace location must be an absolute path")
	}
	if len(location) > tablespaceLocationLimit {
		return location, sqlError("42P17", "tablespace location \""+location+"\" is too long")
	}
	return location, nil
}

// Names have already undergone lexer normalization and byte-safe truncation.
// The existing flag is a lookup result, not a tablespace publication facility.
func ValidateTablespaceName(name string, existing bool) error {
	if strings.HasPrefix(name, "pg_") {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42939", Message: "unacceptable tablespace name \"" + name + "\"", Detail: "The prefix \"pg_\" is reserved for system tablespaces."}
	}
	if existing {
		return sqlError("42710", "tablespace \""+name+"\" already exists")
	}
	return nil
}
