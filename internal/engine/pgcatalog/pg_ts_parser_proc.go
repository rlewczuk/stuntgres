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
// REL_18_6 pg_proc.dat uses all pg_proc.h property defaults for these 14 rows.
func appendInternalTypeProcedures(in Input, rows [][]sqltype.Value) ([][]sqltype.Value, error) {
	if len(in.Source.Columns) != procedureColumnCount {
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
		row := make([]sqltype.Value, procedureColumnCount)
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		for _, field := range [...]struct {
			index int
			oid   uint32
		}{
			{procOID, uint32(oid)}, {procNamespace, uint32(identity.Namespace)}, {procOwner, uint32(in.Source.Owner.ID())},
			{procLanguage, uint32(language.ObjectID)}, {procVariadic, 0}, {procResult, uint32(result)},
		} {
			value, err := sqltype.OIDValue(in.Source.Columns[field.index].Usage.Type, field.oid)
			if err != nil {
				return nil, err
			}
			row[field.index] = value
		}
		for _, field := range [...]struct {
			index int
			text  string
		}{
			{procName, identity.Name}, {procKind, "f"}, {procVolatility, "i"}, {procParallel, "s"}, {procSource, identity.Name},
		} {
			row[field.index] = sqltype.String(in.Source.Columns[field.index].Usage.Type, field.text)
		}
		for _, field := range [...]struct {
			index int
			value bool
		}{
			{procSecurityDefiner, false}, {procLeakproof, false}, {procStrict, true}, {procReturnsSet, false},
		} {
			row[field.index] = sqltype.Boolean(in.Source.Columns[field.index].Usage.Type, field.value)
		}
		var err error
		row[procSupport], err = sqltype.RegprocValue(in.Source.Columns[procSupport].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		arity := int64(0)
		if identity.Signature != "" {
			arity = int64(strings.Count(string(identity.Signature), ",") + 1)
		}
		row[procNArgs], err = sqltype.Integer(in.Source.Columns[procNArgs].Usage.Type, arity)
		if err != nil {
			return nil, err
		}
		row[procNDefaults], err = sqltype.Integer(in.Source.Columns[procNDefaults].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		row[procCost], err = sqltype.Float(in.Source.Columns[procCost].Usage.Type, 1)
		if err != nil {
			return nil, err
		}
		row[procRows], err = sqltype.Float(in.Source.Columns[procRows].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		row[procArgTypes], err = procedureArgumentVector(in.Context, in.Source.Columns[procArgTypes].Usage.Type, identity.Signature)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}
