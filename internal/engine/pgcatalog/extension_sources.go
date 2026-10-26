package pgcatalog

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func captureExtensionCatalog(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.Address().ObjectID == ids.ExtensionClassID {
		ids := [8]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Bool, sqltype.Text, 1028, 1009}
		if len(source.Columns) != len(ids) {
			return nil, fmt.Errorf("stuntgres: invalid extension source shape")
		}
		for i, column := range source.Columns {
			owned, ok := view.LookupType(ids[i])
			if !ok || owned != column.Usage.Type {
				return nil, fmt.Errorf("stuntgres: foreign extension source type")
			}
		}
	}
	if source.Address().ObjectID == ids.ProcedureClassID {
		expected := [17]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Int2, sqltype.Oid, sqltype.Char, sqltype.Bool, sqltype.Char, sqltype.Char, 1009, sqltype.Oid, sqltype.Bool, sqltype.Float4, sqltype.Text, sqltype.OidVector, sqltype.Oid}
		if len(source.Columns) != len(expected) {
			return nil, fmt.Errorf("stuntgres: invalid procedure source shape")
		}
		for i, id := range expected {
			owned, ok := view.LookupType(id)
			if !ok || owned != source.Columns[i].Usage.Type {
				return nil, fmt.Errorf("stuntgres: foreign procedure source type")
			}
		}
	}
	descriptors := make([]catalog.Descriptor, 0)
	for _, d := range view.Descriptors() {
		if d.Address().ClassID == source.Address().ObjectID {
			descriptors = append(descriptors, d)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	capacity := len(descriptors)
	if source.Address().ObjectID == ids.ProcedureClassID {
		capacity += 13
	}
	rows := make([][]sqltype.Value, 0, capacity)
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(source.Columns))
		var valueErr error
		integer := func(i int, n int64) {
			var v sqltype.Value
			var err error
			if source.Columns[i].Usage.Type.ID() == sqltype.Oid {
				v, err = sqltype.OIDValue(source.Columns[i].Usage.Type, uint32(n))
			} else {
				v, err = sqltype.Integer(source.Columns[i].Usage.Type, n)
			}
			if err != nil {
				valueErr = err
			}
			row[i] = v
		}
		text := func(i int, s string) { row[i] = sqltype.String(source.Columns[i].Usage.Type, s) }
		boolean := func(i int, b bool) { row[i] = sqltype.Boolean(source.Columns[i].Usage.Type, b) }
		switch d := descriptor.(type) {
		case *catalog.Extension:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Owner.ID()))
			integer(3, int64(d.TargetNamespace.ObjectID))
			boolean(4, true)
			text(5, d.Version)
			row[6] = sqltype.Null(source.Columns[6].Usage.Type)
			row[7] = sqltype.Null(source.Columns[7].Usage.Type)
		case *catalog.Procedure:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			integer(3, int64(d.Owner.ID()))
			integer(4, ProcedureArity(d))
			integer(5, int64(d.Result))
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
			}
			languageAddress := view.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: language})
			integer(11, int64(languageAddress.ObjectID))
			vol := "v"
			if d.Volatility == expr.Stable {
				vol = "s"
			} else if d.Volatility == expr.Immutable {
				vol = "i"
			}
			text(6, vol)
			boolean(7, d.Strict)
			parallel := "s"
			if d.Foreign != nil || d.Aggregate != nil && d.DynamicCallable() {
				parallel = "u"
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
			text(8, parallel)
			text(9, string(d.RoutineKind))
			text(14, d.Display.Source)
			var argumentErr error
			row[15], argumentErr = procedureArgumentVector(ctx, source.Columns[15].Usage.Type, d.Signature)
			if argumentErr != nil {
				return nil, argumentErr
			}
			row[10] = sqltype.Null(source.Columns[10].Usage.Type)
			if d.SQL != nil {
				named := false
				elements := make([]sqltype.Value, len(d.SQL.Formals))
				for i, formal := range d.SQL.Formals {
					named = named || formal.Name != ""
					elements[i] = sqltype.String(source.Columns[10].Usage.Type.Element(), formal.Name)
				}
				if named {
					row[10], valueErr = sqltype.OwnedArrayValue(source.Columns[10].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
				}
			} else if d.Aggregate != nil && len(d.Aggregate.ArgumentNames) > 0 {
				names := d.Aggregate.ArgumentNames
				if int64(len(names)) != ProcedureArity(d) {
					return nil, fmt.Errorf("stuntgres: invalid aggregate argument-name declaration")
				}
				elements := make([]sqltype.Value, len(names))
				for i, name := range names {
					elements[i] = sqltype.String(source.Columns[10].Usage.Type.Element(), name)
				}
				row[10], valueErr = sqltype.OwnedArrayValue(source.Columns[10].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
			} else if d.Builtin != nil && d.Builtin.Providers&functions.FTSParserRoutineProvider != 0 {
				names := [4]string{"parser_name", "tokid", "alias", "description"}
				if strings.HasPrefix(string(d.Builtin.Identity.Signature), "26") {
					names[0] = "parser_oid"
				}
				if d.Builtin.Identity.Name == "ts_parse" {
					names[1], names[2], names[3] = "txt", "tokid", "token"
				}
				elements := make([]sqltype.Value, len(names))
				for i, name := range names {
					elements[i] = sqltype.String(source.Columns[10].Usage.Type.Element(), name)
				}
				row[10], valueErr = sqltype.OwnedArrayValue(source.Columns[10].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: 4}}, elements)
			} else if ext, ok := view.Extension(d.Extension); ok && ext.Name() == "uuid-ossp" && d.Implementation != nil && slices.Contains(ext.Members, d.Address()) {
				// Use the catalog's existing validation capability and the
				// released manifest slot, without importing an upper owner.
				if err := d.Implementation.ValidateExtension(view, ext); err != nil {
					return nil, err
				}
				slot := slices.Index(ext.Members, d.Address())
				if slot >= len(functions.UUIDOsspMembers) {
					return nil, fmt.Errorf("stuntgres: invalid UUID routine manifest slot")
				}
				member := functions.UUIDOsspMembers[slot]
				if member.Signature == string(d.Signature) && member.Signature != "" {
					element := source.Columns[10].Usage.Type.Element()
					values := []sqltype.Value{sqltype.String(element, member.ArgumentNames[0]), sqltype.String(element, member.ArgumentNames[1])}
					row[10], valueErr = sqltype.OwnedArrayValue(source.Columns[10].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, values)
				}
			}
			if d.Installed.ArgumentNames[0] != "" {
				elements := make([]sqltype.Value, ProcedureArity(d))
				for i := range elements {
					elements[i] = sqltype.String(source.Columns[10].Usage.Type.Element(), d.Installed.ArgumentNames[i])
				}
				row[10], valueErr = sqltype.OwnedArrayValue(source.Columns[10].Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
			}
			returnsSet, estimate := d.Installed.ReturnsSet, float64(d.Installed.Rows)
			if d.Builtin != nil && d.Builtin.ReturnsSet {
				returnsSet, estimate = true, builtinRoutineRows(d.Builtin)
			}
			if ext, ok := view.Extension(d.Extension); ok && d.Implementation != nil && (ext.Name() == "pgrowlocks" || ext.Name() == "intagg" && d.RoutineKind == catalog.FunctionRoutine && d.Signature == "1007" && d.Result == 23) {
				if err := d.Implementation.ValidateExtension(view, ext); err != nil {
					return nil, err
				}
				if !slices.Contains(ext.Members, d.Address()) {
					return nil, fmt.Errorf("stuntgres: nonmember set routine declaration")
				}
				returnsSet, estimate = true, 1000
			}
			boolean(12, returnsSet)
			estimateValue, err := sqltype.Float(source.Columns[13].Usage.Type, estimate)
			if err != nil {
				return nil, err
			}
			row[13] = estimateValue
			variadic, err := ProcedureVariadic(d)
			if err != nil {
				return nil, err
			}
			integer(16, int64(variadic))
		case *catalog.TSTemplate:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			row[3], valueErr = sqltype.RegprocValue(source.Columns[3].Usage.Type, uint32(d.Init.ObjectID))
			if valueErr != nil {
				return nil, valueErr
			}
			row[4], valueErr = sqltype.RegprocValue(source.Columns[4].Usage.Type, uint32(d.Lexize.ObjectID))
		case *catalog.TSDictionary:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			integer(3, int64(d.Owner.ID()))
			integer(4, int64(d.Template.ObjectID))
			if len(d.Options) != 0 {
				text(5, catalog.DictionaryOptionsText(d.Options))
			} else {
				row[5] = sqltype.Null(source.Columns[5].Usage.Type)
			}
		default:
			continue
		}
		if valueErr != nil {
			return nil, valueErr
		}
		rows = append(rows, row)
	}
	if source.Address().ObjectID == ids.ProcedureClassID {
		return appendInternalTypeProcedures(in, rows)
	}
	return rows, nil
}

func ProcedureArity(p *catalog.Procedure) int64 {
	if p.Signature == "" {
		return 0
	}
	return int64(strings.Count(string(p.Signature), ",") + 1)
}

func procedureArgumentVector(ctx context.Context, vector *sqltype.Descriptor, signature catalog.ProcedureSignature) (sqltype.Value, error) {
	text := string(signature)
	count := 0
	if text != "" {
		count = strings.Count(text, ",") + 1
	}
	elements := make([]sqltype.Value, count)
	for i := range elements {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		head, rest, _ := strings.Cut(text, ",")
		n, err := strconv.ParseUint(head, 10, 32)
		if err != nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid routine signature: %w", err)
		}
		elements[i], err = sqltype.OIDValue(vector.Element(), uint32(n))
		if err != nil {
			return sqltype.Value{}, err
		}
		text = rest
	}
	return sqltype.OwnedCatalogVectorValue(vector, elements)
}

// ProcedureVariadic projects declaration metadata, not expanded call operands.
func ProcedureVariadic(p *catalog.Procedure) (ids.OID, error) {
	if p.Builtin != nil {
		return p.Builtin.Variadic, nil
	}
	if p.Aggregate != nil && p.Aggregate.Variadic {
		return 2276, nil // VARIADIC ANY has no array-packing element type.
	}
	if p.RangeConstructor != nil {
		return p.RangeConstructor.Variadic(), nil
	}
	if p.SQL != nil {
		for _, formal := range p.SQL.Formals {
			if formal.Mode != "v" {
				continue
			}
			if formal.Usage.Type == nil || !formal.Usage.Type.IsArray() {
				return 0, fmt.Errorf("stuntgres: invalid variadic routine declaration")
			}
			return ids.OID(formal.Usage.Type.Element().ID()), nil
		}
	}
	return 0, nil
}
