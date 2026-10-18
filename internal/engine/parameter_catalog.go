package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// A parameter descriptor represents an ACL, not a setting value or provider.
// Baseline-equivalent ACLs have no catalog row.
type parameterDescriptor struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *parameterDescriptor) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *parameterDescriptor) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*parameterDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *parameterDescriptor) Name() string                     { return d.ObjectName }
func (*parameterDescriptor) Kind() ids.ObjectKind               { return ids.ParameterObject }

// parameterProvider validates parameter ACL identities against the engine's
// setting definitions, which close at construction.
type parameterProvider struct{ definitions map[string]SettingDefinition }

func (parameterProvider) ClassID() ids.OID { return ids.ParameterClassID }
func (p parameterProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*parameterDescriptor)
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d {
		return fmt.Errorf("stuntgres: invalid parameter descriptor authority")
	}
	name, err := canonicalGrantParameter(p.definitions, d.Name())
	if err != nil || name != d.Name() || d.Address().ClassID != ids.ParameterClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(descriptorNameKey(d)) != d.Address() {
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
func (e *Engine) grantParameterDescriptorLocked(v AuthorizationView, name string) (*parameterDescriptor, error) {
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
	return &parameterDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: oid}, ObjectRevision: revision, ObjectName: key, Owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}, nil
}

// installParameterGrant changes only the detached central catalog candidate.
// Both the descriptor and name entry disappear when the ACL returns to sA for
// the owner alone. Explicit empty ACLs are not the default baseline.
func installParameterGrant(candidate *catalogState, d *parameterDescriptor) {
	if !d.ACL.Explicit || authorization.MatchesDefaultBaseline(d.ACL, d.Owner, authorization.DefaultParameters, false) {
		candidate.removeDescriptor(d.Address())
		candidate.unbindName(descriptorNameKey(d))
		return
	}
	candidate.setDescriptor(d.Address(), d)
	candidate.bindName(descriptorNameKey(d), d.Address())
}
