package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// parameterProvider validates parameter ACL identities against the engine's
// immutable setting registry. The pointer borrows the Engine-owned value, which
// is initialized before provider registration and never replaced or mutated.
type parameterProvider struct{ settings *settings.Registry }

func (parameterProvider) ClassID() ids.OID { return ids.ParameterClassID }
func (p parameterProvider) Validate(v catalog.Read, descriptor catalog.Descriptor) error {
	d, ok := descriptor.(*catalog.Parameter)
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid parameter descriptor authority")
	}
	name, err := canonicalGrantParameter(*p.settings, d.Name())
	if err != nil || name != d.Name() || d.Address().ClassID != ids.ParameterClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid parameter descriptor")
	}
	return nil
}

// Registered settings and valid custom dotted names are the only native
// targets. In particular, PostgreSQL aliases for absent providers do not invent
// settings here. This never calls normalization or default-provider callbacks.
func (e *Engine) canonicalGrantParameter(name string) (string, error) {
	return canonicalGrantParameter(e.settings, name)
}

func canonicalGrantParameter(registry settings.Registry, name string) (string, error) {
	key := settings.FoldName(name)
	if _, ok := registry.Definitions()[key]; ok {
		return key, nil
	}
	if settings.CustomName(key) {
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
	if address := v.Catalog.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: key}); address != (ids.ObjectAddress{}) {
		d, ok := v.Catalog.Parameter(address)
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
	return &catalog.Parameter{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: key, Owner: e.roles.domain.Principal(authorization.BootstrapPrincipalID)}, nil
}
