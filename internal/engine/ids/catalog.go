package ids

type OID uint32
type CatalogEpoch uint64
type DefinitionRevision uint64
type ObjectAddress struct {
	ClassID  OID
	ObjectID OID
	SubID    int32
}

// ObjectRef is an object address bound to its engine domain and catalog
// epoch. Copying a complete ref preserves provenance; reconstructing it from
// epoch and address does not.
type ObjectRef struct {
	domain  *Domain
	Epoch   CatalogEpoch
	Address ObjectAddress
}

func (r ObjectRef) Domain() *Domain { return r.domain }

type NameKey struct {
	ClassID   OID
	Namespace ObjectAddress
	Name      string
	Signature string
}

const (
	NamespaceClassID             OID = 2615
	RelationClassID              OID = 1259
	TypeClassID                  OID = 1247
	CollationClassID             OID = 3456
	ConversionClassID            OID = 2607
	PolicyClassID                OID = 3256
	ParameterClassID             OID = 6243
	TablespaceClassID            OID = 1213
	ProcedureClassID             OID = 1255
	ExtensionClassID             OID = 3079
	TSTemplateClassID            OID = 3764
	TSDictionaryClassID          OID = 3600
	DatabaseClassID              OID = 1262
	ForeignWrapperClassID        OID = 2328
	ForeignServerClassID         OID = 1417
	ForeignMappingClassID        OID = 1418
	ForeignTableClassID          OID = 3118
	OperatorClassID              OID = 2617
	OperatorFamilyClassID        OID = 2753
	AccessMethodOperatorClassID  OID = 2602
	AccessMethodProcedureClassID OID = 2603
	LargeObjectClassID           OID = 2995
	PGCatalogNamespaceOID        OID = 11
	PGToastNamespaceOID          OID = 99
	PublicNamespaceOID           OID = 2200
)

type ObjectKind uint8

const (
	NamespaceObject ObjectKind = iota + 1
	RelationObject
	TypeObject
	CollationObject
	ConversionObject
	ExtensionObject
	ProcedureObject
	TSTemplateObject
	TSDictionaryObject
	DatabaseObject
	OperatorObject
	ForeignWrapperObject
	ForeignServerObject
	ForeignMappingObject
	ParameterObject
	TablespaceObject
	LargeObjectObject
	OperatorFamilyObject
	OperatorFamilyOperatorObject
	OperatorFamilySupportObject
)
