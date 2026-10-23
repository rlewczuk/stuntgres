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
// row as wide as d, and every cell a closed snapshot scalar. Constructor bounds
// may name all columns in destination order, with closed integer slices only.
func MaterializedSnapshotInsert(record MaterializedSnapshot, d *catalog.Relation, statement parser.Statement) (*parser.Insert, error) {
	s, ok := statement.(*parser.Insert)
	if !ok || s.Source != nil || s.Override != 0 || len(s.With) != 0 || s.Alias.Text != "" || s.Conflict != nil || s.Returning != nil || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != record.Schema || s.Name.Parts[1].Text != record.Name {
		return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot row statement")
	}
	if len(s.Targets) != 0 {
		if s.DefaultValues || len(s.Targets) != len(d.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot targets")
		}
		bounded := false
		for i, target := range s.Targets {
			if target.Name != d.Columns[i].Name || len(target.Steps) > 6 {
				return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot targets")
			}
			for _, step := range target.Steps {
				if step.Field != nil || step.Subscript == nil || !step.Subscript.Slice || !materializedSnapshotInteger(step.Subscript.Lower) || !materializedSnapshotInteger(step.Subscript.Upper) {
					return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot bounds")
				}
				bounded = true
			}
		}
		if !bounded {
			return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot targets")
		}
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

func materializedSnapshotInteger(raw parser.Expr) bool {
	switch r := raw.(type) {
	case parser.LiteralExpr:
		return r.Value.Kind == parser.IntegerValue
	case parser.OperatorExpr:
		return len(r.Name.Parts) == 1 && (r.Name.Parts[0].Text == "-" || r.Name.Parts[0].Text == "+") && len(r.Args) == 1 && materializedSnapshotInteger(r.Args[0])
	default:
		return false
	}
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
		if materializedSnapshotArrayCall(r, "array_fill", 3) {
			if !materializedSnapshotArraySeed(r) {
				return invalid()
			}
			return MaterializedSnapshotScalar(r.Args[0])
		}
		if materializedSnapshotArrayCall(r, "array_cat", 2) {
			seed, ok := r.Args[0].(parser.FunctionExpr)
			if !ok || !materializedSnapshotArraySeed(seed) {
				return invalid()
			}
			tail, ok := r.Args[1].(parser.CastExpr)
			if !ok || tail.TypeName.Schema == nil || tail.TypeName.Setof || len(tail.TypeName.Modifiers) != 0 || len(tail.TypeName.ArrayBounds) != 1 || tail.TypeName.ArrayBounds[0] != 0 {
				return invalid()
			}
			elements, ok := tail.Child.(parser.ArrayExpr)
			if !ok || len(elements.Elements) == 0 {
				return invalid()
			}
			// Syntax grants no callable or type authority. The destination's
			// normal binder selects the real providers and checks both operands,
			// then exact snapshot evaluation preserves their usages and bounds.
			if err := MaterializedSnapshotScalar(seed.Args[0]); err != nil {
				return err
			}
			for _, child := range elements.Elements {
				if _, nested := child.(parser.ArrayExpr); nested {
					return invalid()
				}
				if err := MaterializedSnapshotScalar(child); err != nil {
					return err
				}
			}
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

// These are the two rank-one forms emitted by regobj's constructor codec, not
// a list of arbitrary functions safe to execute during snapshot import.
func materializedSnapshotArrayCall(r parser.FunctionExpr, name string, arity int) bool {
	return len(r.Name.Parts) == 2 && r.Name.Parts[0].Text == "pg_catalog" && r.Name.Parts[1].Text == name && len(r.Args) == arity &&
		len(r.ArgNames) == 0 && !r.Variadic && !r.ResolutionOnly && !r.CompositeCandidate && !r.StoredSQLRoutine &&
		r.Syntax == parser.OrdinaryFunctionSyntax && r.StringSyntax == 0 && r.Window == nil && !r.Star && !r.Distinct && !r.All &&
		len(r.Order) == 0 && r.Filter == nil && !r.Within && len(r.ColumnDefinitions) == 0
}

func materializedSnapshotArraySeed(r parser.FunctionExpr) bool {
	if !materializedSnapshotArrayCall(r, "array_fill", 3) {
		return false
	}
	dimensions, ok := r.Args[1].(parser.ArrayExpr)
	if !ok || len(dimensions.Elements) != 1 {
		return false
	}
	length, ok := dimensions.Elements[0].(parser.LiteralExpr)
	if !ok || length.Value.Kind != parser.IntegerValue || length.Value.Integer != 1 {
		return false
	}
	bounds, ok := r.Args[2].(parser.ArrayExpr)
	if !ok || len(bounds.Elements) != 1 {
		return false
	}
	// The parser folds the emitter's signed numeric literal, including the
	// int32 minimum. Expressions over bounds are not part of this codec.
	value, ok := bounds.Elements[0].(parser.LiteralExpr)
	return ok && value.Value.Kind == parser.IntegerValue && value.Value.Integer >= -2147483648 && value.Value.Integer <= 2147483647
}
