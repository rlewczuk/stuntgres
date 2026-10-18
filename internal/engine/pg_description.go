package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Projection coverage is deliberately broader than legacy obj_description.
// Inquiry dispatch and COMMENT target support remain their owning authorities.
func catalogDescription(d Descriptor, sub int32) string {
	if sub == 0 {
		switch d := d.(type) {
		case *collationDescriptor:
			return collationDescription(d.OID)
		case *conversionDescriptor:
			return conversionDescription(d.OID)
		}
	}
	return descriptorComment(d, sub)
}

func collationDescription(oid ids.OID) string {
	switch oid {
	case 100:
		return "database's default collation"
	case 950:
		return "standard C collation"
	case 951:
		return "standard POSIX collation"
	case 962:
		return "sorts by Unicode code point, C character semantics"
	case 963:
		return "sorts using the Unicode Collation Algorithm with default settings"
	case 811:
		return "sorts by Unicode code point; Unicode and POSIX character semantics"
	case 6411:
		return "sorts by Unicode code point; Unicode character semantics"
	}
	return ""
}

func conversionDescription(oid ids.OID) string {
	if oid < 4402 || oid > 4531 || oid == 4450 || oid == 4451 {
		return ""
	}
	index := int(oid - 4402)
	if oid >= 4452 {
		index -= 2
	}
	return builtinConversionDescriptions[index]
}

func capturePGDescription(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, catalog.DescriptorCount())
	appendDescription := func(address ids.ObjectAddress, sub int32, text string) error {
		if text == "" {
			return nil
		}
		object, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return err
		}
		class, err := sqltype.OIDValue(source.Columns[1].Usage.Type, uint32(address.ClassID))
		if err != nil {
			return err
		}
		attribute, err := sqltype.Integer(source.Columns[2].Usage.Type, int64(sub))
		if err != nil {
			return err
		}
		rows = append(rows, []sqltype.Value{object, class, attribute, sqltype.String(source.Columns[3].Usage.Type, text)})
		return nil
	}
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := appendDescription(descriptor.Address(), 0, catalogDescription(descriptor, 0)); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			for _, attribute := range relation.Attributes {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if err := appendDescription(relation.address, attribute, descriptorComment(relation, attribute)); err != nil {
					return nil, err
				}
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
