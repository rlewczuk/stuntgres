package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// domainNulls returns one row of typed NULLs, each wrapped in its column's
// domain. Producers clone it per row and set only the cells they fill.
func domainNulls(columns []catalog.Column) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(columns))
	for i, c := range columns {
		v, err := c.Usage.Type.Wrap(sqltype.Null(c.Usage.Type.BaseType()))
		if err != nil {
			return nil, err
		}
		row[i] = v
	}
	return row, nil
}

// domainText is the cell a producer writes for value in a text-based domain
// column once wrapDomainCells wraps it.
func domainText(column catalog.Column, value string) (sqltype.Value, error) {
	return column.Usage.Type.Wrap(sqltype.String(column.Usage.Type.BaseType(), value))
}

// wrapDomainCells wraps the cells of a cloned domainNulls row that the
// producer replaced with base-type values.
func wrapDomainCells(columns []catalog.Column, row []sqltype.Value) error {
	for i := range row {
		if t := columns[i].Usage.Type; row[i].Type() != t {
			v, err := t.Wrap(row[i])
			if err != nil {
				return err
			}
			row[i] = v
		}
	}
	return nil
}
