package sqltype

import "fmt"

// CheckStoredColumn rejects query-only identities at the final named storage
// boundary. Lookup and expression annotation remain independent capabilities.
func CheckStoredColumn(name string, usage Usage) error {
	if usage.Type != nil && IsPseudo(usage.Type.ID()) {
		return failure("42P16", fmt.Sprintf(`column "%s" has pseudo-type %s`, name, usage.Type.DiagnosticName()))
	}
	return nil
}
