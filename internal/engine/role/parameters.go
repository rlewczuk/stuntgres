package role

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// ParameterProvider validates parameter ACL identities against the engine's
// immutable setting registry. The pointer borrows the Engine-owned value, which
// is initialized before provider registration and never replaced or mutated.
type ParameterProvider struct{ Settings *settings.Registry }

func (ParameterProvider) ClassID() ids.OID { return ids.ParameterClassID }
func (p ParameterProvider) Validate(v catalog.Read, descriptor catalog.Descriptor) error {
	d, ok := descriptor.(*catalog.Parameter)
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid parameter descriptor authority")
	}
	name, err := CanonicalGrantParameter(*p.Settings, d.Name())
	if err != nil || name != d.Name() || d.Address().ClassID != ids.ParameterClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid parameter descriptor")
	}
	return nil
}

// CanonicalGrantParameter folds a GRANT ON PARAMETER name to its setting key.
// Registered settings and valid custom dotted names are the only native
// targets. In particular, PostgreSQL aliases for absent providers do not invent
// settings here. This never calls normalization or default-provider callbacks.
func CanonicalGrantParameter(registry settings.Registry, name string) (string, error) {
	key := settings.FoldName(name)
	if _, ok := registry.Definitions()[key]; ok {
		return key, nil
	}
	if settings.CustomName(key) {
		return key, nil
	}
	return "", sqlError("42704", fmt.Sprintf(`unrecognized configuration parameter "%s"`, name))
}

// GrantParameterDescriptorLocked returns the existing ACL identity of
// parameter name in view, or an unpublished baseline identity for the central
// grant candidate, owned by the bootstrap superuser of principals, with an OID
// and a revision from the engine's allocators. The caller owns Engine.mu and
// publishes only after all targets and ACL changes succeed.
func GrantParameterDescriptorLocked(registry settings.Registry, view catalog.Read, name string, principals *authorization.Domain, oid func() (ids.OID, error), revision func() (ids.DefinitionRevision, error)) (*catalog.Parameter, error) {
	key, err := CanonicalGrantParameter(registry, name)
	if err != nil {
		return nil, err
	}
	if address := view.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: key}); address != (ids.ObjectAddress{}) {
		d, ok := view.Parameter(address)
		if !ok || d == nil {
			return nil, fmt.Errorf("stuntgres: invalid parameter catalog identity")
		}
		return d, nil
	}
	object, err := oid()
	if err != nil {
		return nil, err
	}
	stamp, err := revision()
	if err != nil {
		return nil, err
	}
	return &catalog.Parameter{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: object}, ObjectRevision: stamp, ObjectName: key, Owner: principals.Principal(authorization.BootstrapPrincipalID)}, nil
}
