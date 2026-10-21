package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const materializedSnapshotMarker = "stuntgres-matview-v1"

type materializedSnapshot struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Populated bool   `json:"populated"`
	Rows      string `json:"rows"`
}

func materializedViewReadError(d *catalog.Relation) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`materialized view "%s" has not been populated`, d.Name()), Hint: "Use the REFRESH MATERIALIZED VIEW command."}
}

// emitMaterializedSnapshot writes the materialized view t's snapshot record:
// its population flag and the rows its table dump emits.
func emitMaterializedSnapshot(ctx context.Context, t *table.DumpRelation, catalog catalog.Read, emit func(string) error) error {
	var rows strings.Builder
	if err := t.EmitRows(ctx, catalog, func(line string) error { rows.WriteString(line); return nil }); err != nil {
		return err
	}
	record := materializedSnapshot{Schema: t.Schema, Name: t.Name, Populated: t.Descriptor.Populated, Rows: rows.String()}
	if !record.Populated && record.Rows != "" {
		return fmt.Errorf("stuntgres: unpopulated materialized view has stored rows")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return emit("-- " + materializedSnapshotMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n")
}

// Called only for genuine line comments by Restore's opt-in lexer callback.
// Strict keys also reject duplicates/null fields, not merely unknown fields.
func decodeMaterializedSnapshot(comment string) (materializedSnapshot, bool, error) {
	var record materializedSnapshot
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-matview-") {
		return record, false, nil
	}
	formatError := func() (materializedSnapshot, bool, error) {
		return record, true, fmt.Errorf("stuntgres: invalid materialized view snapshot")
	}
	marker, payload, found := strings.Cut(text, " ")
	if !found || marker != materializedSnapshotMarker || payload == "" {
		return formatError()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || !utf8.Valid(data) {
		return formatError()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return formatError()
	}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return formatError()
		}
		seen[key] = true
		switch key {
		case "schema", "name", "rows":
			var value *string
			if err := decoder.Decode(&value); err != nil || value == nil {
				return formatError()
			}
			switch key {
			case "schema":
				record.Schema = *value
			case "name":
				record.Name = *value
			case "rows":
				record.Rows = *value
			}
		case "populated":
			var value *bool
			if err := decoder.Decode(&value); err != nil || value == nil {
				return formatError()
			}
			record.Populated = *value
		default:
			return formatError()
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 4 || record.Schema == "" || record.Name == "" {
		return formatError()
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return formatError()
	}
	if !record.Populated && record.Rows != "" {
		return formatError()
	}
	return record, true, nil
}

// The payload admits only closed canonical codec forms, not Restore's wider
// scalar whitelist (which includes volatile clocks, identity and DEFAULT).
func materializedSnapshotScalar(raw parser.Expr) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid materialized view snapshot scalar") }
	switch r := raw.(type) {
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		return nil
	case parser.CastExpr:
		return materializedSnapshotScalar(r.Child)
	case parser.RegclassExpr:
		return materializedSnapshotScalar(r.Child)
	case parser.ArrayExpr:
		for _, child := range r.Elements {
			if err := materializedSnapshotScalar(child); err != nil {
				return err
			}
		}
		return nil
	case parser.OperatorExpr:
		if len(r.Name.Parts) == 1 && r.Name.Parts[0].Text == "||" && len(r.Args) == 2 {
			// Routine-containing containers emit closed TEXT concatenation
			// around qualified alias-to-OID casts, not arbitrary execution.
			for _, child := range r.Args {
				if err := materializedSnapshotScalar(child); err != nil {
					return err
				}
			}
			return nil
		}
		if len(r.Name.Parts) != 1 || len(r.Args) != 1 || (r.Name.Parts[0].Text != "-" && r.Name.Parts[0].Text != "+") {
			return invalid()
		}
		return materializedSnapshotScalar(r.Args[0])
	case parser.FunctionExpr:
		// The exact TIMETZ endpoint codec is the only emitted function form.
		if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || r.Name.Parts[1].Text != "timezone" || len(r.Args) != 2 || r.Variadic {
			return invalid()
		}
		for _, child := range r.Args {
			if err := materializedSnapshotScalar(child); err != nil {
				return err
			}
		}
		return nil
	default:
		return invalid()
	}
}
