package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Projection positions are the released pg_proc attribute numbers minus one.
// Both descriptor and metadata-only producers use this single layout.
const (
	procOID = iota
	procName
	procNamespace
	procOwner
	procLanguage
	procCost
	procRows
	procVariadic
	procSupport
	procKind
	procSecurityDefiner
	procLeakproof
	procStrict
	procReturnsSet
	procVolatility
	procParallel
	procNArgs
	procNDefaults
	procResult
	procArgTypes
	procAllArgTypes
	procArgModes
	procArgNames
	procArgDefaults
	procTransformTypes
	procSource
	procBinary
	procSQLBody
	procConfig
	procACL
	procedureColumnCount
)

var procedureColumnTypes = [procedureColumnCount]sqltype.ID{
	sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Oid,
	sqltype.Float4, sqltype.Float4, sqltype.Oid, sqltype.Regproc, sqltype.Char,
	sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Char,
	sqltype.Char, sqltype.Int2, sqltype.Int2, sqltype.Oid, sqltype.OidVector,
	1028, 1002, 1009, sqltype.PgNodeTree, 1028, sqltype.Text, sqltype.Text,
	sqltype.PgNodeTree, 1009, 1034,
}

func procedureRow(in Input, d *catalog.Procedure) ([]sqltype.Value, error) {
	ctx, view, source := in.Context, in.Catalog, in.Source
	if !d.Display.Valid() {
		return nil, fmt.Errorf("stuntgres: invalid routine declaration display")
	}
	row := make([]sqltype.Value, procedureColumnCount)
	for i := range row {
		row[i] = sqltype.Null(source.Columns[i].Usage.Type)
	}
	var valueErr error
	integer := func(i int, n int64) {
		var err error
		if source.Columns[i].Usage.Type.ID() == sqltype.Oid {
			row[i], err = sqltype.OIDValue(source.Columns[i].Usage.Type, uint32(n))
		} else {
			row[i], err = sqltype.Integer(source.Columns[i].Usage.Type, n)
		}
		if err != nil {
			valueErr = err
		}
	}
	text := func(i int, s string) { row[i] = sqltype.String(source.Columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) { row[i] = sqltype.Boolean(source.Columns[i].Usage.Type, b) }
	floating := func(i int, n float64) {
		var err error
		row[i], err = sqltype.Float(source.Columns[i].Usage.Type, n)
		if err != nil {
			valueErr = err
		}
	}
	integer(procOID, int64(d.Address().ObjectID))
	text(procName, d.Name())
	integer(procNamespace, int64(d.Namespace().ObjectID))
	integer(procOwner, int64(d.Owner.ID()))
	integer(procNArgs, ProcedureArity(d))
	integer(procResult, int64(d.Result))
	language := ""
	switch d.Display.Language {
	case catalog.InternalRoutineLanguage:
		language = "internal"
	case catalog.CRoutineLanguage:
		language = "c"
	case catalog.SQLRoutineLanguage:
		language = "sql"
	case catalog.PLpgSQLRoutineLanguage:
		language = "plpgsql"
	case catalog.GoRoutineLanguage:
		// Native callbacks have no PostgreSQL language. This is the established
		// native-only sentinel, never a fallback for a missing SQL language.
	default:
		return nil, fmt.Errorf("stuntgres: missing routine display language")
	}
	languageAddress := d.Language
	if languageAddress == (ids.ObjectAddress{}) && language != "" {
		languageAddress = view.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: language})
	}
	if language != "" {
		owned, ok := view.Get(languageAddress).(*catalog.Language)
		if !ok || owned.Name() != language {
			return nil, fmt.Errorf("stuntgres: missing routine declaration language")
		}
	} else if languageAddress != (ids.ObjectAddress{}) {
		return nil, fmt.Errorf("stuntgres: native routine has SQL language")
	}
	integer(procLanguage, int64(languageAddress.ObjectID))
	vol := "v"
	if d.Volatility == expr.Stable {
		vol = "s"
	} else if d.Volatility == expr.Immutable {
		vol = "i"
	}
	text(procVolatility, vol)
	boolean(procStrict, d.Strict)
	parallel := "s"
	if d.Foreign != nil {
		parallel = "u"
	} else if d.Aggregate != nil && d.DynamicCallable() {
		parallel = "u"
		if d.Aggregate.Parallel == 's' || d.Aggregate.Parallel == 'r' {
			parallel = string(d.Aggregate.Parallel)
		}
	} else if d.Builtin != nil {
		switch d.Builtin.Parallel {
		case 'r':
			parallel = "r"
		case 'u':
			parallel = "u"
		}
	}
	if d.Installed.Parallel != 0 {
		parallel = string(d.Installed.Parallel)
	}
	if d.Parallel != 0 {
		parallel = string(d.Parallel)
	}
	text(procParallel, parallel)
	text(procKind, string(d.RoutineKind))
	text(procSource, d.Display.Source)
	if d.Display.HasBinary {
		text(procBinary, d.Display.Binary)
	}
	var err error
	row[procSupport], err = sqltype.RegprocValue(source.Columns[procSupport].Usage.Type, uint32(d.Display.Support))
	if err != nil {
		return nil, err
	}
	boolean(procLeakproof, d.Display.Leakproof)
	variadic, err := ProcedureVariadic(d)
	if err != nil {
		return nil, err
	}
	row[procArgTypes], err = procedureArgumentVector(ctx, source.Columns[procArgTypes].Usage.Type, d.Signature)
	if err != nil {
		return nil, err
	}
	row[procAllArgTypes], row[procArgModes], err = procedureFormalArrays(ctx, d, source.Columns[procAllArgTypes].Usage.Type, source.Columns[procArgModes].Usage.Type, variadic, row[procArgTypes])
	if err != nil {
		return nil, err
	}
	names, err := ProcedureNameProjection(view, d)
	if err != nil {
		return nil, err
	}
	if names.present {
		elements := make([]sqltype.Value, names.count)
		for i := range elements {
			elements[i] = sqltype.String(source.Columns[procArgNames].Usage.Type.Element(), names.At(i))
		}
		row[procArgNames], err = sqltype.OwnedArrayValue(source.Columns[procArgNames].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
		if err != nil {
			return nil, err
		}
	}
	if installation, ok := catalog.OriginInstallation(d); ok && d.Implementation != nil && (installation.Provider == "pgrowlocks" || installation.Provider == "intagg" && d.RoutineKind == catalog.FunctionRoutine && d.Signature == "1007" && d.Result == 23) {
		if err := validateInstalledProcedureOwner(view, d); err != nil {
			return nil, err
		}
	}
	boolean(procReturnsSet, d.ReturnsSet())
	floating(procRows, float64(d.RowEstimate()))
	integer(procVariadic, int64(variadic))
	integer(procNDefaults, procedureDefaultCount(d))
	row[procArgDefaults], row[procSQLBody], err = in.Runtime.RoutineTrees(ctx, view, d, source.Columns[procArgDefaults].Usage.Type)
	if err != nil {
		return nil, err
	}
	if d.Display.TransformTypes.Len() != 0 {
		elements := make([]sqltype.Value, d.Display.TransformTypes.Len())
		for i := range elements {
			id := d.Display.TransformTypes.At(i)
			if _, ok := view.LookupType(sqltype.ID(id)); !ok {
				return nil, fmt.Errorf("stuntgres: missing routine transform type")
			}
			elements[i], err = sqltype.OIDValue(source.Columns[procTransformTypes].Usage.Type.Element(), uint32(id))
			if err != nil {
				return nil, err
			}
		}
		row[procTransformTypes], err = sqltype.OwnedArrayValue(source.Columns[procTransformTypes].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
		if err != nil {
			return nil, err
		}
	}
	floating(procCost, float64(d.CostEstimate()))
	boolean(procSecurityDefiner, d.SecurityDefiner)
	if d.Configuration.Len() != 0 {
		elements := make([]sqltype.Value, d.Configuration.Len())
		for i := range elements {
			elements[i] = sqltype.String(source.Columns[procConfig].Usage.Type.Element(), d.Configuration.At(i))
		}
		row[procConfig], err = sqltype.OwnedArrayValue(source.Columns[procConfig].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
		if err != nil {
			return nil, err
		}
	}
	acl, err := in.Runtime.ObjectACL(view, d.Address())
	if err != nil {
		return nil, err
	}
	row[procACL], err = ClassACL(in.Owner, in.Roles, source.Columns[procACL].Usage.Type, acl)
	if err != nil {
		return nil, err
	}
	if valueErr != nil {
		return nil, valueErr
	}
	return row, nil
}
