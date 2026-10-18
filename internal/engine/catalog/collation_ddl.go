package catalog

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

func CollationSemantics(d *Collation) (uint32, error) {
	switch d.Provider {
	case "c":
		if d.Collate == nil || d.CType == nil || d.Locale != nil {
			return 0, expr.ErrorAt("42P17", "invalid libc collation properties", parser.Position{})
		}
		if (*d.Collate != "C" && *d.Collate != "POSIX") || (*d.CType != "C" && *d.CType != "POSIX") {
			return 0, expr.ErrorAt("0A000", "libc locale provider data is not available in this engine", parser.Position{})
		}
		return 950, nil
	case "b":
		if d.Locale == nil || d.Collate != nil || d.CType != nil {
			return 0, expr.ErrorAt("42P17", "invalid builtin collation properties", parser.Position{})
		}
		switch *d.Locale {
		case "C":
			return 950, nil
		case "C.UTF-8", "C.UTF8":
			return 811, nil
		case "PG_UNICODE_FAST":
			return 6411, nil
		}
		return 0, expr.ErrorAt("42809", fmt.Sprintf(`invalid locale name "%s" for builtin provider`, *d.Locale), parser.Position{})
	case "i":
		return 0, expr.ErrorAt("0A000", "ICU is not supported in this build", parser.Position{})
	}
	return 0, expr.ErrorAt("42P17", "unrecognized collation provider: "+d.Provider, parser.Position{})
}
