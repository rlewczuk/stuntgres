package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoSQLFeaturesOID is the native relation identity, not a PostgreSQL allocated OID.
const InfoSQLFeaturesOID ids.OID = 16222

func captureInfoSQLFeatures(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoSQLFeaturesOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid sql_features source")
	}
	characterData, characterOK := in.Catalog.LookupType(13246)
	yesOrNo, yesOK := in.Catalog.LookupType(13256)
	base, baseOK := in.Catalog.LookupType(sqltype.VarChar)
	if !characterOK || !yesOK || !baseOK || !characterData.IsDomain() || !yesOrNo.IsDomain() || characterData.BaseType() != base || yesOrNo.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: invalid sql_features domains")
	}
	for i, column := range in.Source.Columns {
		want := characterData
		if i == 4 {
			want = yesOrNo
		}
		if column.Usage.Type != want {
			return nil, fmt.Errorf("stuntgres: foreign sql_features domain")
		}
	}
	rows := make([][]sqltype.Value, len(sqlFeaturesRelease))
	cells := make([]sqltype.Value, len(sqlFeaturesRelease)*7)
	for i := range sqlFeaturesRelease {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		fields := &sqlFeaturesRelease[i]
		row := cells[i*7 : (i+1)*7 : (i+1)*7]
		for j, text := range fields {
			domain, column := characterData, j
			if j == 4 {
				domain = yesOrNo
			}
			if j == 5 {
				column = 6
			}
			value, err := domain.Wrap(sqltype.String(base, text))
			if err != nil {
				return nil, err
			}
			row[column] = value
		}
		row[5] = sqltype.Null(characterData)
		rows[i] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
