package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
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
	address         ids.ObjectAddress
	revision        ids.DefinitionRevision
	name, Version   string
	TargetNamespace ids.ObjectAddress
	Owner           authorization.PrincipalRef
	Members         []ids.ObjectAddress // immutable after detached staging
}

func (d *extensionDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *extensionDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *extensionDescriptor) Namespace() ids.ObjectAddress     { return ids.ObjectAddress{} }
func (d *extensionDescriptor) Name() string                     { return d.name }
func (*extensionDescriptor) Kind() ids.ObjectKind               { return ids.ExtensionObject }

type procedureDescriptor struct {
	address, namespace, Extension ids.ObjectAddress
	revision                      ids.DefinitionRevision
	name                          string
	Signature                     procedureSignature
	RoutineKind                   routineKind
	Builtin                       *builtinRoutineDeclaration
	Body                          []routineBodyEdge
	Owner                         authorization.PrincipalRef
	Result                        ids.OID
	Volatility                    expr.Volatility
	Strict                        bool
	// Implementation is the extension implementation that installed this
	// routine; nil for builtin, conversion and foreign routines.
	Implementation extensionImplementation
	Aggregate      *ownedAggregate
	Conversion     sqltype.EncodingConversionHandler
	ACL            authorization.ACLValue
	Dictionary     ids.ObjectAddress
	Foreign        *foreignProcedure
}

func (d *procedureDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *procedureDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *procedureDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *procedureDescriptor) Name() string                     { return d.name }
func (*procedureDescriptor) Kind() ids.ObjectKind               { return ids.ProcedureObject }

type tsTemplateDescriptor struct {
	address, namespace, Extension, Init, Lexize ids.ObjectAddress
	revision                                    ids.DefinitionRevision
	name                                        string
	Implementation                              extensionImplementation
}

func (d *tsTemplateDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *tsTemplateDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *tsTemplateDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *tsTemplateDescriptor) Name() string                     { return d.name }
func (*tsTemplateDescriptor) Kind() ids.ObjectKind               { return ids.TSTemplateObject }

type tsDictionaryDescriptor struct {
	address, namespace, Extension, Template ids.ObjectAddress
	revision                                ids.DefinitionRevision
	name                                    string
	Owner                                   authorization.PrincipalRef
	Implementation                          extensionImplementation
}

func (d *tsDictionaryDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *tsDictionaryDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *tsDictionaryDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *tsDictionaryDescriptor) Name() string                     { return d.name }
func (*tsDictionaryDescriptor) Kind() ids.ObjectKind               { return ids.TSDictionaryObject }

func descriptorNameKey(d Descriptor) ids.NameKey {
	key := ids.NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
	if p, ok := d.(*procedureDescriptor); ok {
		key.Signature = string(p.Signature)
	}
	if operator, ok := d.(*operatorDescriptor); ok {
		key.Signature = operator.signature()
	}
	return key
}

type extensionCatalogProvider struct{ class ids.OID }

func (p extensionCatalogProvider) ClassID() ids.OID { return p.class }
func (p extensionCatalogProvider) Validate(v CatalogRead, d Descriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extension provider descriptor") }
	a := d.Address()
	if a.ClassID != p.class || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || v.NameAddress(descriptorNameKey(d)) != a {
		return invalid()
	}
	namespace := d.Namespace()
	if namespace != (ids.ObjectAddress{}) {
		if _, ok := v.Namespace(namespace); !ok {
			return invalid()
		}
	}
	var ext ids.ObjectAddress
	switch d := d.(type) {
	case *extensionDescriptor:
		implementation, registered := v.owner.extensions[d.name]
		if !registered || d.Version != implementation.Control().version || d.Namespace() != (ids.ObjectAddress{}) {
			return invalid()
		}
		if _, ok := v.Namespace(d.TargetNamespace); !ok {
			return invalid()
		}
		seen := map[ids.ObjectAddress]bool{}
		for _, a := range d.Members {
			if seen[a] {
				return invalid()
			}
			seen[a] = true
			member := v.Get(a)
			if member == nil || member.Namespace() != d.TargetNamespace || extensionMembership(member) != d.address {
				return invalid()
			}
		}
		if err := implementation.ValidateExtension(v, d); err != nil {
			return err
		}
	case *procedureDescriptor:
		if d.uuidOsspProvider() != nil && (d.Aggregate != nil || d.Foreign != nil || d.Builtin != nil) {
			return invalid()
		}
		if d.Aggregate != nil && d.intaggProvider() != stockIntagg {
			return invalid()
		}
		if d.Foreign != nil {
			p := d.Foreign
			if p.name.Name != d.name || d.Extension != (ids.ObjectAddress{}) || d.Implementation != nil || d.Conversion.Name() != "" || d.Builtin != nil {
				return invalid()
			}
			if p.wrapper != nil {
				if p.validator != nil || d.Signature != foreignHandlerSignature || d.Result != 3115 {
					return invalid()
				}
			} else if p.validator == nil || d.Signature != foreignValidatorSignature || d.Result != 2278 {
				return invalid()
			}
			break
		}
		if d.Builtin != nil {
			declared := d.Builtin
			if builtinRoutineByOID[d.address.ObjectID] != declared || d.namespace != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || d.name != declared.identity.name || d.Signature != declared.identity.signature || d.RoutineKind != declared.kind || d.Result != declared.result || d.Strict != declared.strict || d.Volatility != declared.volatility || d.Implementation != nil || d.Conversion.Name() != "" || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.Foreign != nil {
				return invalid()
			}
			break
		}
		if d.intaggProvider() != nil {
			if err := d.intaggProvider().validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.RoutineKind != functionRoutine {
			return invalid()
		}
		if d.uuidOsspProvider() != nil {
			if d.Conversion.Name() != "" || d.Dictionary != (ids.ObjectAddress{}) {
				return invalid()
			}
			if err := d.uuidOsspProvider().validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.fuzzystrmatchProvider() != nil {
			if err := d.fuzzystrmatchProvider().validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.pgcryptoProvider() != nil {
			if err := d.pgcryptoProvider().validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.intarrayProvider() != nil {
			if err := d.intarrayProvider().validateProcedure(v, d); err != nil {
				return err
			}
			ext = d.Extension
			break
		}
		if d.Conversion.Name() != "" {
			registered, ok := sqltype.RegisteredEncodingConversion(d.name)
			if !ok || d.Conversion != registered || d.unaccentProvider() != nil || d.Signature != conversionSignature || d.Result != 23 || d.Volatility != expr.Immutable || !d.Strict || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) || d.namespace != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || conversionRoutineName(d.address.ObjectID) != d.name {
				return invalid()
			}
			break
		}
		if d.unaccentProvider() != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		switch d.Signature {
		case unaccentTextSignature, unaccentDictionarySignature:
			if d.name != "unaccent" || d.Result != 25 || d.Volatility != expr.Stable || !d.Strict {
				return invalid()
			}
			if ext != (ids.ObjectAddress{}) {
				dict, ok := v.TSDictionary(d.Dictionary)
				if !ok || dict.namespace != d.namespace || dict.Extension != ext {
					return invalid()
				}
			}
		case unaccentInitSignature:
			if d.name != "unaccent_init" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
				return invalid()
			}
		case unaccentLexizeSignature:
			if d.name != "unaccent_lexize" || d.Result != 2281 || d.Volatility != expr.Volatile || d.Strict {
				return invalid()
			}
		default:
			return invalid()
		}
	case *tsTemplateDescriptor:
		if d.name != "unaccent" || d.unaccentProvider() != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		for i, a := range [2]ids.ObjectAddress{d.Init, d.Lexize} {
			proc, ok := v.Procedure(a)
			if !ok || ext != (ids.ObjectAddress{}) && proc.namespace != d.namespace || proc.Extension != ext || proc.Signature != [2]procedureSignature{unaccentInitSignature, unaccentLexizeSignature}[i] {
				return invalid()
			}
		}
	case *tsDictionaryDescriptor:
		if d.name != "unaccent" || d.unaccentProvider() != stockUnaccent {
			return invalid()
		}
		ext = d.Extension
		tmpl, ok := v.TSTemplate(d.Template)
		if !ok || ext != (ids.ObjectAddress{}) && tmpl.namespace != d.namespace || tmpl.Extension != ext {
			return invalid()
		}
	default:
		return invalid()
	}
	if ext != (ids.ObjectAddress{}) {
		parent, ok := v.Extension(ext)
		if !ok || parent.TargetNamespace != namespace {
			return invalid()
		}
		found := false
		for _, member := range parent.Members {
			found = found || member == a
		}
		if !found {
			return invalid()
		}
	}
	return nil
}
func extensionMembership(d Descriptor) ids.ObjectAddress {
	switch d := d.(type) {
	case *typeDescriptor:
		return d.Extension
	case *procedureDescriptor:
		return d.Extension
	case *tsTemplateDescriptor:
		return d.Extension
	case *tsDictionaryDescriptor:
		return d.Extension
	}
	return ids.ObjectAddress{}
}
func procedureArity(p *procedureDescriptor) int64 {
	if p.Signature == "" {
		return 0
	}
	return int64(strings.Count(string(p.Signature), ",") + 1)
}

// Namespace retirement owns complete installations by their real target, not
// by the extension's database-wide name or generic Namespace field.
func namespaceChildren(base CatalogRead, namespaces map[ids.ObjectAddress]bool) ([]ids.ObjectAddress, error) {
	selected := make(map[ids.ObjectAddress]bool)
	for address, descriptor := range base.Descriptors() {
		if namespaces[descriptor.Namespace()] {
			selected[address] = true
		}
		if extension, ok := descriptor.(*extensionDescriptor); ok && namespaces[extension.TargetNamespace] {
			selected[address] = true
			for _, member := range extension.Members {
				d := base.Get(member)
				if d == nil || d.Namespace() != extension.TargetNamespace || extensionMembership(d) != address {
					return nil, fmt.Errorf("stuntgres: invalid namespace extension membership")
				}
				selected[member] = true
			}
		}
	}
	children := make([]ids.ObjectAddress, 0, len(selected))
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
		address := catalog.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: path.at(i), Name: name, Signature: string(signature)})
		if address != (ids.ObjectAddress{}) {
			procedure, _ := catalog.Procedure(address)
			return procedure
		}
	}
	return nil
}

func (p *unaccentProvider) ValidateExtension(v CatalogRead, d *extensionDescriptor) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid unaccent manifest") }
	if p != stockUnaccent || len(d.Members) != 6 {
		return invalid()
	}
	classes := [6]ids.OID{ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.ProcedureClassID, ids.TSTemplateClassID, ids.TSDictionaryClassID}
	for i, member := range d.Members {
		if member.ClassID != classes[i] {
			return invalid()
		}
	}
	signatures := [4]procedureSignature{unaccentDictionarySignature, unaccentTextSignature, unaccentInitSignature, unaccentLexizeSignature}
	for i, signature := range signatures {
		proc, ok := v.Procedure(d.Members[i])
		if !ok || proc.Signature != signature || proc.Implementation != p {
			return invalid()
		}
	}
	tmpl, ok := v.TSTemplate(d.Members[4])
	if !ok || tmpl.Init != d.Members[2] || tmpl.Lexize != d.Members[3] || tmpl.unaccentProvider() != p {
		return invalid()
	}
	dict, ok := v.TSDictionary(d.Members[5])
	if !ok || dict.Template != d.Members[4] || dict.unaccentProvider() != p {
		return invalid()
	}
	return nil
}
