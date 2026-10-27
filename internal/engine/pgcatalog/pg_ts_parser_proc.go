package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Stock parser handlers, vector I/O, the pg_ddl_command receiver and RI trigger
// functions have internal identities without standalone callable providers.
// A represented Procedure takes precedence. REL_18_6 pg_proc.dat uses header
// defaults except the RI functions' explicit volatile classification.
func appendInternalTypeProcedures(in Input, rows [][]sqltype.Value) ([][]sqltype.Value, error) {
	if len(in.Source.Columns) != procedureColumnCount {
		return nil, fmt.Errorf("stuntgres: invalid procedure source shape")
	}
	p := catalog.DefaultTextParser()
	language := in.Catalog.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: "internal"})
	if language == (ids.ObjectAddress{}) {
		return nil, fmt.Errorf("stuntgres: missing parser handler language")
	}
	for _, fact := range [...]struct{ oid ids.OID; volatile bool }{
		{p.Start,false},{p.Token,false},{p.End,false},{p.Headline,false},{p.LexType,false},
		{40,false},{41,false},{54,false},{55,false},{2410,false},{2411,false},{2420,false},{2421,false},{88,false},
		{1644,true},{1645,true},{1646,true},{1647,true},{1648,true},{1649,true},
		{1650,true},{1651,true},{1652,true},{1653,true},{1654,true},{1655,true},
	} {
		oid := fact.oid
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
		volatility := "i"
		if fact.volatile { volatility = "v" }
		for _, field := range [...]struct {
			index int
			text  string
		}{
			{procName, identity.Name}, {procKind, "f"}, {procVolatility, volatility}, {procParallel, "s"}, {procSource, identity.Name},
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
