package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Condensed projections keep their established result order. Stable attribute
// numbers instead follow REL_18_0 catalog headers, including omitted neighbors.
func AttributeNumber(oid ids.OID, name string, ordinal int32) int32 {
	switch oid {
	case 2606:
		switch name {
		case "contype":
			return 4
		case "conrelid":
			return 9
		case "conbin":
			return 28
		case "conindid":
			return 11
		case "conparentid":
			return 12
		case "conislocal":
			return 17
		case "coninhcount":
			return 18
		}
	case 2610:
		switch name {
		case "indisunique":
			return 5
		case "indisprimary":
			return 7
		case "indisvalid":
			return 11
		case "indisready":
			return 13
		case "indislive":
			return 14
		case "indnullsnotdistinct":
			return 6
		case "indkey":
			return 16
		case "indcollation":
			return 17
		case "indclass":
			return 18
		case "indoption":
			return 19
		}
	case 2620:
		switch name {
		case "tgname":
			return 4
		case "tgisinternal":
			return 8
		case "tgconstraint":
			return 11
		}
	}
	return ordinal
}

func InitializeAttributes(d *catalog.Relation) {
	d.ColumnMeta.CatalogAttributes = make([]catalog.CatalogAttribute, len(d.Columns))
	view := d.VirtualView || SummaryViewDefinition(d.Address().ObjectID) != ""
	d.CatalogView = view
	for i, column := range d.Columns {
		attribute := AttributeNumber(d.Address().ObjectID, column.Name, int32(i+1))
		d.Attributes[i], d.Columns[i].OriginAttribute = attribute, int16(attribute)
		if attribute > d.ColumnMeta.HighWater {
			d.ColumnMeta.HighWater = attribute
		}
		fact := catalog.CatalogAttribute{NotNull: !view, Collation: column.Usage.Collation.ID}
		if !view {
			if column.Usage.Type.IsArray() {
				fact.Dimensions = 1
			}
			// Bootstrap textual catalog fields use C, not the database default.
			if fact.Collation != 0 || column.Usage.Type.ID() == 194 {
				fact.Collation = 950
			}
			switch d.Address().ObjectID {
			case InfoSQLFeaturesOID, InfoSQLSizingOID, InfoSQLImplementationInfoOID, InfoSQLPartsOID:
				fact.NotNull = !column.Nullable
			case ids.EventTriggerClassID, StatisticExtOID, StatisticExtDataOID, SubscriptionOID, SubscriptionRelOID, ids.PolicyClassID:
				fact.NotNull = !column.Nullable
			case ids.ProcedureClassID:
				fact.NotNull = !column.Nullable
			case TypeOID:
				fact.NotNull = i < 29
			case 1213:
				fact.NotNull = i < 3
			case ids.RoleClassID:
				fact.NotNull = i < 10
			case 1262:
				fact.NotNull = i < 14
			case 2964:
				fact.NotNull = i < 2
			case ids.ParameterClassID:
				fact.NotNull = i < 2
			case ids.ForeignMappingClassID:
				fact.NotNull = i < 3
			case 3456:
				fact.NotNull = i < 7
			case LanguageOID:
				fact.NotNull = i < 8
			case AggregateOID:
				fact.NotNull = i < 20
			case 3079:
				fact.NotNull = i < 6
			case 3600:
				fact.NotNull = i < 5
			case 1259:
				fact.NotNull = i < 31
			case 2606:
				fact.NotNull = i < 20
			case 1249:
				fact.NotNull = i < 20
			}
		}
		d.ColumnMeta.CatalogAttributes[i] = fact
	}
}
