package engine

// Released facts belong to initialized catalog descriptors, separately from
// their adapter's ability to return physical fields or infer query nullability.
type catalogAttribute struct {
	notNull    bool
	dimensions int16
	collation  uint32
}

// Condensed projections keep their established result order. Stable attribute
// numbers instead follow REL_18_0 catalog headers, including omitted neighbors.
func catalogAttributeNumber(oid OID, name string, ordinal int32) int32 {
	switch oid {
	case 1255:
		switch name {
		case "pronargs":
			return 17
		case "prorettype":
			return 19
		case "provolatile":
			return 15
		case "proisstrict":
			return 13
		case "proparallel":
			return 16
		}
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
		}
	}
	return ordinal
}

func initializeCatalogAttributes(d *relationDescriptor) {
	d.catalogAttributes = make([]catalogAttribute, len(d.columns))
	view := d.virtualView || representedSummaryViewDefinition(d.address.ObjectID) != ""
	for i, column := range d.columns {
		attribute := catalogAttributeNumber(d.address.ObjectID, column.Name, int32(i+1))
		d.attributes[i], d.columns[i].OriginAttribute = attribute, int16(attribute)
		if attribute > d.attributeHighWater {
			d.attributeHighWater = attribute
		}
		fact := catalogAttribute{notNull: !view, collation: column.Usage.Collation.ID}
		if !view {
			if column.Usage.Type.IsArray() {
				fact.dimensions = 1
			}
			// Bootstrap textual catalog fields use C, not the database default.
			if fact.collation != 0 || column.Usage.Type.ID() == 194 {
				fact.collation = 950
			}
			switch d.address.ObjectID {
			case pgTypeOID:
				fact.notNull = i < 29
			case 1213:
				fact.notNull = i < 3
			case 1262:
				fact.notNull = i < 14
			case 2964:
				fact.notNull = i < 2
			case ParameterClassID:
				fact.notNull = i < 2
			case 3456:
				fact.notNull = i < 7
			case pgLanguageOID:
				fact.notNull = i < 8
			case pgAggregateOID:
				fact.notNull = i < 20
			case 3079:
				fact.notNull = i < 6
			case 3600:
				fact.notNull = i < 5
			case 1259:
				fact.notNull = i < 31
			case 2606:
				fact.notNull = i < 20
			case 1249:
				fact.notNull = i < 20
			}
		}
		d.catalogAttributes[i] = fact
	}
}
