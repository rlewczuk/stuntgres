package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// parameterProvider validates parameter ACL identities against the engine's
// setting definitions, which close at construction.
type parameterProvider struct{ definitions map[string]SettingDefinition }

func (parameterProvider) ClassID() ids.OID { return ids.ParameterClassID }
func (p parameterProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*catalog.Parameter)
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid parameter descriptor authority")
	}
	name, err := canonicalGrantParameter(p.definitions, d.Name())
	if err != nil || name != d.Name() || d.Address().ClassID != ids.ParameterClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid parameter descriptor")
	}
	return nil
}

// Registered settings and valid custom dotted names are the only native
// targets. In particular, PostgreSQL aliases for absent providers do not invent
// settings here. This never calls normalization or default-provider callbacks.
func (e *Engine) canonicalGrantParameter(name string) (string, error) {
	return canonicalGrantParameter(e.settingsDefinitions, name)
}

func canonicalGrantParameter(definitions map[string]SettingDefinition, name string) (string, error) {
	key := settingFold(name)
	if _, ok := definitions[key]; ok {
		return key, nil
	}
	if customName(key) {
		return key, nil
	}
	return "", sqlError("42704", fmt.Sprintf(`unrecognized configuration parameter "%s"`, name))
}

// grantParameterDescriptorLocked returns an existing ACL identity or an
// unpublished baseline identity for the central grant candidate. The caller
// owns Engine.mu and publishes only after all targets and ACL changes succeed.
func (e *Engine) grantParameterDescriptorLocked(v AuthorizationView, name string) (*catalog.Parameter, error) {
	key, err := e.canonicalGrantParameter(name)
	if err != nil {
		return nil, err
	}
	if address := v.catalog.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: key}); address != (ids.ObjectAddress{}) {
		d, ok := v.catalog.Parameter(address)
		if !ok || d == nil {
			return nil, fmt.Errorf("stuntgres: invalid parameter catalog identity")
		}
		return d, nil
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	return &catalog.Parameter{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: key, Owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}, nil
}

// installParameterGrant changes only the detached central catalog candidate.
// Both the descriptor and name entry disappear when the ACL returns to sA for
// the owner alone. Explicit empty ACLs are not the default baseline.
func installParameterGrant(candidate *catalogState, d *catalog.Parameter) {
	if !d.ACL.Explicit || authorization.MatchesDefaultBaseline(d.ACL, d.Owner, authorization.DefaultParameters, false) {
		candidate.RemoveDescriptor(d.Address())
		candidate.UnbindName(catalog.DescriptorNameKey(d))
		return
	}
	candidate.SetDescriptor(d.Address(), d)
	candidate.BindName(catalog.DescriptorNameKey(d), d.Address())
}
