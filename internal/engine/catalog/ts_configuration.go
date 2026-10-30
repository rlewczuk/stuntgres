package catalog

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// TSMapping owns its ordered dictionary identities. Published slices are immutable.
type TSMapping struct {
	TokenID      int32
	Dictionaries []ids.ObjectAddress
}

type TSConfiguration struct {
	ObjectAddress, ObjectNamespace, Parser ids.ObjectAddress
	Extension                              ids.ObjectAddress
	ObjectRevision                         ids.DefinitionRevision
	ObjectName                             string
	Owner                                  authorization.PrincipalRef
	Mappings                               []TSMapping
	seed                                   snowballSeed
}

func (d *TSConfiguration) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *TSConfiguration) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *TSConfiguration) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *TSConfiguration) Name() string                     { return d.ObjectName }
func (*TSConfiguration) Kind() ids.ObjectKind               { return ids.TSConfigurationObject }
func (d *TSConfiguration) Seeded() bool {
	return d.seed != noSnowballSeed || d.Pinned()
}
func (d *TSConfiguration) Pinned() bool {
	return d.Address() == (ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
}
func CloneTSMappings(in []TSMapping) []TSMapping {
	out := slices.Clone(in)
	for i := range out {
		out[i].Dictionaries = slices.Clone(in[i].Dictionaries)
	}
	return out
}
func (d *TSConfiguration) Mapping(token int32) []ids.ObjectAddress {
	i, ok := slices.BinarySearchFunc(d.Mappings, token, func(m TSMapping, t int32) int {
		if m.TokenID < t {
			return -1
		}
		if m.TokenID > t {
			return 1
		}
		return 0
	})
	if !ok {
		return nil
	}
	return d.Mappings[i].Dictionaries
}

// Dependencies are derived from the current descriptor; COPY provenance is absent.
func (d *TSConfiguration) Dependencies() []ids.ObjectAddress {
	out := []ids.ObjectAddress{d.Namespace(), d.Parser}
	for _, m := range d.Mappings {
		for _, a := range m.Dictionaries {
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}
func (s *State) TSConfiguration(a ids.ObjectAddress) (*TSConfiguration, bool) {
	return descriptorAs[*TSConfiguration](s, a)
}
func (s *State) MustTSConfiguration(a ids.ObjectAddress) *TSConfiguration {
	return s.Get(a).(*TSConfiguration)
}
func (r Read) TSConfiguration(a ids.ObjectAddress) (*TSConfiguration, bool) {
	return r.state.TSConfiguration(a)
}
func (r Read) MustTSConfiguration(a ids.ObjectAddress) *TSConfiguration {
	return r.state.MustTSConfiguration(a)
}

type TSConfigurationProvider struct{}

func (TSConfigurationProvider) ClassID() ids.OID { return ids.TSConfigurationClassID }
func (TSConfigurationProvider) Validate(v Read, raw Descriptor) error {
	d, ok := raw.(*TSConfiguration)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid text search configuration descriptor authority") }
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d || d.Address().ClassID != ids.TSConfigurationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || d.Name() == "" || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if v.Owner().Principals == nil || d.Owner.ID() == 0 || d.Owner != v.Owner().Principals.Principal(d.Owner.ID()) {
		return invalid()
	}
	if _, ok := v.Namespace(d.Namespace()); !ok {
		return invalid()
	}
	parser, ok := v.TSParser(d.Parser)
	if !ok {
		return invalid()
	}
	if err := (TSParserProvider{}).Validate(v, parser); err != nil {
		return err
	}
	var previous int32
	for _, m := range d.Mappings {
		if m.TokenID <= previous || (parser.LexType == 3721 && int(m.TokenID) > len(TextParserTokens())) || len(m.Dictionaries) == 0 {
			return invalid()
		}
		previous = m.TokenID
		for _, a := range m.Dictionaries {
			dict, ok := v.TSDictionary(a)
			if !ok || dict.Runtime == nil || a.ClassID != ids.TSDictionaryClassID || a.SubID != 0 {
				return invalid()
			}
			p := v.Owner().Providers[a.ClassID]
			if p == nil {
				return invalid()
			}
			if err := p.Validate(v, dict); err != nil {
				return err
			}
		}
	}
	return ValidateExtensionMembership(v, d)
}
func InstallBuiltinTSConfigurations(state *State, owner authorization.PrincipalRef) {
	d := &TSConfiguration{ObjectAddress: ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Parser: ids.ObjectAddress{ClassID: 3601, ObjectID: 3722}, ObjectRevision: 1, ObjectName: "simple", Owner: owner}
	for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		d.Mappings = append(d.Mappings, TSMapping{TokenID: token, Dictionaries: []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}}})
	}
	installSeedDescriptor(state, d)
}
