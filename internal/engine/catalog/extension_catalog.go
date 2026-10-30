package catalog

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Signatures are declared identities, not runtime overload admission. Promoting
// regdictionary's scalar codec does not enable the two-argument unaccent overload.
type ProcedureSignature string

const (
	UnaccentTextSignature       ProcedureSignature = "25"
	UnaccentDictionarySignature ProcedureSignature = "3769,25"
	UnaccentInitSignature       ProcedureSignature = "2281"
	UnaccentLexizeSignature     ProcedureSignature = "2281,2281,2281,2281"
	ConversionSignature         ProcedureSignature = "23,23,2275,2281,23,16"
)

type Extension struct {
	ObjectAddress       ids.ObjectAddress
	ObjectRevision      ids.DefinitionRevision
	ObjectName, Version string
	Comment             string // immutable; empty means absent
	TargetNamespace     ids.ObjectAddress
	Owner               authorization.PrincipalRef
	Installation        *Installation
	Members             []ids.ObjectAddress // current SQL membership; immutable after publication
}

func (d *Extension) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Extension) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Extension) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Extension) Name() string { return d.ObjectName }

func (*Extension) Kind() ids.ObjectKind { return ids.ExtensionObject }

// InstalledRoutineDeclaration is immutable declaration metadata, not execution
// authority. Names and the immutable Outputs pointer preserve comparable identity.
type InstalledRoutineDeclaration struct {
	ArgumentNames [4]string
	Parallel      byte
	ReturnsSet    bool
	Rows          float32
	Defaults      uint8
	Outputs       *RoutineOutputs
}

type Procedure struct {
	ObjectAddress, ObjectNamespace, Extension ids.ObjectAddress
	Origin                                    *InstalledOrigin
	ObjectRevision                            ids.DefinitionRevision
	ObjectName                                string
	Signature                                 ProcedureSignature
	RoutineKind                               RoutineKind
	Builtin                                   *BuiltinRoutineDeclaration
	Installed                                 InstalledRoutineDeclaration
	SQL                                       *proc.Definition
	Language                                  ids.ObjectAddress
	Display                                   RoutineDisplay
	PLHandler                                 PLHandler
	HostHandler                               *host.Runtime
	Host                                      *HostRoutine
	HostConversion                            *HostConversion
	RangeConstructor                          *RangeConstructor
	// DeclarationDependencies order replay only; PL source is not an eager SQL
	// body dependency manifest and these addresses do not create pg_depend edges.
	DeclarationDependencies []ids.ObjectAddress
	SQLBody                 RoutineProgram
	// DeclarationBody is immutable parsed declaration syntax for builtin or
	// installed SQL-standard routines. It grants no checked execution authority.
	DeclarationBody sqltype.StructuralTree
	SQLDefaults     RoutineDefaults
	LiteralDefaults *RoutineLiteralDefaults
	InputNames      *RoutineInputNames
	Body            []RoutineBodyEdge
	Owner           authorization.PrincipalRef
	Result          ids.OID
	Volatility      expr.Volatility
	Strict          bool
	SecurityDefiner bool
	Parallel        byte
	Cost            float32
	Rows            float32
	Configuration   *RoutineConfiguration
	// Implementation is the extension implementation that installed this
	// routine; nil for builtin, conversion and foreign routines.
	Implementation ExtensionImplementation
	Aggregate      *OwnedAggregate
	Conversion     sqltype.EncodingConversionHandler
	AccessMethod   NativeAccessMethod
	BrinSupport    uint8 // Registered minmax summary operation 1–4; never a scalar ABI.
	ACL            authorization.ACLValue
	Dictionary     ids.ObjectAddress
	Foreign        *ForeignProcedure
	SnowballHelper SnowballHelper
}

func (d *Procedure) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Procedure) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Procedure) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *Procedure) Name() string { return d.ObjectName }

func (*Procedure) Kind() ids.ObjectKind { return ids.ProcedureObject }

type TSTemplate struct {
	ObjectAddress, ObjectNamespace, Extension, Init, Lexize ids.ObjectAddress
	Origin                                                  *InstalledOrigin
	ObjectRevision                                          ids.DefinitionRevision
	ObjectName                                              string
	Implementation                                          ExtensionImplementation
	snowball                                                bool
}

func (d *TSTemplate) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *TSTemplate) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *TSTemplate) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *TSTemplate) Name() string { return d.ObjectName }

func (*TSTemplate) Kind() ids.ObjectKind { return ids.TSTemplateObject }

// DictionaryOption retains decoded spelling and source order. A bare option
// differs from an explicitly supplied empty value.
type DictionaryOption struct {
	Name, Value string
	Supplied    bool
	Numeric     bool
}

// DictionaryRuntime is immutable, catalog-independent execution authority.
type DictionaryRuntime interface {
	Lexize(context.Context, string) (fts.LexicalResult, error)
	Filtering() bool
}

type PhraseDictionaryRuntime interface {
	DictionaryRuntime
	NewMatcher() expr.PhraseMatcher
}

type TSDictionary struct {
	ObjectAddress, ObjectNamespace, Extension, Template ids.ObjectAddress
	Origin                                              *InstalledOrigin
	ObjectRevision                                      ids.DefinitionRevision
	ObjectName                                          string
	Owner                                               authorization.PrincipalRef
	Implementation                                      ExtensionImplementation
	Options                                             []DictionaryOption
	Runtime                                             DictionaryRuntime
	seed                                                snowballSeed
	// Accepted lookup namespace for host-authorized unqualified logical replay.
	// This is not a SQL dependency or an execution-time binding.
	NormalizerNamespace ids.ObjectAddress
}

func (d *TSDictionary) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *TSDictionary) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *TSDictionary) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *TSDictionary) Name() string { return d.ObjectName }

func (*TSDictionary) Kind() ids.ObjectKind { return ids.TSDictionaryObject }

func DescriptorNameKey(d Descriptor) ids.NameKey {
	if m, ok := d.(*PublicationRelation); ok {
		return ids.NameKey{ClassID: ids.PublicationRelationClassID, Namespace: m.Publication, Name: fmt.Sprint(m.Relation.ObjectID)}
	}
	if m, ok := d.(*PublicationNamespace); ok {
		return ids.NameKey{ClassID: ids.PublicationNamespaceClassID, Namespace: m.Publication, Name: fmt.Sprint(m.Schema.ObjectID)}
	}
	if sub, ok := d.(*Subscription); ok {
		return SubscriptionNameKey(sub.Database, sub.Name())
	}
	if class, ok := d.(*OperatorClass); ok {
		return ClassNameKey(class.Namespace(), class.Method, class.Name())
	}
	if family, ok := d.(*OperatorFamily); ok {
		return FamilyNameKey(family.Namespace(), family.Method, family.Name())
	}
	key := ids.NameKey{ClassID: d.Address().ClassID, Namespace: d.Namespace(), Name: d.Name()}
	if p, ok := d.(*Procedure); ok {
		key.Signature = string(p.Signature)
	}
	if operator, ok := d.(*Operator); ok {
		key.Signature = operator.signature()
	}
	return key
}

// ExtensionMembership returns the extension claimed by a direct member, or
// the zero address. Family members instead follow their recorded family/class dependencies.
func ExtensionMembership(d Descriptor) ids.ObjectAddress {
	switch d := d.(type) {
	case *Type:
		return d.Extension
	case *Procedure:
		return d.Extension
	case *Operator:
		return d.Extension
	case *TSParser:
		return d.Extension
	case *TSTemplate:
		return d.Extension
	case *TSDictionary:
		return d.Extension
	case *TSConfiguration:
		return d.Extension
	case *OperatorClass:
		return d.Extension
	case *OperatorFamily:
		return d.Extension
	case *Namespace:
		return d.Extension
	case *Relation:
		return d.Extension
	case *Collation:
		return d.Extension
	case *Conversion:
		return d.Extension
	case *Language:
		return d.Extension
	case *AccessMethod:
		return d.Extension
	case *EventTrigger:
		return d.Extension
	case *Cast:
		return d.Extension
	case *ForeignObject:
		if d.Address().ClassID == ids.ForeignWrapperClassID || d.Address().ClassID == ids.ForeignServerClassID {
			return d.Extension
		}
	}
	return ids.ObjectAddress{}
}

// ValidateExtensionMembership checks the reciprocal direct-member edge.
func ValidateExtensionMembership(view Read, d Descriptor) error {
	if ext, ok := d.(*Extension); ok {
		seen := make(map[ids.ObjectAddress]bool, len(ext.Members))
		for _, address := range ext.Members {
			if seen[address] {
				return fmt.Errorf("stuntgres: duplicate extension member")
			}
			seen[address] = true
			member := view.Get(address)
			if member == nil || ExtensionMembership(member) != ext.Address() {
				return fmt.Errorf("stuntgres: invalid reciprocal extension member")
			}
		}
		for address, member := range view.Descriptors() {
			if ExtensionMembership(member) == ext.Address() && !seen[address] {
				return fmt.Errorf("stuntgres: missing reciprocal extension membership")
			}
		}
		return nil
	}
	address := ExtensionMembership(d)
	if address == (ids.ObjectAddress{}) {
		return nil
	}
	ext, ok := view.Extension(address)
	if !ok {
		return fmt.Errorf("stuntgres: missing extension member owner")
	}
	for _, member := range ext.Members {
		if member == d.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: missing reciprocal extension membership")
}
