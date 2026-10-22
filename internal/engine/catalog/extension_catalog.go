package catalog

import (
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

type Procedure struct {
	ObjectAddress, ObjectNamespace, Extension ids.ObjectAddress
	ObjectRevision                            ids.DefinitionRevision
	ObjectName                                string
	Signature                                 ProcedureSignature
	RoutineKind                               RoutineKind
	Builtin                                   *BuiltinRoutineDeclaration
	SQL                                       *proc.Definition
	SQLBody                                   RoutineProgram
	Body                                      []RoutineBodyEdge
	Owner                                     authorization.PrincipalRef
	Result                                    ids.OID
	Volatility                                expr.Volatility
	Strict                                    bool
	// Implementation is the extension implementation that installed this
	// routine; nil for builtin, conversion and foreign routines.
	Implementation ExtensionImplementation
	Aggregate      *OwnedAggregate
	Conversion     sqltype.EncodingConversionHandler
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

// ExtensionMembership returns the extension that d claims to belong to, or
// the zero address. Only types, procedures and text search templates and
// dictionaries carry a membership.
func ExtensionMembership(d Descriptor) ids.ObjectAddress {
	switch d := d.(type) {
	case *Type:
		return d.Extension
	case *Procedure:
		return d.Extension
	case *TSTemplate:
		return d.Extension
	case *TSDictionary:
		return d.Extension
	}
	return ids.ObjectAddress{}
}
