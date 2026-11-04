package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatXactUserFunctionsOID ids.OID = 12284

// FunctionFact is detached function activity. Logical times are completed
// invocation units, each representing one millisecond of own work.
type FunctionFact struct {
	Calls, LogicalTotal, LogicalSelf int64
}

var functionStatisticsColumns = []ColumnSpec{
	{"funcid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 0, 0, -1},
	{"funcname", sqltype.Name, true, 0, 0, -1},
	{"calls", sqltype.Int8, true, 0, 0, -1},
	{"total_time", sqltype.Float8, true, 0, 0, -1},
	{"self_time", sqltype.Float8, true, 0, 0, -1},
}

func captureXactFunctionStatistics(in Input) ([][]sqltype.Value, error) {
	return captureFunctionStatisticsRows(in, StatXactUserFunctionsOID)
}

func captureFunctionStatisticsRows(in Input, sourceOID ids.OID) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, functionStatisticsColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != sourceOID || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid function statistics authority")
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Procedure)
		if !ok || d.Display.Language == catalog.InternalRoutineLanguage {
			continue
		}
		ns, ok := in.Catalog.Namespace(d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing function statistics namespace")
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		schema := sqltype.String(in.Source.Columns[1].Usage.Type, ns.Name())
		name := sqltype.String(in.Source.Columns[2].Usage.Type, d.Name())
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return oid, nil }) || in.Keep.Rejects(1, func() (sqltype.Value, error) { return schema, nil }) || in.Keep.Rejects(2, func() (sqltype.Value, error) { return name, nil }) {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		var fact FunctionFact
		var present bool
		if sourceOID == StatXactUserFunctionsOID {
			fact, present = in.Runtime.XactFunctionStatistics(d.Address())
		} else {
			fact, present = in.Runtime.FunctionStatistics(d.Address())
		}
		if !present {
			continue
		}
		calls, err := sqltype.Integer(in.Source.Columns[3].Usage.Type, fact.Calls)
		if err != nil {
			return nil, err
		}
		total, err := sqltype.Float(in.Source.Columns[4].Usage.Type, float64(fact.LogicalTotal))
		if err != nil {
			return nil, err
		}
		self, err := sqltype.Float(in.Source.Columns[5].Usage.Type, float64(fact.LogicalSelf))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, schema, name, calls, total, self})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
