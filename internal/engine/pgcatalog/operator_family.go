package pgcatalog

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func captureOperatorFamilies(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	var definitions []catalog.Descriptor
	for a, d := range in.Catalog.Descriptors() {
		if a.ClassID == in.Source.Address().ObjectID {
			definitions = append(definitions, d)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Address().ObjectID < definitions[j].Address().ObjectID })
	width := len(in.Source.Columns)
	rows := make([][]sqltype.Value, len(definitions))
	cells := make([]sqltype.Value, len(rows)*width)
	for i, d := range definitions {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		var numbers []uint32
		switch d := d.(type) {
		case *catalog.OperatorFamily:
			numbers = []uint32{uint32(d.Address().ObjectID), uint32(d.Method), 0, uint32(d.Namespace().ObjectID), uint32(d.Owner.ID())}
		case *catalog.OperatorFamilyOperator:
			numbers = []uint32{uint32(d.Address().ObjectID), uint32(d.Family), uint32(d.Left), uint32(d.Right), uint32(d.Number), 0, uint32(d.Operator), uint32(d.Method), uint32(d.SortFamily)}
		case *catalog.OperatorFamilySupport:
			numbers = []uint32{uint32(d.Address().ObjectID), uint32(d.Family), uint32(d.Left), uint32(d.Right), uint32(d.Number), uint32(d.Function)}
		default:
			return nil, fmt.Errorf("stuntgres: invalid operator family source descriptor")
		}
		if width != len(numbers) {
			return nil, fmt.Errorf("stuntgres: invalid operator family source shape")
		}
		row := cells[i*width : (i+1)*width : (i+1)*width]
		for j, n := range numbers {
			typ := in.Source.Columns[j].Usage.Type
			switch typ.ID() {
			case sqltype.Name:
				row[j] = sqltype.String(typ, d.Name())
			case sqltype.Char:
				row[j] = sqltype.String(typ, string([]byte{d.(*catalog.OperatorFamilyOperator).Purpose}))
			case sqltype.Int2:
				v, err := sqltype.Integer(typ, int64(n))
				if err != nil {
					return nil, err
				}
				row[j] = v
			case sqltype.Regproc:
				v, err := sqltype.RegprocValue(typ, n)
				if err != nil {
					return nil, err
				}
				row[j] = v
			case sqltype.Oid:
				v, err := sqltype.OIDValue(typ, n)
				if err != nil {
					return nil, err
				}
				row[j] = v
			default:
				return nil, fmt.Errorf("stuntgres: invalid operator family source type")
			}
		}
		rows[i] = row
	}
	return rows, nil
}
