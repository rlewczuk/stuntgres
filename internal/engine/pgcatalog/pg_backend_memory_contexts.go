package pgcatalog

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const BackendMemoryContextsOID ids.OID = 12142

// BackendMemorySnapshot contains detached own-layout measurements only. Count
// distinguishes a real registry attachment from a session with no registry.
// No owner, definition, allocator or mutable descriptor pointers cross capture.
type BackendMemorySnapshot struct {
	Own   [3]int64
	Count int
}

var backendMemoryColumns = []ColumnSpec{
	{"name", sqltype.Text, true, 0, 0, -1},
	{"ident", sqltype.Text, true, 0, 0, -1},
	{"type", sqltype.Text, true, 0, 0, -1},
	{"level", sqltype.Int4, true, 0, 0, -1},
	{"path", 1007, true, 0, 0, -1},
	{"total_bytes", sqltype.Int8, true, 0, 0, -1},
	{"total_nblocks", sqltype.Int8, true, 0, 0, -1},
	{"free_bytes", sqltype.Int8, true, 0, 0, -1},
	{"free_chunks", sqltype.Int8, true, 0, 0, -1},
	{"used_bytes", sqltype.Int8, true, 0, 0, -1},
}

func capturePGBackendMemoryContexts(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, backendMemoryColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != BackendMemoryContextsOID {
		return nil, fmt.Errorf("stuntgres: unregistered backend memory source")
	}
	var types [10]*sqltype.Descriptor
	for i, expected := range backendMemoryColumns {
		actual := in.Source.Columns[i]
		collation := uint32(0)
		if expected.Type == sqltype.Text {
			collation = 100
		}
		if actual.Usage.Typmod != -1 || actual.Usage.Collation.ID != collation || actual.DeclaredDimensions != 0 || !actual.Nullable || !actual.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid backend memory source attribute")
		}
		types[i] = actual.Usage.Type
	}
	return BackendMemoryRows(in.Context, types, in.BackendMemory)
}

// BackendMemoryRows is shared by the view and callable SRF. Descriptors belong
// to the executing catalog/result generation; captures never carry source types.
func BackendMemoryRows(ctx context.Context, types [10]*sqltype.Descriptor, snapshot BackendMemorySnapshot) ([][]sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot.Count != 1 && snapshot.Count != 3 || snapshot.Own[0] <= 0 || snapshot.Own[1] < 0 || snapshot.Own[2] < 0 || snapshot.Count == 3 && snapshot.Own[1] == 0 || snapshot.Count == 1 && (snapshot.Own[1] != 0 || snapshot.Own[2] != 0) {
		return nil, fmt.Errorf("stuntgres: invalid backend memory capture")
	}
	for i, expected := range backendMemoryColumns {
		if types[i] == nil || types[i].ID() != expected.Type {
			return nil, fmt.Errorf("stuntgres: invalid backend memory result type")
		}
	}
	if !types[4].IsArray() || types[4].Element() != types[3] {
		return nil, fmt.Errorf("stuntgres: foreign backend memory path element")
	}
	names := [...]string{"TopMemoryContext", "PreparedStatements", "PreparedDefinitions"}
	rows := make([][]sqltype.Value, snapshot.Count)
	values := make([]sqltype.Value, snapshot.Count*10)
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*10 : (i+1)*10 : (i+1)*10]
		row[0] = sqltype.String(types[0], names[i])
		row[1] = sqltype.Null(types[1])
		row[2] = sqltype.String(types[2], "GoObjectLayout")
		row[3], _ = sqltype.Integer(types[3], int64(i+1))
		elements := make([]sqltype.Value, i+1)
		for j := range elements {
			elements[j], _ = sqltype.Integer(types[3], int64(j+1))
		}
		path, err := sqltype.OwnedArrayValue(types[4], []sqltype.ArrayDimension{{Lower: 1, Length: int32(i+1)}}, elements)
		if err != nil {
			return nil, err
		}
		row[4] = path
		row[5], _ = sqltype.Integer(types[5], snapshot.Own[i])
		row[6], row[7], row[8] = sqltype.Null(types[6]), sqltype.Null(types[7]), sqltype.Null(types[8])
		row[9], _ = sqltype.Integer(types[9], snapshot.Own[i])
		rows[i] = row
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

var backendMemoryViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range backendMemoryColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_get_backend_memory_contexts", ""), viewText("() "), viewIdentifier("pg_get_backend_memory_contexts"), viewText("("))
	for i, column := range backendMemoryColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
