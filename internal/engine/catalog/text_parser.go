package catalog

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
)

// TSParser has no owner or ACL. Published descriptors and callback identities
// are immutable; rename and schema changes replace the descriptor.
type TSParser struct {
	ObjectAddress, ObjectNamespace       ids.ObjectAddress
	ObjectRevision                       ids.DefinitionRevision
	ObjectName                           string
	Start, Token, End, Headline, LexType ids.OID
}

func (d *TSParser) Address() ids.ObjectAddress       { return d.ObjectAddress }
func (d *TSParser) Namespace() ids.ObjectAddress     { return d.ObjectNamespace }
func (d *TSParser) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (d *TSParser) Name() string                     { return d.ObjectName }
func (*TSParser) Kind() ids.ObjectKind               { return ids.TSParserObject }
func (d *TSParser) Seeded() bool {
	return d.Address() == (ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722})
}

func (d *TSParser) Dependencies() []ids.ObjectAddress {
	out := make([]ids.ObjectAddress, 1, 6)
	out[0] = d.Namespace()
	for _, oid := range [...]ids.OID{d.Start, d.Token, d.End, d.Headline, d.LexType} {
		a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		if oid != 0 && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func (s *State) TSParser(a ids.ObjectAddress) (*TSParser, bool) { return descriptorAs[*TSParser](s, a) }
func (s *State) MustTSParser(a ids.ObjectAddress) *TSParser     { return s.Get(a).(*TSParser) }
func (r Read) TSParser(a ids.ObjectAddress) (*TSParser, bool)   { return r.state.TSParser(a) }
func (r Read) MustTSParser(a ids.ObjectAddress) *TSParser       { return r.state.MustTSParser(a) }

// ParserMethodSignature is the exact SQL contract, not an expression-call
// signature and not a native implementation capability check.
func ParserMethodSignature(method string) (ProcedureSignature, ids.OID) {
	switch method {
	case "start":
		return "2281,23", 2281
	case "gettoken":
		return "2281,2281,2281", 2281
	case "end":
		return "2281", 2278
	case "headline":
		return "2281,2281,3615", 2281
	case "lextypes":
		return "2281", 2281
	}
	return "", 0
}

func ParserRoutineResult(v Read, oid ids.OID) (ids.OID, bool) {
	if p, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}); ok {
		return p.Result, true
	}
	return BuiltinRoutineResultByOID(oid)
}

type TSParserProvider struct{}

func (TSParserProvider) ClassID() ids.OID { return ids.TSParserClassID }
func (TSParserProvider) Validate(v Read, raw Descriptor) error {
	d, ok := raw.(*TSParser)
	invalid := func() error { return fmt.Errorf("stuntgres: invalid text search parser descriptor authority") }
	if !ok || d == nil || v.Owner() == nil || v.State() == nil || v.Get(d.Address()) != d ||
		d.Address().ClassID != ids.TSParserClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 ||
		d.Revision() == 0 || d.Name() == "" || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return invalid()
	}
	if _, ok := v.Namespace(d.Namespace()); !ok {
		return invalid()
	}
	for _, method := range [...]struct {
		name string
		oid  ids.OID
	}{{"start", d.Start}, {"gettoken", d.Token}, {"end", d.End}, {"headline", d.Headline}, {"lextypes", d.LexType}} {
		if method.oid == 0 {
			if method.name == "headline" {
				continue
			}
			return invalid()
		}
		expected, result := ParserMethodSignature(method.name)
		var signature ProcedureSignature
		if p, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.oid}); ok {
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.oid}
			if p.Address() != address || p.Revision() == 0 || p.Name() == "" || v.NameAddress(DescriptorNameKey(p)) != address {
				return invalid()
			}
			if _, ok := v.Namespace(p.Namespace()); !ok {
				return invalid()
			}
			signature = p.Signature
		} else if p, ok := BuiltinRoutineIdentityByOID(method.oid); ok {
			signature = p.Signature
		} else {
			return invalid()
		}
		if signature != expected {
			return invalid()
		}
		actual, ok := ParserRoutineResult(v, method.oid)
		if !ok || actual != result {
			return invalid()
		}
	}
	return nil
}

func InstallBuiltinTSParsers(s *State) {
	installSeedDescriptor(s, &TSParser{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
		ObjectRevision:  1, ObjectName: "default",
		Start: 3717, Token: 3718, End: 3719, Headline: 3720, LexType: 3721,
	})
}

// ResolveParserRuntime certifies current descriptor authority before copying
// callback identities into a catalog-free leaf capability.
func ResolveParserRuntime(v Read, d *TSParser) (fts.ParserRuntime, error) {
	if err := (TSParserProvider{}).Validate(v, d); err != nil {
		return fts.ParserRuntime{}, err
	}
	return fts.NewParserRuntime(uint32(d.Start), uint32(d.Token), uint32(d.End), uint32(d.Headline), uint32(d.LexType)), nil
}

func TextParserTokens() [23]struct{ Alias, Description string } { return fts.DocumentTypes() }
