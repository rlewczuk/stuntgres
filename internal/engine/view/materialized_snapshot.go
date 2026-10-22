package view

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
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const MaterializedSnapshotMarker = "stuntgres-matview-v1"

type MaterializedSnapshot struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Populated bool   `json:"populated"`
	Rows      string `json:"rows"`
}

func MaterializedViewReadError(d *catalog.Relation) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`materialized view "%s" has not been populated`, d.Name()), Hint: "Use the REFRESH MATERIALIZED VIEW command."}
}

// EmitMaterializedSnapshot writes the materialized view t's snapshot record:
// its population flag and the rows its table dump emits.
func EmitMaterializedSnapshot(ctx context.Context, t *table.DumpRelation, catalog catalog.Read, emit func(string) error) error {
	var rows strings.Builder
	if err := t.EmitRows(ctx, catalog, func(line string) error { rows.WriteString(line); return nil }); err != nil {
		return err
	}
	record := MaterializedSnapshot{Schema: t.Schema, Name: t.Name, Populated: t.Descriptor.Populated, Rows: rows.String()}
	if !record.Populated && record.Rows != "" {
		return fmt.Errorf("stuntgres: unpopulated materialized view has stored rows")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return emit("-- " + MaterializedSnapshotMarker + " " + base64.StdEncoding.EncodeToString(data) + "\n")
}

// Called only for genuine line comments by Restore's opt-in lexer callback.
// Strict keys also reject duplicates/null fields, not merely unknown fields.
func DecodeMaterializedSnapshot(comment string) (MaterializedSnapshot, bool, error) {
	var record MaterializedSnapshot
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-matview-") {
		return record, false, nil
	}
	formatError := func() (MaterializedSnapshot, bool, error) {
		return record, true, fmt.Errorf("stuntgres: invalid materialized view snapshot")
	}
	marker, payload, found := strings.Cut(text, " ")
	if !found || marker != MaterializedSnapshotMarker || payload == "" {
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

// ParseMaterializedSnapshot parses a snapshot's row statements and pairs each
// INSERT with the domain replay record of its line comment, if any.
func ParseMaterializedSnapshot(ctx context.Context, rows string) ([]parser.Statement, map[*parser.Insert]typedef.DomainReplayRecord, error) {
	var domainReplays []typedef.PositionedDomainReplay
	statements, err := parser.ParseWithOptions(rows, parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
		record, recognized, err := typedef.DecodeDomainReplay(comment)
		if err == nil && recognized {
			domainReplays = append(domainReplays, typedef.PositionedDomainReplay{Record: record, End: position.Offset + len(comment)})
		}
		return err
	}})
	if err != nil {
		return nil, nil, err
	}
	pairs, err := typedef.PairDomainReplay(rows, statements, domainReplays)
	if err != nil {
		return nil, nil, err
	}
	return statements, pairs, nil
}

// MaterializedSnapshotInsert checks that statement is a plain INSERT into
// record's materialized view d: DEFAULT VALUES only for zero columns, every
// row as wide as d, and every cell a closed snapshot scalar.
func MaterializedSnapshotInsert(record MaterializedSnapshot, d *catalog.Relation, statement parser.Statement) (*parser.Insert, error) {
	s, ok := statement.(*parser.Insert)
	if !ok || s.Source != nil || len(s.Targets) != 0 || s.Override != 0 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != record.Schema || s.Name.Parts[1].Text != record.Name {
		return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot row statement")
	}
	if s.DefaultValues {
		if len(d.Columns) != 0 {
			return nil, fmt.Errorf("stuntgres: snapshot DEFAULT requires zero columns")
		}
		return s, nil
	}
	for _, row := range s.Rows {
		if len(row) != len(d.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot row width")
		}
		for _, raw := range row {
			if err := MaterializedSnapshotScalar(raw); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// The payload admits only closed canonical codec forms, not Restore's wider
// scalar whitelist (which includes volatile clocks, identity and DEFAULT).
func MaterializedSnapshotScalar(raw parser.Expr) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid materialized view snapshot scalar") }
	switch r := raw.(type) {
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		return nil
	case parser.CastExpr:
		if row, ok := r.Child.(parser.RowExpr); ok {
			// Lossless composite literals use a destination-owned qualified cast
			// over closed typed members. The ordinary binder verifies that the
			// target is a real composite and checks each member's usage.
			if r.TypeName.Schema == nil || len(r.TypeName.Modifiers) != 0 || len(r.TypeName.ArrayBounds) != 0 || r.TypeName.Setof {
				return invalid()
			}
			for _, child := range row.Fields {
				if err := MaterializedSnapshotScalar(child); err != nil {
					return err
				}
			}
			return nil
		}
		return MaterializedSnapshotScalar(r.Child)
	case parser.RegclassExpr:
		return MaterializedSnapshotScalar(r.Child)
	case parser.ArrayExpr:
		for _, child := range r.Elements {
			if err := MaterializedSnapshotScalar(child); err != nil {
				return err
			}
		}
		return nil
	case parser.OperatorExpr:
		if len(r.Name.Parts) == 1 && r.Name.Parts[0].Text == "||" && len(r.Args) == 2 {
			// Routine-containing containers emit closed TEXT concatenation
			// around qualified alias-to-OID casts, not arbitrary execution.
			for _, child := range r.Args {
				if err := MaterializedSnapshotScalar(child); err != nil {
					return err
				}
			}
			return nil
		}
		if len(r.Name.Parts) != 1 || len(r.Args) != 1 || (r.Name.Parts[0].Text != "-" && r.Name.Parts[0].Text != "+") {
			return invalid()
		}
		return MaterializedSnapshotScalar(r.Args[0])
	case parser.FunctionExpr:
		if functions.CircleConstructorReplay(r) {
			return nil
		}
		// Preserve the existing exact TIMETZ endpoint codec below.
		if len(r.Name.Parts) != 2 || r.Name.Parts[0].Text != "pg_catalog" || r.Name.Parts[1].Text != "timezone" || len(r.Args) != 2 || r.Variadic {
			return invalid()
		}
		for _, child := range r.Args {
			if err := MaterializedSnapshotScalar(child); err != nil {
				return err
			}
		}
		return nil
	default:
		return invalid()
	}
}
