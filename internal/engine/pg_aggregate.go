package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgAggregateOID ids.OID = 2600

// An aggregate definition belongs to its implemented routine. Optional seeds
// distinguish SQL NULL from an empty string without allocating seed pointers.
type aggregateDefinition struct {
	Function                       ids.OID
	Kind                           string
	DirectArgs                     int16
	Functions                      [8]ids.OID // transition, final, combine, serialize, deserialize, moving, inverse, moving-final
	FinalExtra, MovingFinalExtra   bool
	FinalModify, MovingFinalModify string
	SortOperator, TransitionType   ids.OID
	TransitionSpace                int32
	MovingType                     ids.OID
	MovingSpace                    int32
	Initial, MovingInitial         string
	HasInitial, HasMovingInitial   bool
}

var aggregateColumnTypes = [...]sqltype.ID{
	sqltype.Regproc, sqltype.Char, sqltype.Int2, sqltype.Regproc, sqltype.Regproc,
	sqltype.Regproc, sqltype.Regproc, sqltype.Regproc, sqltype.Regproc, sqltype.Regproc,
	sqltype.Regproc, sqltype.Bool, sqltype.Bool, sqltype.Char, sqltype.Char,
	sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Oid, sqltype.Int4, sqltype.Text, sqltype.Text,
}

func capturePGAggregate(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if catalog.Owner() == nil || catalog.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgAggregateOID}) || ref.Domain() != catalog.Owner().Domain || ref.Epoch != catalog.Epoch() || ref.Address != source.Address() || catalog.Get(source.Address()) != source || len(source.Columns) != len(aggregateColumnTypes) {
		return nil, fmt.Errorf("stuntgres: foreign aggregate source authority")
	}
	for i, id := range aggregateColumnTypes {
		typ, ok := catalog.LookupType(id)
		if !ok || typ != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign aggregate source type")
		}
	}
	count := len(builtinAggregateDefinitions)
	for _, d := range catalog.Descriptors() {
		if p, ok := d.(*procedureDescriptor); ok && p.Aggregate != nil {
			count++
		}
	}
	rows := make([][]sqltype.Value, count)
	cells := make([]sqltype.Value, count*len(aggregateColumnTypes))
	index := 0
	put := func(d *aggregateDefinition) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := cells[index*22 : (index+1)*22]
		for i := range row {
			typ := source.Columns[i].Usage.Type
			var err error
			switch {
			case i == 0:
				row[i], err = sqltype.RegprocValue(typ, uint32(d.Function))
			case i == 1:
				row[i] = sqltype.String(typ, d.Kind)
			case i == 2:
				row[i], err = sqltype.Integer(typ, int64(d.DirectArgs))
			case i >= 3 && i <= 10:
				row[i], err = sqltype.RegprocValue(typ, uint32(d.Functions[i-3]))
			case i == 11:
				row[i] = sqltype.Boolean(typ, d.FinalExtra)
			case i == 12:
				row[i] = sqltype.Boolean(typ, d.MovingFinalExtra)
			case i == 13:
				row[i] = sqltype.String(typ, d.FinalModify)
			case i == 14:
				row[i] = sqltype.String(typ, d.MovingFinalModify)
			case i == 15:
				row[i], err = sqltype.OIDValue(typ, uint32(d.SortOperator))
			case i == 16:
				row[i], err = sqltype.OIDValue(typ, uint32(d.TransitionType))
			case i == 17:
				row[i], err = sqltype.Integer(typ, int64(d.TransitionSpace))
			case i == 18:
				row[i], err = sqltype.OIDValue(typ, uint32(d.MovingType))
			case i == 19:
				row[i], err = sqltype.Integer(typ, int64(d.MovingSpace))
			case i == 20:
				row[i] = sqltype.Null(typ)
				if d.HasInitial {
					row[i] = sqltype.String(typ, d.Initial)
				}
			case i == 21:
				row[i] = sqltype.Null(typ)
				if d.HasMovingInitial {
					row[i] = sqltype.String(typ, d.MovingInitial)
				}
			}
			if err != nil {
				return err
			}
		}
		rows[index] = row
		index++
		return nil
	}
	for i := range builtinAggregateDefinitions {
		if err := put(&builtinAggregateDefinitions[i]); err != nil {
			return nil, err
		}
	}
	for _, d := range catalog.Descriptors() {
		if p, ok := d.(*procedureDescriptor); ok && p.Aggregate != nil {
			if err := put(&p.Aggregate.Definition); err != nil {
				return nil, err
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
