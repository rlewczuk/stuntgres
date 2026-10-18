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
	function                       ids.OID
	kind                           string
	directArgs                     int16
	functions                      [8]ids.OID // transition, final, combine, serialize, deserialize, moving, inverse, moving-final
	finalExtra, movingFinalExtra   bool
	finalModify, movingFinalModify string
	sortOperator, transitionType   ids.OID
	transitionSpace                int32
	movingType                     ids.OID
	movingSpace                    int32
	initial, movingInitial         string
	hasInitial, hasMovingInitial   bool
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
	if catalog.owner == nil || catalog.state == nil || source == nil || source.address != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgAggregateOID}) || ref.Domain() != catalog.owner.catalog.domain || ref.Epoch != catalog.Epoch() || ref.Address != source.address || catalog.state.descriptors[source.address] != source || len(source.columns) != len(aggregateColumnTypes) {
		return nil, fmt.Errorf("stuntgres: foreign aggregate source authority")
	}
	for i, id := range aggregateColumnTypes {
		typ, ok := catalog.LookupType(id)
		if !ok || typ != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign aggregate source type")
		}
	}
	count := len(builtinAggregateDefinitions)
	for _, d := range catalog.state.descriptors {
		if p, ok := d.(*procedureDescriptor); ok && p.aggregate != nil {
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
			typ := source.columns[i].Usage.Type
			var err error
			switch {
			case i == 0:
				row[i], err = sqltype.RegprocValue(typ, uint32(d.function))
			case i == 1:
				row[i] = sqltype.String(typ, d.kind)
			case i == 2:
				row[i], err = sqltype.Integer(typ, int64(d.directArgs))
			case i >= 3 && i <= 10:
				row[i], err = sqltype.RegprocValue(typ, uint32(d.functions[i-3]))
			case i == 11:
				row[i] = sqltype.Boolean(typ, d.finalExtra)
			case i == 12:
				row[i] = sqltype.Boolean(typ, d.movingFinalExtra)
			case i == 13:
				row[i] = sqltype.String(typ, d.finalModify)
			case i == 14:
				row[i] = sqltype.String(typ, d.movingFinalModify)
			case i == 15:
				row[i], err = sqltype.OIDValue(typ, uint32(d.sortOperator))
			case i == 16:
				row[i], err = sqltype.OIDValue(typ, uint32(d.transitionType))
			case i == 17:
				row[i], err = sqltype.Integer(typ, int64(d.transitionSpace))
			case i == 18:
				row[i], err = sqltype.OIDValue(typ, uint32(d.movingType))
			case i == 19:
				row[i], err = sqltype.Integer(typ, int64(d.movingSpace))
			case i == 20:
				row[i] = sqltype.Null(typ)
				if d.hasInitial {
					row[i] = sqltype.String(typ, d.initial)
				}
			case i == 21:
				row[i] = sqltype.Null(typ)
				if d.hasMovingInitial {
					row[i] = sqltype.String(typ, d.movingInitial)
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
	for _, d := range catalog.state.descriptors {
		if p, ok := d.(*procedureDescriptor); ok && p.aggregate != nil {
			if err := put(&p.aggregate.definition); err != nil {
				return nil, err
			}
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
