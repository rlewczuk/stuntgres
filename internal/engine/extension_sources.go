package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func captureExtensionCatalog(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.address.ObjectID == ids.ExtensionClassID {
		ids := [8]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Bool, sqltype.Text, 1028, 1009}
		if len(source.columns) != len(ids) {
			return nil, fmt.Errorf("stuntgres: invalid extension source shape")
		}
		for i, column := range source.columns {
			owned, ok := catalog.LookupType(ids[i])
			if !ok || owned != column.Usage.Type {
				return nil, fmt.Errorf("stuntgres: foreign extension source type")
			}
		}
	}
	descriptors := make([]Descriptor, 0)
	for _, d := range catalog.Descriptors() {
		if d.Address().ClassID == source.address.ObjectID {
			descriptors = append(descriptors, d)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(source.columns))
		var valueErr error
		integer := func(i int, n int64) {
			var v sqltype.Value
			var err error
			if source.columns[i].Usage.Type.ID() == sqltype.Oid {
				v, err = sqltype.OIDValue(source.columns[i].Usage.Type, uint32(n))
			} else {
				v, err = sqltype.Integer(source.columns[i].Usage.Type, n)
			}
			if err != nil {
				valueErr = err
			}
			row[i] = v
		}
		text := func(i int, s string) { row[i] = sqltype.String(source.columns[i].Usage.Type, s) }
		boolean := func(i int, b bool) { row[i] = sqltype.Boolean(source.columns[i].Usage.Type, b) }
		switch d := descriptor.(type) {
		case *extensionDescriptor:
			integer(0, int64(d.address.ObjectID))
			text(1, d.name)
			integer(2, int64(d.owner.ID()))
			integer(3, int64(d.targetNamespace.ObjectID))
			boolean(4, true)
			text(5, d.version)
			row[6] = sqltype.Null(source.columns[6].Usage.Type)
			row[7] = sqltype.Null(source.columns[7].Usage.Type)
		case *procedureDescriptor:
			integer(0, int64(d.address.ObjectID))
			text(1, d.name)
			integer(2, int64(d.namespace.ObjectID))
			integer(3, int64(d.owner.ID()))
			integer(4, procedureArity(d))
			integer(5, int64(d.result))
			vol := "v"
			if d.volatility == Stable {
				vol = "s"
			} else if d.volatility == Immutable {
				vol = "i"
			}
			text(6, vol)
			boolean(7, d.strict)
			parallel := "s"
			if d.foreign != nil {
				parallel = "u"
			} else if d.builtin != nil {
				switch d.builtin.parallel {
				case 'r':
					parallel = "r"
				case 'u':
					parallel = "u"
				}
			}
			text(8, parallel)
		case *tsTemplateDescriptor:
			integer(0, int64(d.address.ObjectID))
			text(1, d.name)
			integer(2, int64(d.namespace.ObjectID))
		case *tsDictionaryDescriptor:
			integer(0, int64(d.address.ObjectID))
			text(1, d.name)
			integer(2, int64(d.namespace.ObjectID))
			integer(3, int64(d.owner.ID()))
			integer(4, int64(d.template.ObjectID))
			text(5, "rules = 'unaccent'")
		default:
			continue
		}
		if valueErr != nil {
			return nil, valueErr
		}
		rows = append(rows, row)
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
