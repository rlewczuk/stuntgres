package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// statisticExpressionRows projects captured immutable facts, never table rows or
// SQL source. The catalog and facts share the capture's checked generation.
func statisticExpressionRows(ctx context.Context, read catalog.Read, fact *storage.ExtendedStatistics) (sqltype.Value, error) {
	array, ok := read.LookupType(10028)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statistic array type")
	}
	if fact.ExpressionLen() == 0 {
		return sqltype.ResultNull(array.Usage()), nil
	}
	rows := make([]sqltype.Value, fact.ExpressionLen())
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		var err error
		rows[i], err = statisticExpressionRow(read, array.Element(), fact.Expression(i))
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedStatisticRowsValue(array, rows)
}

func statisticExpressionRow(read catalog.Read, rowType *sqltype.Descriptor, fact *storage.ColumnStatistics) (sqltype.Value, error) {
	if fact == nil || fact.Rows == 0 || fact.NullCount > fact.Rows {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression statistics fact")
	}
	fields := make([]sqltype.Value, rowType.CompositeLen())
	for i := range fields {
		d := rowType.CompositeMember(i).Usage.Type
		switch d.ID() {
		case sqltype.Oid:
			fields[i], _ = sqltype.OIDValue(d, 0)
		case sqltype.Int2, sqltype.Int4:
			fields[i], _ = sqltype.Integer(d, 0)
		case sqltype.Bool:
			fields[i] = sqltype.Boolean(d, false)
		case sqltype.Float4:
			fields[i], _ = sqltype.Float(d, 0)
		default:
			fields[i] = sqltype.ResultNull(d.Usage())
		}
	}
	fields[3], _ = sqltype.Float(fields[3].Type(), fact.NullFraction())
	fields[4], _ = sqltype.Integer(fields[4].Type(), int64(fact.AverageWidth))
	distinct, known := fact.EncodedDistinct()
	if known {
		fields[5], _ = sqltype.Float(fields[5].Type(), distinct)
	}
	slot := 0
	if len(fact.MCV) > 0 {
		values := make([]sqltype.Value, len(fact.MCV))
		numbers := make([]sqltype.Value, len(fact.MCV))
		numberType := rowType.CompositeMember(21).Usage.Type.Element()
		for i, mcv := range fact.MCV {
			if mcv.Count == 0 || mcv.Count > fact.Rows || mcv.Value.IsNull() || !mcv.Value.ValidResult(fact.Usage) {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expression MCV")
			}
			values[i] = mcv.Value
			numbers[i], _ = sqltype.Float(numberType, float64(mcv.Count)/float64(fact.Rows))
		}
		numberArray, err := sqltype.OwnedArrayValue(rowType.CompositeMember(21).Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(numbers))}}, numbers)
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[21] = numberArray
		if err := statisticExpressionSlot(read, rowType, fields, fact.Usage, slot, 1, 3, values); err != nil {
			return sqltype.Value{}, err
		}
		slot++
	}
	if len(fact.Histogram) > 0 {
		if err := statisticExpressionSlot(read, rowType, fields, fact.Usage, slot, 2, 1, fact.Histogram); err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedStatisticRowValue(rowType, fields)
}

func statisticExpressionSlot(read catalog.Read, rowType *sqltype.Descriptor, fields []sqltype.Value, usage sqltype.Usage, slot int, kind int64, strategy int16, values []sqltype.Value) error {
	array, ok := read.LookupType(usage.Type.AssociatedArray())
	if !ok || array.Element() != usage.Type {
		return fmt.Errorf("stuntgres: unavailable statistic value array")
	}
	for _, value := range values {
		if value.IsNull() || !value.ValidResult(usage) {
			return fmt.Errorf("stuntgres: foreign statistic slot value")
		}
	}
	dimensions := []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
	var concrete sqltype.Value
	var err error
	if kind == 1 {
		// MCV projection just allocated this exclusive buffer; histogram storage
		// remains shared immutable capture state and must instead be frozen below.
		concrete, err = sqltype.OwnedArrayValue(array, dimensions, values)
	} else {
		concrete, err = sqltype.ArrayValue(array, dimensions, values)
	}
	if err != nil {
		return err
	}
	wrapped, err := sqltype.StatisticArrayValue(rowType.CompositeMember(26+slot).Usage.Type, concrete, usage)
	if err != nil {
		return err
	}
	class, found, err := read.DefaultIndexClass(403, usage.Type)
	if err != nil {
		return err
	}
	if !found && kind == 1 {
		// MCV needs equality, not ordering. Equality-only types retain their
		// genuine hash-family equality operator even without a btree class.
		class, found, err = read.DefaultIndexClass(405, usage.Type)
		if err != nil {
			return err
		}
		strategy = 1
	}
	if !found {
		return fmt.Errorf("stuntgres: unavailable statistic operator class")
	}
	member, found := read.FamilyOperator(ids.OID(class.FamilyOID()), ids.OID(class.InputOID()), ids.OID(class.InputOID()), strategy, 's')
	if !found {
		return fmt.Errorf("stuntgres: unavailable statistic slot operator")
	}
	fields[6+slot], _ = sqltype.Integer(fields[6+slot].Type(), kind)
	fields[11+slot], err = sqltype.OIDValue(fields[11+slot].Type(), uint32(member.Operator))
	if err != nil {
		return err
	}
	fields[16+slot], err = sqltype.OIDValue(fields[16+slot].Type(), usage.Collation.ID)
	if err != nil {
		return err
	}
	fields[26+slot] = wrapped
	return nil
}
