package pgcatalog

import (
	"fmt"
	"slices"
	"sort"
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
	descriptors := make([]catalog.Descriptor, 0)
	for _, d := range view.Descriptors() {
		if d.Address().ClassID == source.Address().ObjectID {
			descriptors = append(descriptors, d)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(descriptors))
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
			case catalog.InternalRoutineLanguage: language = "internal"
			case catalog.CRoutineLanguage: language = "c"
			case catalog.SQLRoutineLanguage: language = "sql"
			case catalog.PLpgSQLRoutineLanguage: language = "plpgsql"
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
			text(8, parallel)
			text(9, string(d.RoutineKind))
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
		case *catalog.TSTemplate:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
		case *catalog.TSDictionary:
			integer(0, int64(d.Address().ObjectID))
			text(1, d.Name())
			integer(2, int64(d.Namespace().ObjectID))
			integer(3, int64(d.Owner.ID()))
			integer(4, int64(d.Template.ObjectID))
			text(5, "rules = 'unaccent'")
		default:
			continue
		}
		if valueErr != nil {
			return nil, valueErr
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func ProcedureArity(p *catalog.Procedure) int64 {
	if p.Signature == "" {
		return 0
	}
	return int64(strings.Count(string(p.Signature), ",") + 1)
}
