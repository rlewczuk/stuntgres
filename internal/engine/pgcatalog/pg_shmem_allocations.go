package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const (
	ShmemAllocationsOID     ids.OID = 12134
	ShmemAllocationsNUMAOID ids.OID = 12138

	// Virtual accounting only: no segment, heap sampling or host NUMA inquiry.
	simulatedShmemName           = "stuntgres simulated arena"
	simulatedSegment      int64 = 1048576
	simulatedHeader       int64 = 4096
	simulatedRequest      int64 = 65537
	simulatedAlignment    int64 = 64
	simulatedPage         int64 = 4096
	simulatedAllocated          = (simulatedRequest + simulatedAlignment - 1) / simulatedAlignment * simulatedAlignment
	simulatedFreeOffset         = simulatedHeader + simulatedAllocated
	simulatedFree               = simulatedSegment - simulatedFreeOffset
	simulatedCoverage           = (simulatedFreeOffset + simulatedPage - 1) / simulatedPage * simulatedPage - simulatedHeader
)

func capturePGShmemAllocations(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil ||
		source.Address().ClassID != ids.RelationClassID ||
		(source.Address().ObjectID != ShmemAllocationsOID && source.Address().ObjectID != ShmemAllocationsNUMAOID) ||
		source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() ||
		in.Ref.Address != source.Address() || view.Get(in.Ref.Address) != source || !source.VirtualView {
		return nil, fmt.Errorf("stuntgres: invalid shared-memory source authority")
	}
	if in.Roles != (authorization.Read{}) && !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign shared-memory role authority")
	}
	numa := source.Address().ObjectID == ShmemAllocationsNUMAOID
	names := []string{"name", "off", "size", "allocated_size"}
	types := []sqltype.ID{sqltype.Text, sqltype.Int8, sqltype.Int8, sqltype.Int8}
	count, width := 3, 4
	if numa {
		names = []string{"name", "numa_node", "size"}
		types = []sqltype.ID{sqltype.Text, sqltype.Int4, sqltype.Int8}
		count, width = 2, 3
	}
	if len(source.Columns) != width {
		return nil, fmt.Errorf("stuntgres: invalid shared-memory source width")
	}
	for i, name := range names {
		typ, ok := view.LookupType(types[i])
		if !ok || source.Columns[i].Name != name || source.Columns[i].Usage.Type != typ || !source.Columns[i].Nullable {
			return nil, fmt.Errorf("stuntgres: invalid shared-memory source column")
		}
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*width)
	for i := range rows {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := values[i*width : (i+1)*width : (i+1)*width]
		if numa || i == 0 {
			row[0] = sqltype.String(source.Columns[0].Usage.Type, simulatedShmemName)
		} else if i == 1 {
			row[0] = sqltype.String(source.Columns[0].Usage.Type, "<anonymous>")
		} else {
			row[0] = sqltype.Null(source.Columns[0].Usage.Type)
		}
		// Integer constructors cannot fail: descriptor identity was checked above
		// and every virtual constant fits its declared INT4/INT8 carrier.
		if numa {
			if i == 0 {
				row[1], _ = sqltype.Integer(source.Columns[1].Usage.Type, 0)
				row[2], _ = sqltype.Integer(source.Columns[2].Usage.Type, simulatedCoverage)
			} else {
				row[1] = sqltype.Null(source.Columns[1].Usage.Type)
				row[2], _ = sqltype.Integer(source.Columns[2].Usage.Type, 0)
			}
		} else {
			off, size, allocated := simulatedHeader, simulatedRequest, simulatedAllocated
			if i == 1 {
				size, allocated = simulatedHeader, simulatedHeader
			}
			if i == 2 {
				off, size, allocated = simulatedFreeOffset, simulatedFree, simulatedFree
			}
			if i != 1 {
				row[1], _ = sqltype.Integer(source.Columns[1].Usage.Type, off)
			} else {
				row[1] = sqltype.Null(source.Columns[1].Usage.Type)
			}
			row[2], _ = sqltype.Integer(source.Columns[2].Usage.Type, size)
			row[3], _ = sqltype.Integer(source.Columns[3].Usage.Type, allocated)
		}
		rows[i] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
