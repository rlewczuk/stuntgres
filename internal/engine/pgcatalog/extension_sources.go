package pgcatalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
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
		expected := procedureColumnTypes[:]
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
	rows := make([][]sqltype.Value, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if procedure, ok := descriptor.(*catalog.Procedure); ok {
			if in.Keep.Rejects(ProcedureOIDColumn, func() (sqltype.Value, error) {
				return sqltype.OIDValue(source.Columns[ProcedureOIDColumn].Usage.Type, uint32(procedure.Address().ObjectID))
			}) {
				continue
			}
			row, err := procedureRow(in, procedure)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
			continue
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
			boolean(4, view.Owner().Extensions[d.Name()].Control().Relocatable)
			text(5, d.Version)
			row[6] = sqltype.Null(source.Columns[6].Usage.Type)
			row[7] = sqltype.Null(source.Columns[7].Usage.Type)
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
	return rows, nil
}

func ProcedureArity(p *catalog.Procedure) int64 {
	if p.Signature == "" {
		return 0
	}
	return int64(strings.Count(string(p.Signature), ",") + 1)
}

func procedureDefaultCount(p *catalog.Procedure) int64 {
	if p.SQL != nil {
		return int64(p.SQL.DefaultCount())
	}
	if p.Builtin != nil {
		return int64(p.Builtin.Defaults)
	}
	return int64(p.Installed.Defaults)
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
