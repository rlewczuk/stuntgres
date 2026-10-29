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
	RoleClassID                  OID = 1260
	RelationClassID              OID = 1259
	TypeClassID                  OID = 1247
	CollationClassID             OID = 3456
	ConversionClassID            OID = 2607
	CastClassID                  OID = 2605
	PolicyClassID                OID = 3256
	TriggerClassID               OID = 2620
	EventTriggerClassID          OID = 3466
	SubscriptionClassID          OID = 6100
	ParameterClassID             OID = 6243
	TablespaceClassID            OID = 1213
	LanguageClassID              OID = 2612
	ProcedureClassID             OID = 1255
	TransformClassID             OID = 3576
	ExtensionClassID             OID = 3079
	TSTemplateClassID            OID = 3764
	TSDictionaryClassID          OID = 3600
	TSConfigurationClassID       OID = 3602
	TSParserClassID              OID = 3601
	DatabaseClassID              OID = 1262
	ForeignWrapperClassID        OID = 2328
	ForeignServerClassID         OID = 1417
	ForeignMappingClassID        OID = 1418
	ForeignTableClassID          OID = 3118
	OperatorClassID              OID = 2617
	OperatorClassCatalogID       OID = 2616
	OperatorFamilyClassID        OID = 2753
	AccessMethodOperatorClassID  OID = 2602
	AccessMethodProcedureClassID OID = 2603
	AccessMethodClassID          OID = 2601
	LargeObjectClassID           OID = 2995
	StatisticExtClassID          OID = 3381
	PGCatalogNamespaceOID        OID = 11
	PGToastNamespaceOID          OID = 99
	PublicNamespaceOID           OID = 2200
)

// IsForeignCatalogClass reports whether class is an SQL/MED catalog class:
// foreign-data wrappers, foreign servers and user mappings.
func IsForeignCatalogClass(class OID) bool {
	return class == ForeignWrapperClassID || class == ForeignServerClassID || class == ForeignMappingClassID
}

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
	OperatorClassObject
	AccessMethodObject
	EventTriggerObject
	CastObject
	LanguageObject
	TSConfigurationObject
	StatisticsObject
	SubscriptionObject
	TSParserObject
	TransformObject
)
