package core

import (
	"strings"
	"stuntgres/internal/sqltype"
)

// Result descriptor errors preserve typmods while borrowing the caller's type
// visibility. A nil resolver retains the descriptor's builtin diagnostic name.
func resultUsageName(u sqltype.Usage, resolve func(*sqltype.Descriptor) string) string {
	if u.Type.IsArray() && !u.Type.IsCatalogVector() {
		u.Type = u.Type.Element()
		return resultUsageName(u, resolve) + "[]"
	}
	name := u.Type.DiagnosticName()
	if resolve != nil {
		name = resolve(u.Type)
	}
	if u.Typmod < 0 {
		return name
	}
	declaration, err := u.Declaration()
	if err != nil {
		return name
	}
	if u.Type.ID() == sqltype.Interval {
		return strings.ToLower(declaration)
	}
	if at := strings.IndexByte(declaration, '('); at >= 0 {
		end := strings.IndexByte(declaration[at:], ')')
		if end < 0 {
			return name
		}
		modifier := declaration[at : at+end+1]
		if zone := strings.Index(name, " with"); zone >= 0 {
			return name[:zone] + modifier + name[zone:]
		}
		return name + modifier
	}
	return name
}
