package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSQLSizingOID is the native relation identity, not a PostgreSQL allocated OID.
const InfoSQLSizingOID ids.OID = 16224

// A negative value represents SQL NULL (unsupported); zero means unlimited or
// undetermined. Only stable enforced native bounds are reported as positive.
// Cursor names, SELECT targets and SYSTEM_USER do not have an established bound.
var sqlSizingItems = [...]struct {
	id    int64
	name  string
	value int64
}{
	{0, "MAXIMUM DRIVER CONNECTIONS", -1},
	{1, "MAXIMUM CONCURRENT ACTIVITIES", 0},
	{30, "MAXIMUM COLUMN NAME LENGTH", 63},
	{31, "MAXIMUM CURSOR NAME LENGTH", 0},
	{32, "MAXIMUM SCHEMA NAME LENGTH", 63},
	{34, "MAXIMUM CATALOG NAME LENGTH", 63},
	{35, "MAXIMUM TABLE NAME LENGTH", 63},
	{97, "MAXIMUM COLUMNS IN GROUP BY", 0},
	{99, "MAXIMUM COLUMNS IN ORDER BY", 0},
	{100, "MAXIMUM COLUMNS IN SELECT", 0},
	{101, "MAXIMUM COLUMNS IN TABLE", 1600},
	{106, "MAXIMUM TABLES IN SELECT", 0},
	{107, "MAXIMUM USER NAME LENGTH", 63},
	{10005, "MAXIMUM IDENTIFIER LENGTH", 63},
	{20000, "MAXIMUM STATEMENT OCTETS", 0},
	{20001, "MAXIMUM STATEMENT OCTETS DATA", 0},
	{20002, "MAXIMUM STATEMENT OCTETS SCHEMA", 0},
	{25000, "MAXIMUM CURRENT DEFAULT TRANSFORM GROUP LENGTH", -1},
	{25001, "MAXIMUM CURRENT TRANSFORM GROUP LENGTH", -1},
	{25002, "MAXIMUM CURRENT PATH LENGTH", 0},
	{25003, "MAXIMUM CURRENT ROLE LENGTH", -1},
	{25004, "MAXIMUM SESSION USER LENGTH", 63},
	{25005, "MAXIMUM SYSTEM USER LENGTH", 0},
}

func captureInfoSQLSizing(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSQLSizingOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid sql_sizing source")
	}
	cardinal, cardinalOK := in.Catalog.LookupType(13243)
	character, characterOK := in.Catalog.LookupType(13246)
	integer, integerOK := in.Catalog.LookupType(sqltype.Int4)
	varchar, varcharOK := in.Catalog.LookupType(sqltype.VarChar)
	if !cardinalOK || !characterOK || !integerOK || !varcharOK || !cardinal.IsDomain() || !character.IsDomain() || cardinal.BaseType() != integer || character.BaseType() != varchar {
		return nil, fmt.Errorf("stuntgres: invalid sql_sizing domains")
	}
	for i, column := range in.Source.Columns {
		want := cardinal
		if i%2 != 0 {
			want = character
		}
		if column.Usage.Type != want {
			return nil, fmt.Errorf("stuntgres: foreign sql_sizing domain")
		}
	}
	rows := make([][]sqltype.Value, len(sqlSizingItems))
	cells := make([]sqltype.Value, len(sqlSizingItems)*4)
	for i, item := range sqlSizingItems {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := cells[i*4 : (i+1)*4 : (i+1)*4]
		// Use the same integer/domain conversion as other numeric metadata.
		for j, number := range [2]int64{item.id, item.value} {
			column := j * 2
			row[column] = sqltype.Null(cardinal)
			if number >= 0 {
				value, err := sqltype.Integer(integer, number)
				if err != nil {
					return nil, err
				}
				row[column], err = cardinal.Wrap(value)
				if err != nil {
					return nil, err
				}
			}
		}
		name, err := character.Wrap(sqltype.String(varchar, item.name))
		if err != nil {
			return nil, err
		}
		row[1], row[3] = name, sqltype.Null(character)
		if item.value == 63 {
			row[3], err = character.Wrap(sqltype.String(varchar, "Might be less, depending on character set."))
			if err != nil {
				return nil, err
			}
		}
		rows[i] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
