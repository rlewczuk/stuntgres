package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// A parameter descriptor represents an ACL, not a setting value or provider.
// Baseline-equivalent ACLs have no catalog row.
type Parameter struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *Parameter) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Parameter) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Parameter) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Parameter) Name() string { return d.ObjectName }

func (*Parameter) Kind() ids.ObjectKind { return ids.ParameterObject }

// ParameterBaseline reports whether sparse ACL storage must omit this row.
func ParameterBaseline(d *Parameter) bool {
	return !d.ACL.Explicit || authorization.MatchesDefaultBaseline(d.ACL, d.Owner, authorization.DefaultParameters, false)
}

// InstallParameterGrant changes only the detached central catalog candidate.
// Both the descriptor and name entry disappear when the ACL returns to sA for
// the owner alone. Explicit empty ACLs are not the default baseline.
func InstallParameterGrant(candidate *State, d *Parameter) {
	if ParameterBaseline(d) {
		candidate.RemoveDescriptor(d.Address())
		candidate.UnbindName(DescriptorNameKey(d))
		return
	}
	candidate.SetDescriptor(d.Address(), d)
	candidate.BindName(DescriptorNameKey(d), d.Address())
}
