package catalog

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
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
	TargetNamespace     ids.ObjectAddress
	Owner               authorization.PrincipalRef
	Members             []ids.ObjectAddress // immutable after detached staging
}

func (d *Extension) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Extension) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Extension) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Extension) Name() string { return d.ObjectName }

func (*Extension) Kind() ids.ObjectKind { return ids.ExtensionObject }

// InstalledRoutineDeclaration is immutable declaration metadata, not execution
// authority. Fixed-size argument names keep descriptor copies self-contained.
type InstalledRoutineDeclaration struct {
	ArgumentNames [4]string
	Parallel      byte
	ReturnsSet    bool
	Rows          float32
	Defaults      uint8
}

type Procedure struct {
	ObjectAddress, ObjectNamespace, Extension ids.ObjectAddress
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
	RangeConstructor                          *RangeConstructor
	// DeclarationDependencies order replay only; PL source is not an eager SQL
	// body dependency manifest and these addresses do not create pg_depend edges.
	DeclarationDependencies []ids.ObjectAddress
	SQLBody                 RoutineProgram
	SQLDefaults             RoutineDefaults
	Body                    []RoutineBodyEdge
	Owner                   authorization.PrincipalRef
	Result                  ids.OID
	Volatility              expr.Volatility
	Strict                  bool
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
}

func (d *Procedure) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Procedure) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Procedure) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *Procedure) Name() string { return d.ObjectName }

func (*Procedure) Kind() ids.ObjectKind { return ids.ProcedureObject }

type TSTemplate struct {
	ObjectAddress, ObjectNamespace, Extension, Init, Lexize ids.ObjectAddress
	ObjectRevision                                          ids.DefinitionRevision
	ObjectName                                              string
	Implementation                                          ExtensionImplementation
}

func (d *TSTemplate) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *TSTemplate) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *TSTemplate) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *TSTemplate) Name() string { return d.ObjectName }

func (*TSTemplate) Kind() ids.ObjectKind { return ids.TSTemplateObject }

type TSDictionary struct {
	ObjectAddress, ObjectNamespace, Extension, Template ids.ObjectAddress
	ObjectRevision                                      ids.DefinitionRevision
	ObjectName                                          string
	Owner                                               authorization.PrincipalRef
	Implementation                                      ExtensionImplementation
}

func (d *TSDictionary) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *TSDictionary) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *TSDictionary) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *TSDictionary) Name() string { return d.ObjectName }

func (*TSDictionary) Kind() ids.ObjectKind { return ids.TSDictionaryObject }

func DescriptorNameKey(d Descriptor) ids.NameKey {
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
	case *TSTemplate:
		return d.Extension
	case *TSDictionary:
		return d.Extension
	case *OperatorClass:
		return d.Extension
	case *OperatorFamily:
		return d.Extension
	}
	return ids.ObjectAddress{}
}

// ValidateExtensionMembership checks the reciprocal direct-member edge.
func ValidateExtensionMembership(view Read, d Descriptor) error {
	address := ExtensionMembership(d)
	if address == (ids.ObjectAddress{}) {
		return nil
	}
	ext, ok := view.Extension(address)
	if !ok || ext.TargetNamespace != d.Namespace() {
		return fmt.Errorf("stuntgres: invalid extension member placement")
	}
	for _, member := range ext.Members {
		if member == d.Address() {
			return nil
		}
	}
	return fmt.Errorf("stuntgres: missing reciprocal extension membership")
}
