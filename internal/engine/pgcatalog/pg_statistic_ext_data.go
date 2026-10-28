package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatisticExtDataOID ids.OID = 3429

func capturePGStatisticExtData(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: StatisticExtDataOID}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid statistics data source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, StatisticExtDataOID); err != nil {
		return nil, err
	}
	if err := in.Runtime.SelectColumns(in.Ref, in.Source); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	err := in.Runtime.VisitExtendedStatistics(in.Context, func(oid ids.OID, inherited bool, value, dependencies, mcv, expressions sqltype.Value) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, 6)
		var err error
		row[0], err = sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(oid))
		if err != nil {
			return err
		}
		row[1] = sqltype.Boolean(in.Source.Columns[1].Usage.Type, inherited)
		if !value.ValidResult(in.Source.Columns[2].Usage) {
			return fmt.Errorf("stuntgres: foreign ndistinct payload")
		}
		row[2] = value
		if !dependencies.ValidResult(in.Source.Columns[3].Usage) {
			return fmt.Errorf("stuntgres: foreign dependencies payload")
		}
		row[3] = dependencies
		if !mcv.ValidResult(in.Source.Columns[4].Usage) {
			return fmt.Errorf("stuntgres: foreign MCV payload")
		}
		row[4] = mcv
		if !expressions.ValidResult(in.Source.Columns[5].Usage) {
			return fmt.Errorf("stuntgres: foreign expression statistics payload")
		}
		row[5] = expressions
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, in.Context.Err()
}
