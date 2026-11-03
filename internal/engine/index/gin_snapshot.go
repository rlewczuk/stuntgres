package index

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// Native metadata identifies rows in the relational INSERT stream, never by a
// source OID, row/version allocation, rendered key, or physical replay order.
type GinDumpIndex struct {
	Schema       string `json:"schema"`
	Name         string `json:"name"`
	ParentSchema string `json:"parent_schema"`
	Parent       string `json:"parent"`
	Pending      []int  `json:"pending"`
}
type GinDumpRow struct {
	Schema       string `json:"schema"`
	Table        string `json:"table"`
	Ordinal      int    `json:"ordinal"`
	ReplaySchema string `json:"replay_schema"`
	ReplayTable  string `json:"replay_table"`
}
type PositionedGinRow struct {
	Record GinDumpRow
	End    int
}

func EncodeGinIndex(record GinDumpIndex) (string, error) { return encodeGinMetadata("index", record) }
func EncodeGinRow(record GinDumpRow) (string, error)     { return encodeGinMetadata("row", record) }
func encodeGinMetadata(kind string, record any) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- stuntgres-gin-" + kind + "-v1 " + base64.StdEncoding.EncodeToString(data) + "\n", nil
}
func decodeGinMetadata(comment, kind string, destination any) (bool, error) {
	text := strings.TrimSpace(comment)
	prefix := "stuntgres-gin-" + kind + "-"
	if !strings.HasPrefix(text, prefix) {
		return false, nil
	}
	invalid := func() (bool, error) { return true, fmt.Errorf("stuntgres: invalid native GIN %s metadata", kind) }
	marker, payload, ok := strings.Cut(text, " ")
	if !ok || marker != prefix+"v1" {
		return invalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return invalid()
	}
	// Flat typed objects: reject duplicate/NULL keys rather than last-key-wins.
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := make(map[string]bool)
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || fields[key] {
			return invalid()
		}
		fields[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil || bytes.Equal(value, []byte("null")) {
			return invalid()
		}
	}
	if _, err := d.Token(); err != nil {
		return invalid()
	}
	if _, err := d.Token(); err != io.EOF || len(fields) != 5 {
		return invalid()
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(destination); err != nil {
		return invalid()
	}
	return true, nil
}
func DecodeGinIndex(comment string) (GinDumpIndex, bool, error) {
	var record GinDumpIndex
	found, err := decodeGinMetadata(comment, "index", &record)
	if err == nil && found {
		if record.Schema == "" || record.Name == "" || record.ParentSchema == "" || record.Parent == "" || record.Pending == nil {
			return record, true, fmt.Errorf("stuntgres: invalid native GIN index metadata")
		}
		previous := 0
		for _, ordinal := range record.Pending {
			if ordinal <= previous {
				return record, true, fmt.Errorf("stuntgres: invalid native GIN pending ordinal")
			}
			previous = ordinal
		}
	}
	return record, found, err
}
func DecodeGinRow(comment string) (GinDumpRow, bool, error) {
	var record GinDumpRow
	found, err := decodeGinMetadata(comment, "row", &record)
	if err == nil && found && (record.Schema == "" || record.Table == "" || record.Ordinal <= 0 || record.ReplaySchema == "" || record.ReplayTable == "") {
		err = fmt.Errorf("stuntgres: invalid native GIN row metadata")
	}
	return record, found, err
}
func PairGinRows(script string, statements []parser.Statement, records []PositionedGinRow) (map[*parser.Insert]GinDumpRow, error) {
	pairs := make(map[*parser.Insert]GinDumpRow, len(records))
	cursor := 0
	for _, entry := range records {
		for cursor < len(statements) {
			insert, ok := statements[cursor].(*parser.Insert)
			if ok && insert.Pos.Offset >= entry.End {
				break
			}
			cursor++
		}
		if cursor == len(statements) || entry.End < 0 || entry.End > len(script) {
			return nil, fmt.Errorf("stuntgres: unpaired native GIN row")
		}
		insert := statements[cursor].(*parser.Insert)
		// Other native row annotations may sit between this record and INSERT.
		between, err := parser.Parse(script[entry.End:insert.Pos.Offset])
		if err != nil || len(between) != 0 || pairs[insert].Ordinal != 0 || len(insert.Name.Parts) != 2 || insert.Name.Parts[0].Text != entry.Record.ReplaySchema || insert.Name.Parts[1].Text != entry.Record.ReplayTable || insert.Source != nil || len(insert.With) != 0 || insert.Conflict != nil || insert.Returning != nil || (!insert.DefaultValues && len(insert.Rows) != 1) {
			return nil, fmt.Errorf("stuntgres: invalid native GIN row pairing")
		}
		pairs[insert] = entry.Record
	}
	return pairs, nil
}

// CaptureGinSplit rejects mixed/inconsistent per-column placement and captures
// only live dump-visible members. Category tokens make even NULL/empty members
// genuine postings in every column.
func CaptureGinSplit(ctx context.Context, view catalog.Read, target *catalog.Relation, root *storage.IndexRoot, store *storage.Relation, frontier ids.CommitSeq) (GinDumpIndex, error) {
	parent, ok := view.Relation(target.SQLIndex.Parent)
	if !ok || root == nil || root.Gin == nil || store == nil {
		return GinDumpIndex{}, ids.ErrCatalogReplaced
	}
	bindings, err := BindGinIndex(view, parent, target.SQLIndex)
	if err != nil {
		return GinDumpIndex{}, err
	}
	record := GinDumpIndex{Schema: view.Get(target.Namespace()).Name(), Name: target.Name(), ParentSchema: view.Get(parent.Namespace()).Name(), Parent: parent.Name(), Pending: []int{}}
	placements := make(map[RowIdentity]bool, len(root.Entries))
	members := make(map[RowIdentity]int, len(root.Entries))
	for _, entry := range root.Entries {
		identity := RowIdentity{entry.Row, entry.Version}
		if _, exists := members[identity]; exists || entry.Position < 0 || entry.Position >= len(store.Versions) || store.Versions[entry.Position].Row != entry.Row || store.Versions[entry.Position].Version != entry.Version {
			return record, fmt.Errorf("stuntgres: inconsistent GIN membership")
		}
		members[identity] = entry.Position
	}
	for portion, columns := range [2][]storage.GinColumn{root.Gin.Columns, root.Gin.Pending} {
		if len(columns) == 0 && portion == 1 {
			continue
		}
		if len(columns) != len(bindings) {
			return record, fmt.Errorf("stuntgres: inconsistent GIN split columns")
		}
		var portionMembers map[RowIdentity]bool
		for ordinal, column := range columns {
			if !column.Binding.Same(bindings[ordinal]) {
				return record, ids.ErrCatalogReplaced
			}
			seen := make(map[RowIdentity]bool)
			for _, token := range column.Tokens {
				for _, p := range token.Postings {
					if err := ctx.Err(); err != nil {
						return record, err
					}
					identity := RowIdentity{p.Row, p.Version}
					position, exists := members[identity]
					if !exists || p.Position != position {
						return record, fmt.Errorf("stuntgres: GIN posting without membership")
					}
					if ordinal == 0 {
						if previous, exists := placements[identity]; exists && previous != (portion == 1) {
							return record, fmt.Errorf("stuntgres: mixed GIN placement")
						}
						placements[identity] = portion == 1
					} else if !portionMembers[identity] {
						return record, fmt.Errorf("stuntgres: inconsistent GIN split column")
					}
					seen[identity] = true
				}
			}
			if ordinal == 0 {
				portionMembers = seen
			} else if len(seen) != len(portionMembers) {
				return record, fmt.Errorf("stuntgres: incomplete GIN split column")
			}
			for _, p := range column.NonNull {
				if !seen[RowIdentity{p.Row, p.Version}] || members[RowIdentity{p.Row, p.Version}] != p.Position {
					return record, fmt.Errorf("stuntgres: inconsistent GIN nonnull placement")
				}
			}
		}
	}
	// Keep raw source positions above for posting validation; replay ordinals
	// instead follow relational export's captured-header/Additional projection.
	rows, ends := store.Versions, store.Ends
	if store.Headers != nil {
		rows, ends = storage.VisibleHeapVersions(store, frontier), nil
	}
	ordinal := 0
	for _, row := range rows {
		if !visibility.LiveVersion(row, frontier, ends, nil) {
			continue
		}
		ordinal++
		identity := RowIdentity{row.Row, row.Version}
		pending, exists := placements[identity]
		if _, member := members[identity]; member && !exists {
			return record, fmt.Errorf("stuntgres: missing GIN member postings")
		}
		if pending {
			record.Pending = append(record.Pending, ordinal)
		}
	}
	return record, ctx.Err()
}

// RestoreGinSplit consumes destination-produced typed keys after ordinary DDL
// validation; metadata does not execute recipes or bypass support admission.
func RestoreGinSplit(ctx context.Context, view catalog.Read, target *catalog.Relation, root *storage.IndexRoot, pending map[RowIdentity]bool, input sqltype.InputEnvironment) (*storage.IndexRoot, error) {
	parent, ok := view.Relation(target.SQLIndex.Parent)
	if !ok || target.SQLIndex.Method != catalog.GinMethod || root == nil || root.Gin == nil {
		return nil, ids.ErrCatalogReplaced
	}
	bindings, err := BindGinIndex(view, parent, target.SQLIndex)
	if err != nil {
		return nil, err
	}
	if len(pending) > len(root.Entries) {
		return nil, fmt.Errorf("stuntgres: native GIN ordinal is not an index member")
	}
	main := make([]storage.IndexEntry, 0, len(root.Entries)-len(pending))
	deferred := make([]storage.IndexEntry, 0, len(pending))
	found := 0
	for _, entry := range root.Entries {
		if pending[RowIdentity{entry.Row, entry.Version}] {
			deferred = append(deferred, entry)
			found++
		} else {
			main = append(main, entry)
		}
	}
	if found != len(pending) {
		return nil, fmt.Errorf("stuntgres: native GIN ordinal is not an index member")
	}
	payload, err := storage.BuildGin(ctx, bindings, main, input)
	if err != nil {
		return nil, err
	}
	if len(deferred) != 0 {
		part, err := storage.BuildGin(ctx, bindings, deferred, input)
		if err != nil {
			return nil, err
		}
		payload.Pending = part.Columns
	}
	next := *root
	next.Gin = payload
	return &next, ctx.Err()
}
