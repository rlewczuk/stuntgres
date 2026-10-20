package analyze

import (
	"strings"

	"stuntgres/internal/sqltype"
)

// PGType spells u as stored SQL names it (text, boolean, character varying,
// time without time zone, ...).
func PGType(u sqltype.Usage) (string, error) {
	declaration, err := u.Declaration()
	if err != nil {
		return "", err
	}
	if u.Type.IsUserDefined() || u.Type.IsDomain() {
		return declaration, nil
	}
	switch u.Type.ID() {
	case sqltype.Text:
		return "text", nil
	case sqltype.Bool:
		return "boolean", nil
	case sqltype.Time, sqltype.Timestamp:
		return strings.ToLower(declaration) + " without time zone", nil
	case sqltype.VarChar:
		return strings.Replace(strings.ToLower(declaration), "varchar", "character varying", 1), nil
	case sqltype.Char:
		return `"char"`, nil
	case sqltype.BPChar:
		if u.Typmod < 0 {
			return "bpchar", nil
		}
	}
	return strings.ToLower(declaration), nil
}
