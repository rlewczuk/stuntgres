package stats

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// VisitAttributes shares the catalog capture frontier with pg_class.
// Own uncommitted selected deltas are resolved before privilege-filtered output.
// The catalog runtime calls it under its capture lock with the engine's store.
func VisitAttributes[A CatalogAuthority](ctx context.Context, auth A, store *storage.Store, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	view := auth.Access()
	for _, raw := range view.Catalog.Descriptors() {
		relation, ok := raw.(*catalog.Relation)
		if !ok {
			continue
		}
		active, err := access.RowSecurityApplies(view.Roles, relation, view.State.CurrentUser)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		metadata := session.ResolveRelationMetadata(auth.PendingCatalog(), store.RelationMetadata, relation.Address())
		facts := session.ResolveStatistics(auth.PendingCatalog(), relation.Address(), metadata, relation, view.Catalog)
		if facts == nil {
			continue
		}
		ns, ok := view.Catalog.Namespace(relation.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: statistics namespace unavailable")
		}
		for key, fact := range facts.Columns {
			if err := ctx.Err(); err != nil {
				return err
			}
			slot := -1
			for i, attribute := range relation.Attributes {
				if attribute == key.Attribute {
					slot = i
					break
				}
			}
			if slot < 0 {
				continue
			}
			allowed, err := view.RelationPrivilege(ctx, view.State.CurrentUser.ID(), relation, key.Attribute, false, authorization.SelectPrivilege, 0)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			row := make([]sqltype.Value, len(source.Columns))
			for i, column := range source.Columns {
				row[i] = sqltype.ResultNull(column.Usage)
			}
			row[0] = sqltype.String(source.Columns[0].Usage.Type, ns.Name())
			row[1] = sqltype.String(source.Columns[1].Usage.Type, relation.Name())
			row[2] = sqltype.String(source.Columns[2].Usage.Type, relation.Columns[slot].Name)
			row[3] = sqltype.Boolean(source.Columns[3].Usage.Type, key.Inherited)
			if err := columnStatsSummary(source, fact, row, 4); err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// columnStatsSummary fills a contiguous released univariate summary layout.
// pg_stats starts it at 4 (including ranges); the expression view at 7.
// Imported immutable slots and measured populations share this projection.
func columnStatsSummary(source *catalog.Relation, fact *storage.ColumnStatistics, row []sqltype.Value, base int) error {
	if fact == nil || fact.Rows == 0 && fact.Imported == nil || fact.NullCount > fact.Rows {
		return fmt.Errorf("stuntgres: invalid statistics summary")
	}
	row[base], _ = sqltype.Float(source.Columns[base].Usage.Type, fact.NullFraction())
	row[base+1], _ = sqltype.Integer(source.Columns[base+1].Usage.Type, int64(fact.AverageWidth))
	if distinct, known := fact.EncodedDistinct(); known {
		row[base+2], _ = sqltype.Float(source.Columns[base+2].Usage.Type, distinct)
	}
	for _, item := range []struct {
		kind            int16
		values, numbers int
	}{{1, 3, 4}, {2, 5, -1}, {3, -1, 6}, {4, 7, 8}, {5, -1, 9}, {6, 10, 11}, {7, 12, -1}} {
		if base+item.values >= len(row) || base+item.numbers >= len(row) {
			continue
		}
		if slot, ok := fact.Imported.Slot(item.kind); ok {
			if item.values >= 0 && slot.Values.Type() != nil {
				row[base+item.values] = slot.Values
			}
			if item.numbers >= 0 && slot.Numbers.Type() != nil {
				if item.kind == 3 || item.kind == 6 {
					row[base+item.numbers] = slot.Numbers.ArrayElement(0)
				} else {
					row[base+item.numbers] = slot.Numbers
				}
			}
			continue
		}
		if item.kind == 1 && len(fact.MCV) != 0 {
			values := make([]sqltype.Value, len(fact.MCV))
			numbers := make([]sqltype.Value, len(fact.MCV))
			for i, mcv := range fact.MCV {
				if mcv.Count == 0 || mcv.Count > fact.Rows || mcv.Value.IsNull() || !mcv.Value.ValidResult(fact.Usage) {
					return fmt.Errorf("stuntgres: invalid statistics MCV")
				}
				values[i] = mcv.Value
				numbers[i], _ = sqltype.Float(source.Columns[base+4].Usage.Type.Element(), float64(mcv.Count)/float64(fact.Rows))
			}
			dims := []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
			var err error
			row[base+3], err = sqltype.OwnedStatisticArrayValue(source.Columns[base+3].Usage.Type, fact.Usage, dims, values)
			if err != nil {
				return err
			}
			row[base+4], err = sqltype.OwnedArrayValue(source.Columns[base+4].Usage.Type, dims, numbers)
			if err != nil {
				return err
			}
		}
		if item.kind == 2 && len(fact.Histogram) != 0 {
			value, err := sqltype.StatisticArrayValue(source.Columns[base+5].Usage.Type, fact.Usage, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(fact.Histogram))}}, fact.Histogram)
			if err != nil {
				return err
			}
			row[base+5] = value
		}
	}
	return nil
}
