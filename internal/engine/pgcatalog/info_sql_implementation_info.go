package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSQLImplementationInfoOID is a native identity, not a PostgreSQL allocated OID.
const InfoSQLImplementationInfoOID ids.OID = 16225

// Only the returned values carry captured type ownership. These initial product
// characteristics do not track session settings or user-created collations.
var sqlImplementationInfo = [...]struct {
	id, name       string
	integer        int64
	integerPresent bool
	character      string
	comment        string
	commentPresent bool
}{
	{"10003", "CATALOG NAME", 0, false, "Y", "", false},
	{"10004", "COLLATING SEQUENCE", 0, false, "C", "", false},
	{"23", "CURSOR COMMIT BEHAVIOR", 1, true, "", "close cursors and retain prepared statements", true},
	{"2", "DATA SOURCE NAME", 0, false, "", "", false},
	{"17", "DBMS NAME", 0, false, "stuntgres", "", false},
	{"18", "DBMS VERSION", 0, false, functions.EmbeddedVersionBanner, "", false},
	{"26", "DEFAULT TRANSACTION ISOLATION", 2, true, "", "READ COMMITTED; user-settable", true},
	{"28", "IDENTIFIER CASE", 3, true, "", "stored in mixed case - case sensitive", true},
	{"85", "NULL COLLATION", 0, true, "", "nulls higher than non-nulls", true},
	{"13", "SERVER NAME", 0, false, "", "", false},
	{"94", "SPECIAL CHARACTERS", 0, false, "", "all non-ASCII characters allowed", true},
	{"46", "TRANSACTION CAPABLE", 2, true, "", "both DML and DDL", true},
}

func captureInfoSQLImplementationInfo(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSQLImplementationInfoOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid sql_implementation_info source")
	}
	character, characterOK := in.Catalog.LookupType(13246)
	cardinal, cardinalOK := in.Catalog.LookupType(13243)
	varchar, varcharOK := in.Catalog.LookupType(sqltype.VarChar)
	integer, integerOK := in.Catalog.LookupType(sqltype.Int4)
	if !characterOK || !cardinalOK || !varcharOK || !integerOK || !character.IsDomain() || !cardinal.IsDomain() || character.BaseType() != varchar || cardinal.BaseType() != integer {
		return nil, fmt.Errorf("stuntgres: invalid sql_implementation_info domains")
	}
	for i, column := range in.Source.Columns {
		want := character
		if i == 2 {
			want = cardinal
		}
		if column.Usage.Type != want {
			return nil, fmt.Errorf("stuntgres: foreign sql_implementation_info domain")
		}
	}
	rows := make([][]sqltype.Value, len(sqlImplementationInfo))
	cells := make([]sqltype.Value, len(sqlImplementationInfo)*5)
	for i, item := range sqlImplementationInfo {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := cells[i*5 : (i+1)*5 : (i+1)*5]
		for j := range row {
			domain := character
			value := sqltype.String(varchar, "")
			switch j {
			case 0:
				value = sqltype.String(varchar, item.id)
			case 1:
				value = sqltype.String(varchar, item.name)
			case 2:
				domain = cardinal
				if !item.integerPresent {
					row[j] = sqltype.Null(domain)
					continue
				}
				var err error
				value, err = sqltype.Integer(integer, item.integer)
				if err != nil {
					return nil, err
				}
			case 3:
				if item.integerPresent {
					row[j] = sqltype.Null(domain)
					continue
				}
				value = sqltype.String(varchar, item.character)
			case 4:
				if !item.commentPresent {
					row[j] = sqltype.Null(domain)
					continue
				}
				value = sqltype.String(varchar, item.comment)
			}
			wrapped, err := domain.Wrap(value)
			if err != nil {
				return nil, err
			}
			row[j] = wrapped
		}
		rows[i] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
