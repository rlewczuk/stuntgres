package extension

import "stuntgres/internal/engine/catalog"

// Shared by current-installation admission and logical owner metadata. Never
// invoke this on canonical recipe snapshots: only actual surviving descriptors
// have a current class provider, namespace, generation and reciprocal membership.
func validateInstalledDeclaration(view catalog.Read, d catalog.Descriptor) error {
	if provider := view.Owner().Provider(d.Address().ClassID); provider != nil {
		return provider.Validate(view, d)
	}
	if len(view.Owner().Providers) != 0 {
		return InvalidProviderDescriptor()
	}
	// Pure identity units intentionally own no engine/provider registry.
	if p, ok := d.(*catalog.Procedure); ok {
		if p.PLHandler != catalog.NoPLHandler {
			return catalog.ValidatePLHandler(view, p)
		}
		_, err := ProcedureSymbol(view, p)
		return err
	}
	if typ, ok := d.(*catalog.Type); ok {
		return ValidateTypeOrigin(view, typ)
	}
	return InvalidProviderDescriptor()
}
