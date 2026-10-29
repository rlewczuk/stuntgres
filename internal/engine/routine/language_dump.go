package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// LanguageDumpStatement renders current language routes in the destination's
// installed handler namespace. Both initial replay and per-routine validator
// windows use this owner, so omission and restoration serialize identical routes.
func LanguageDumpStatement(view catalog.Read, d *catalog.Language, replace bool) (string, error) {
	statement := "CREATE "
	if replace {
		statement += "OR REPLACE "
	}
	if d.Trusted {
		statement += "TRUSTED "
	}
	statement += "LANGUAGE " + catalog.QuoteIdentifier(d.Name())
	for i, address := range [...]ids.ObjectAddress{d.Call, d.Inline, d.Validator} {
		if address == (ids.ObjectAddress{}) {
			continue
		}
		handler, ok := view.Procedure(address)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing language replay handler")
		}
		namespace, ok := view.Namespace(handler.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing language replay handler namespace")
		}
		statement += []string{" HANDLER ", " INLINE ", " VALIDATOR "}[i] + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(handler.Name())
	}
	return statement + ";\n", nil
}
