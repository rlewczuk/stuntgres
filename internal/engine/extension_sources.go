package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func captureExtensionCatalog(ctx context.Context, view catalog.Read, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.Address().ObjectID == ids.ExtensionClassID {
		ids := [8]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Bool, sqltype.Text, 1028, 1009}
		if len(source.Columns) != len(ids) {
			return nil, fmt.Errorf("stuntgres: invalid extension source shape")
		}
		for i, column := range source.Columns {
			owned, ok := view.LookupType(ids[i])
			if !ok || owned != column.Usage.Type {
				return nil, fmt.Errorf("stuntgres: foreign extension source type")
			}
		}
	}
	descriptors := make([]catalog.Descriptor, 0)
	for _, d := range view.Descriptors() {
		if d.Address().ClassID == source.Address().ObjectID {
			descriptors = append(descriptors, d)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(source.Columns))
		var valueErr error
		integer := func(i int, n int64) {
			var v sqltype.Value
			var err error
			if source.Columns[i].Usage.Type.ID() == sqltype.Oid {
				v, err = sqltype.OIDValue(source.Columns[i].Usage.Type, uint32(n))
			} else {
				v, err = sqltype.Integer(source.Columns[i].Usage.Type, n)
			}
			if err != nil {
				valueErr = err
			}
			row[i] = v
		}
		text := func(i int, s string) { row[i] = sqltype.String(source.Columns[i].Usage.Type, s) }
		boolean := func(i int, b bool) { row[i] = sqltype.Boolean(source.Columns[i].Usage.Type, b) }
		switch d := descriptor.(type) {
		case *catalog.Extension:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Owner.ID()))
			integer(3, int64(d.TargetNamespace.ObjectID))
			boolean(4, true)
			text(5, d.Version)
			row[6] = sqltype.Null(source.Columns[6].Usage.Type)
			row[7] = sqltype.Null(source.Columns[7].Usage.Type)
		case *catalog.Procedure:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			integer(3, int64(d.Owner.ID()))
			integer(4, procedureArity(d))
			integer(5, int64(d.Result))
			vol := "v"
			if d.Volatility == expr.Stable {
				vol = "s"
			} else if d.Volatility == expr.Immutable {
				vol = "i"
			}
			text(6, vol)
			boolean(7, d.Strict)
			parallel := "s"
			if d.Foreign != nil {
				parallel = "u"
			} else if d.Builtin != nil {
				switch d.Builtin.Parallel {
				case 'r':
					parallel = "r"
				case 'u':
					parallel = "u"
				}
			}
			text(8, parallel)
		case *catalog.TSTemplate:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
		case *catalog.TSDictionary:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			integer(3, int64(d.Owner.ID()))
			integer(4, int64(d.Template.ObjectID))
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
