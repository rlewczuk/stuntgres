package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Stock parser handlers, vector I/O and the genuine pg_ddl_command receiver have
// internal catalog identities without granting standalone callable providers.
// A represented Procedure always takes precedence over these selected facts.
func appendInternalTypeProcedures(in Input, rows [][]sqltype.Value) ([][]sqltype.Value, error) {
	if len(in.Source.Columns) != 20 {
		return nil, fmt.Errorf("stuntgres: invalid procedure source shape")
	}
	p := catalog.DefaultTextParser()
	language := in.Catalog.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: "internal"})
	if language == (ids.ObjectAddress{}) {
		return nil, fmt.Errorf("stuntgres: missing parser handler language")
	}
	for _, oid := range [14]ids.OID{p.Start, p.Token, p.End, p.Headline, p.LexType, 40, 41, 54, 55, 2410, 2411, 2420, 2421, 88} {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if _, represented := in.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}); represented {
			continue
		}
		identity, ok := catalog.BuiltinRoutineIdentityByOID(oid)
		result, resultOK := catalog.BuiltinRoutineResultByOID(oid)
		if !ok || !resultOK {
			return nil, fmt.Errorf("stuntgres: missing internal routine identity %d", oid)
		}
		row := make([]sqltype.Value, len(in.Source.Columns))
		fields := [6]struct {
			index int
			oid   uint32
		}{{0, uint32(oid)}, {2, uint32(identity.Namespace)}, {3, uint32(in.Source.Owner.ID())}, {5, uint32(result)}, {11, uint32(language.ObjectID)}, {16, 0}}
		for _, field := range fields {
			i := field.index
			v, err := sqltype.OIDValue(in.Source.Columns[i].Usage.Type, field.oid)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, identity.Name)
		arity := int64(strings.Count(string(identity.Signature), ",") + 1)
		var err error
		row[4], err = sqltype.Integer(in.Source.Columns[4].Usage.Type, arity)
		if err != nil {
			return nil, err
		}
		row[6] = sqltype.String(in.Source.Columns[6].Usage.Type, "i")
		row[7] = sqltype.Boolean(in.Source.Columns[7].Usage.Type, true)
		row[8] = sqltype.String(in.Source.Columns[8].Usage.Type, "s")
		row[9] = sqltype.String(in.Source.Columns[9].Usage.Type, "f")
		row[10] = sqltype.Null(in.Source.Columns[10].Usage.Type)
		row[12] = sqltype.Boolean(in.Source.Columns[12].Usage.Type, false)
		row[13], err = sqltype.Float(in.Source.Columns[13].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		row[14] = sqltype.String(in.Source.Columns[14].Usage.Type, identity.Name)
		row[15], err = procedureArgumentVector(in.Context, in.Source.Columns[15].Usage.Type, identity.Signature)
		if err != nil {
			return nil, err
		}
		row[17] = sqltype.Null(in.Source.Columns[17].Usage.Type)
		row[18] = sqltype.Null(in.Source.Columns[18].Usage.Type)
		row[19] = sqltype.Null(in.Source.Columns[19].Usage.Type)
		rows = append(rows, row)
	}
	return rows, nil
}
