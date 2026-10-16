package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

const (
	ProcedureClassID    OID = 1255
	ExtensionClassID    OID = 3079
	TSTemplateClassID   OID = 3764
	TSDictionaryClassID OID = 3600
	DatabaseClassID     OID = 1262
)

// Signatures are declared identities, not runtime overload admission. Promoting
// regdictionary's scalar codec does not enable the two-argument unaccent overload.
type procedureSignature string

const (
	unaccentTextSignature       procedureSignature = "25"
	unaccentDictionarySignature procedureSignature = "3769,25"
	unaccentInitSignature       procedureSignature = "2281"
	unaccentLexizeSignature     procedureSignature = "2281,2281,2281,2281"
	conversionSignature         procedureSignature = "23,23,2275,2281,23,16"
)

type extensionDescriptor struct {
	address         ObjectAddress
	revision        DefinitionRevision
	name, version   string
	targetNamespace ObjectAddress
	owner           authorization.PrincipalRef
	members         []ObjectAddress // immutable after detached staging
}

func (d *extensionDescriptor) Address() ObjectAddress       { return d.address }
func (d *extensionDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *extensionDescriptor) Namespace() ObjectAddress     { return ObjectAddress{} }
func (d *extensionDescriptor) Name() string                 { return d.name }
func (*extensionDescriptor) Kind() ObjectKind               { return ExtensionObject }

type procedureDescriptor struct {
	address, namespace, extension ObjectAddress
	revision                      DefinitionRevision
	name                          string
	signature                     procedureSignature
	routineKind                   routineKind
	builtin                       *builtinRoutineDeclaration
	body                          []routineBodyEdge
	owner                         authorization.PrincipalRef
	result                        OID
	volatility                    Volatility
	strict                        bool
	provider                      *unaccentProvider
	queryCodec                    *intarrayProvider
	fuzzy                         *fuzzystrmatchProvider
	crypto                        *pgcryptoProvider
	intagg                        *intaggProvider
	aggregate                     *ownedAggregate
	uuidOssp                      *uuidOsspProvider
	conversion                    sqltype.EncodingConversionHandler
	acl                           authorization.ACLValue
	dictionary                    ObjectAddress
	foreign                       *foreignProcedure
}

func (d *procedureDescriptor) Address() ObjectAddress       { return d.address }
func (d *procedureDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *procedureDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *procedureDescriptor) Name() string                 { return d.name }
func (*procedureDescriptor) Kind() ObjectKind               { return ProcedureObject }

type tsTemplateDescriptor struct {
	address, namespace, extension, init, lexize ObjectAddress
	revision                                    DefinitionRevision
	name                                        string
	provider                                    *unaccentProvider
}

func (d *tsTemplateDescriptor) Address() ObjectAddress       { return d.address }
func (d *tsTemplateDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *tsTemplateDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *tsTemplateDescriptor) Name() string                 { return d.name }
func (*tsTemplateDescriptor) Kind() ObjectKind               { return TSTemplateObject }

type tsDictionaryDescriptor struct {
	address, namespace, extension, template ObjectAddress
	revision                                DefinitionRevision
	name                                    string
	owner                                   authorization.PrincipalRef
	provider                                *unaccentProvider
}

func (d *tsDictionaryDescriptor) Address() ObjectAddress       { return d.address }
func (d *tsDictionaryDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *tsDictionaryDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *tsDictionaryDescriptor) Name() string                 { return d.name }
func (*tsDictionaryDescriptor) Kind() ObjectKind               { return TSDictionaryObject }

func descriptorNameKey(d Descriptor) NameKey {
	key := NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
	if p, ok := d.(*procedureDescriptor); ok {
		key.Signature = string(p.signature)
	}
	if operator, ok := d.(*operatorDescriptor); ok {
		key.Signature = operator.signature()
	}
	return key
}

type extensionCatalogProvider struct{ class OID }

func (p extensionCatalogProvider) ClassID() OID { return p.class }
func (p extensionCatalogProvider) Validate(v CatalogRead, d Descriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extension provider descriptor") }
	a := d.Address()
	if a.ClassID != p.class || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || v.state.names[descriptorNameKey(d)] != a {
		return invalid()
	}
	namespace := d.Namespace()
	if namespace != (ObjectAddress{}) {
		if _, ok := v.state.descriptors[namespace].(*namespaceDescriptor); !ok {
			return invalid()
		}
	}
	var ext ObjectAddress
	switch d := d.(type) {
	case *extensionDescriptor:
		registration, registered := v.owner.extensions[d.name]
		if !registered || d.version != registration.version || d.Namespace() != (ObjectAddress{}) {
			return invalid()
		}
		if _, ok := v.state.descriptors[d.targetNamespace].(*namespaceDescriptor); !ok {
			return invalid()
		}
		seen := map[ObjectAddress]bool{}
		for _, a := range d.members {
			if seen[a] {
				return invalid()
			}
			seen[a] = true
			member := v.state.descriptors[a]
			if member == nil || member.Namespace() != d.targetNamespace || extensionMembership(member) != d.address {
				return invalid()
			}
		}
		if err := registration.provider.validate(v, d); err != nil {
			return err
		}
	case *procedureDescriptor:
		if d.uuidOssp != nil && (d.intagg != nil || d.aggregate != nil || d.foreign != nil || d.builtin != nil) {
			return invalid()
		}
		if d.aggregate != nil && d.intagg != stockIntagg {
			return invalid()
		}
		if d.foreign != nil {
			p := d.foreign
			if p.name.Name != d.name || d.extension != (ObjectAddress{}) || d.provider != nil || d.queryCodec != nil || d.fuzzy != nil || d.crypto != nil || d.intagg != nil || d.conversion.Name() != "" || d.builtin != nil {
				return invalid()
			}
			if p.wrapper != nil {
				if p.validator != nil || d.signature != foreignHandlerSignature || d.result != 3115 {
					return invalid()
				}
			} else if p.validator == nil || d.signature != foreignValidatorSignature || d.result != 2278 {
				return invalid()
			}
			break
		}
		if d.builtin != nil {
			declared := d.builtin
			if builtinRoutineByOID[d.address.ObjectID] != declared || d.namespace != (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) || d.name != declared.identity.name || d.signature != declared.identity.signature || d.routineKind != declared.kind || d.result != declared.result || d.strict != declared.strict || d.volatility != declared.volatility || d.provider != nil || d.queryCodec != nil || d.crypto != nil || d.fuzzy != nil || d.intagg != nil || d.conversion.Name() != "" || d.extension != (ObjectAddress{}) || d.dictionary != (ObjectAddress{}) || d.foreign != nil {
				return invalid()
			}
			break
		}
		if d.intagg != nil {
			if err := d.intagg.validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.extension
			break
		}
		if d.routineKind != functionRoutine {
			return invalid()
		}
		if d.crypto != nil && d.fuzzy != nil {
			return invalid()
		}
		if d.uuidOssp != nil {
			if d.crypto != nil || d.fuzzy != nil || d.provider != nil || d.queryCodec != nil || d.conversion.Name() != "" || d.dictionary != (ObjectAddress{}) {
				return invalid()
			}
			if err := d.uuidOssp.validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.extension
			break
		}
		if d.fuzzy != nil {
			if err := d.fuzzy.validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.extension
			break
		}
		if d.crypto != nil {
			if err := d.crypto.validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.extension
			break
		}
		if d.queryCodec != nil {
			if err := d.queryCodec.validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.extension
			break
		}
		if d.conversion.Name() != "" {
			registered, ok := sqltype.RegisteredEncodingConversion(d.name)
			if !ok || d.conversion != registered || d.provider != nil || d.signature != conversionSignature || d.result != 23 || d.volatility != Immutable || !d.strict || d.extension != (ObjectAddress{}) || d.dictionary != (ObjectAddress{}) || d.namespace != (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) || conversionRoutineName(d.address.ObjectID) != d.name {
				return invalid()
			}
			break
		}
		if d.provider != stockUnaccent {
			return invalid()
		}
		ext = d.extension
		switch d.signature {
		case unaccentTextSignature, unaccentDictionarySignature:
			if d.name != "unaccent" || d.result != 25 || d.volatility != Stable || !d.strict {
				return invalid()
			}
			if ext != (ObjectAddress{}) {
				dict, ok := v.state.descriptors[d.dictionary].(*tsDictionaryDescriptor)
				if !ok || dict.namespace != d.namespace || dict.extension != ext {
					return invalid()
				}
			}
		case unaccentInitSignature:
			if d.name != "unaccent_init" || d.result != 2281 || d.volatility != Volatile || d.strict {
				return invalid()
			}
		case unaccentLexizeSignature:
			if d.name != "unaccent_lexize" || d.result != 2281 || d.volatility != Volatile || d.strict {
				return invalid()
			}
		default:
			return invalid()
		}
	case *tsTemplateDescriptor:
		if d.name != "unaccent" || d.provider != stockUnaccent {
			return invalid()
		}
		ext = d.extension
		for i, a := range [2]ObjectAddress{d.init, d.lexize} {
			proc, ok := v.state.descriptors[a].(*procedureDescriptor)
			if !ok || ext != (ObjectAddress{}) && proc.namespace != d.namespace || proc.extension != ext || proc.signature != [2]procedureSignature{unaccentInitSignature, unaccentLexizeSignature}[i] {
				return invalid()
			}
		}
	case *tsDictionaryDescriptor:
		if d.name != "unaccent" || d.provider != stockUnaccent {
			return invalid()
		}
		ext = d.extension
		tmpl, ok := v.state.descriptors[d.template].(*tsTemplateDescriptor)
		if !ok || ext != (ObjectAddress{}) && tmpl.namespace != d.namespace || tmpl.extension != ext {
			return invalid()
		}
	default:
		return invalid()
	}
	if ext != (ObjectAddress{}) {
		parent, ok := v.state.descriptors[ext].(*extensionDescriptor)
		if !ok || parent.targetNamespace != namespace {
			return invalid()
		}
		found := false
		for _, member := range parent.members {
			found = found || member == a
		}
		if !found {
			return invalid()
		}
	}
	return nil
}
func extensionMembership(d Descriptor) ObjectAddress {
	switch d := d.(type) {
	case *typeDescriptor:
		return d.extension
	case *procedureDescriptor:
		return d.extension
	case *tsTemplateDescriptor:
		return d.extension
	case *tsDictionaryDescriptor:
		return d.extension
	}
	return ObjectAddress{}
}
func procedureArity(p *procedureDescriptor) int64 {
	if p.signature == "" {
		return 0
	}
	return int64(strings.Count(string(p.signature), ",") + 1)
}

// Namespace retirement owns complete installations by their real target, not
// by the extension's database-wide name or generic Namespace field.
func namespaceChildren(base CatalogRead, namespaces map[ObjectAddress]bool) ([]ObjectAddress, error) {
	selected := make(map[ObjectAddress]bool)
	for address, descriptor := range base.state.descriptors {
		if namespaces[descriptor.Namespace()] {
			selected[address] = true
		}
		if extension, ok := descriptor.(*extensionDescriptor); ok && namespaces[extension.targetNamespace] {
			selected[address] = true
			for _, member := range extension.members {
				d := base.state.descriptors[member]
				if d == nil || d.Namespace() != extension.targetNamespace || extensionMembership(d) != address {
					return nil, fmt.Errorf("stuntgres: invalid namespace extension membership")
				}
				selected[member] = true
			}
		}
	}
	children := make([]ObjectAddress, 0, len(selected))
	for address := range selected {
		children = append(children, address)
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].ClassID != children[j].ClassID {
			return children[i].ClassID < children[j].ClassID
		}
		return children[i].ObjectID < children[j].ObjectID
	})
	return children, nil
}

func procedureInNamespaces(catalog CatalogRead, path namespaceSelection, name string, signature procedureSignature) *procedureDescriptor {
	for i := range path.count() {
		address := catalog.state.names[NameKey{ClassID: ProcedureClassID, Namespace: path.at(i), Name: name, Signature: string(signature)}]
		if address != (ObjectAddress{}) {
			procedure, _ := catalog.state.descriptors[address].(*procedureDescriptor)
			return procedure
		}
	}
	return nil
}

func (p *unaccentProvider) validate(v CatalogRead, d *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid unaccent manifest") }
	if p != stockUnaccent || len(d.members) != 6 {
		return invalid()
	}
	classes := [6]OID{ProcedureClassID, ProcedureClassID, ProcedureClassID, ProcedureClassID, TSTemplateClassID, TSDictionaryClassID}
	for i, member := range d.members {
		if member.ClassID != classes[i] {
			return invalid()
		}
	}
	signatures := [4]procedureSignature{unaccentDictionarySignature, unaccentTextSignature, unaccentInitSignature, unaccentLexizeSignature}
	for i, signature := range signatures {
		proc, ok := v.state.descriptors[d.members[i]].(*procedureDescriptor)
		if !ok || proc.signature != signature || proc.provider != p || proc.queryCodec != nil || proc.intagg != nil || proc.uuidOssp != nil {
			return invalid()
		}
	}
	tmpl, ok := v.state.descriptors[d.members[4]].(*tsTemplateDescriptor)
	if !ok || tmpl.init != d.members[2] || tmpl.lexize != d.members[3] || tmpl.provider != p {
		return invalid()
	}
	dict, ok := v.state.descriptors[d.members[5]].(*tsDictionaryDescriptor)
	if !ok || dict.template != d.members[4] || dict.provider != p {
		return invalid()
	}
	return nil
}
