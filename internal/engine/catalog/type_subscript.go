package catalog

import "fmt"

// TypeSubscriptProvider is an extension implementation whose installed base
// type declares a subscripting handler (pg_type.typsubscript).
type TypeSubscriptProvider interface {
	// TypeSubscriptDeclaration returns the handler's declaration slot for the
	// type declared at the given slot, if that type has one.
	TypeSubscriptDeclaration(int) (int, bool)
}

// InstalledTypeSubscript returns the subscripting handler d's installation
// declared for it. A type without one reports false. The handler shares d's
// installation even after ALTER EXTENSION DROP detaches its membership.
func InstalledTypeSubscript(v Read, d *Type) (*Procedure, bool, error) {
	installation, ok := OriginInstallation(d)
	if !ok {
		return nil, false, nil
	}
	provider, ok := installation.Implementation.(TypeSubscriptProvider)
	if !ok {
		return nil, false, nil
	}
	slot, ok := provider.TypeSubscriptDeclaration(d.Origin.Declaration)
	if !ok {
		return nil, false, nil
	}
	if slot < 0 || slot >= len(installation.Members) {
		return nil, false, fmt.Errorf("stuntgres: invalid installed subscript handler slot")
	}
	handler, exists := v.Procedure(installation.Members[slot])
	if !exists || !SameInstallation(handler, installation) || handler.Signature != "2281" || handler.Result != 2281 {
		return nil, false, fmt.Errorf("stuntgres: missing installed subscript handler")
	}
	return handler, true, nil
}
