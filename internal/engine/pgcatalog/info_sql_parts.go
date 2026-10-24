package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSQLPartsOID is a native identity, not a PostgreSQL allocated OID.
const InfoSQLPartsOID ids.OID = 16226

// Supporting individual features does not establish support for an entire part.
// Only returned values carry captured type ownership.
var sqlParts = [...]struct{ id, name string }{
	{"1", "Framework (SQL/Framework)"},
	{"2", "Foundation (SQL/Foundation)"},
	{"3", "Call-Level Interface (SQL/CLI)"},
	{"4", "Persistent Stored Modules (SQL/PSM)"},
	{"9", "Management of External Data (SQL/MED)"},
	{"10", "Object Language Bindings (SQL/OLB)"},
	{"11", "Information and Definition Schema (SQL/Schemata)"},
	{"13", "Routines and Types Using the Java Programming Language (SQL/JRT)"},
	{"14", "XML-Related Specifications (SQL/XML)"},
	{"15", "Multi-Dimensional Arrays (SQL/MDA)"},
	{"16", "Property Graph Queries (SQL/PGQ)"},
}

func captureInfoSQLParts(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSQLPartsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid sql_parts source")
	}
	character, characterOK := in.Catalog.LookupType(13246)
	support, supportOK := in.Catalog.LookupType(13256)
	varchar, varcharOK := in.Catalog.LookupType(sqltype.VarChar)
	if !characterOK || !supportOK || !varcharOK || !character.IsDomain() || !support.IsDomain() || character.BaseType() != varchar || support.BaseType() != varchar {
		return nil, fmt.Errorf("stuntgres: invalid sql_parts domains")
	}
	for i, column := range in.Source.Columns {
		want := character
		if i == 2 {
			want = support
		}
		if column.Usage.Type != want {
			return nil, fmt.Errorf("stuntgres: foreign sql_parts domain")
		}
	}
	rows := make([][]sqltype.Value, len(sqlParts))
	cells := make([]sqltype.Value, len(sqlParts)*5)
	for i, item := range sqlParts {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := cells[i*5 : (i+1)*5 : (i+1)*5]
		for j := range row {
			domain, text := character, ""
			switch j {
			case 0:
				text = item.id
			case 1:
				text = item.name
			case 2:
				domain, text = support, "NO"
			case 3:
				row[j] = sqltype.Null(domain)
				continue
			}
			wrapped, err := domain.Wrap(sqltype.String(varchar, text))
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
