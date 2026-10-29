package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
)

// samePreparedInstalledScalar permits only candidate-local datum reownership.
// It never certifies retained executable analysis or replaces environment,
// dependency-purpose, domain, epoch or principal admission by the caller.
func samePreparedInstalledScalar(read catalog.Read, old, current *catalog.Type) bool {
	if old == nil || current == nil || old.SQLType == nil || current.SQLType == nil ||
		!old.Origin.Valid(old.Address()) || !current.Origin.Valid(current.Address()) ||
		old.Origin.Installation != current.Origin.Installation || old.Origin.Declaration != current.Origin.Declaration {
		return false
	}
	source := old.SQLType
	if !source.IsHstore() && !source.IsLquery() && !source.IsLtree() && !source.IsLtxtquery() &&
		!source.IsEan13() && !source.IsIsbn() && !source.IsIsbn13() && !source.IsIsmn() &&
		!source.IsIsmn13() && !source.IsIssn() && !source.IsIssn13() && !source.IsUpc() {
		return false
	}
	if err := extension.ValidateTypeOrigin(read, current); err != nil {
		return false
	}
	// These scalar recipes place the actual input/output declarations directly
	// after their nominal type. Validate the surviving codecs, not a live header
	// or unrelated current member inventory, before authorizing a datum import.
	installation, slot := current.Origin.Installation, current.Origin.Declaration
	for offset := 1; offset <= 2; offset++ {
		if slot+offset >= len(installation.Members) {
			return false
		}
		codec, ok := read.Procedure(installation.Members[slot+offset])
		if !ok || !catalog.SameInstallation(codec, installation) {
			return false
		}
		if _, err := extension.ProcedureSymbol(read, codec); err != nil {
			return false
		}
	}
	// Membership and owner/ACL are mutable authority, checked afresh by binding.
	// All other nominal definition and generation checks remain the shared owner.
	nominal := *old
	nominal.Owner, nominal.ACL, nominal.Extension = current.Owner, current.ACL, current.Extension
	return catalog.SamePreparedTypeDefinition(&nominal, current)
}
