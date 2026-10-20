package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = registeredCatalogProducer(12081)
	captureDataTypePrivileges  = registeredCatalogProducer(16201)
	captureExtensionCatalog    = registeredCatalogProducer(1255)
	capturePGAM                = registeredCatalogProducer(2601)
	capturePGAggregate         = registeredCatalogProducer(2600)
	capturePGAttribute         = registeredCatalogProducer(1249)
	capturePGCast              = registeredCatalogProducer(2605)
	capturePGCollation         = registeredCatalogProducer(3456)
	capturePGConstraint        = registeredCatalogProducer(2606)
	capturePGCursors           = registeredCatalogProducer(12077)
	capturePGDatabase          = registeredCatalogProducer(1262)
	capturePGDefaultACL        = registeredCatalogProducer(826)
	capturePGDepend            = registeredCatalogProducer(2608)
	capturePGDescription       = registeredCatalogProducer(2609)
	capturePGEnum              = registeredCatalogProducer(3501)
	capturePGGroup             = registeredCatalogProducer(12008)
	capturePGIndexes           = registeredCatalogProducer(12002)
	capturePGInherits          = registeredCatalogProducer(2611)
	capturePGLanguage          = registeredCatalogProducer(2612)
	capturePGNamespace         = registeredCatalogProducer(2615)
	capturePGOpclass           = registeredCatalogProducer(2616)
	capturePGOperator          = registeredCatalogProducer(2617)
	capturePGParameterACL      = registeredCatalogProducer(6243)
	capturePGRange             = registeredCatalogProducer(3541)
	capturePGRoles             = registeredCatalogProducer(12007)
	capturePGSequence          = registeredCatalogProducer(2224)
	capturePGSettings          = registeredCatalogProducer(16202)
	capturePGShDescription     = registeredCatalogProducer(2396)
	capturePGTablespace        = registeredCatalogProducer(1213)
	capturePGType              = registeredCatalogProducer(1247)
	capturePGUser              = registeredCatalogProducer(12014)
	capturePreparedSource      = registeredCatalogProducer(12001)
	captureTablePrivileges     = registeredCatalogProducer(16200)
	captureTableViewSummary    = registeredCatalogProducer(12000)
	captureTimezoneSource      = registeredCatalogProducer(12004)
)

func registeredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
}
