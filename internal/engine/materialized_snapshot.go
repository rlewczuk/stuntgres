package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

const materializedSnapshotMarker = "stuntgres-matview-v1"

type materializedSnapshot struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Populated bool   `json:"populated"`
	Rows      string `json:"rows"`
}

func materializedViewReadError(d *relationDescriptor) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf(`materialized view "%s" has not been populated`, d.name), Hint: "Use the REFRESH MATERIALIZED VIEW command."}
}

func (t *dumpRelation) emitMaterializedSnapshot(ctx context.Context, catalog CatalogRead, emit func(string) error) error {
	var rows strings.Builder
	if err := t.emitRows(ctx, catalog, func(line string) error { rows.WriteString(line); return nil }); err != nil {
		return err
	}
	record := materializedSnapshot{Schema: t.schema, Name: t.name, Populated: t.descriptor.populated, Rows: rows.String()}
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

// All rows and descriptors belong exclusively to Restore's isolated engine.
// No public INSERT execution, authority relaxation or definition evaluation is
// involved. The destination engine still receives one fenced replacement only.
func (e *Engine) importMaterializedSnapshots(ctx context.Context, op *Operation, records []materializedSnapshot) error {
	if len(records) == 0 {
		return nil
	}
	input, _ := sessionPresentation(op.session)
	input.ExactSnapshot = true
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		var bootstrap authorization.RoleDescriptor
		bootstrap, err = auth.roles.Descriptor(auth.state.Authenticated)
		if err == nil && (!bootstrap.Superuser || e.importTargets == nil) {
			err = fmt.Errorf("stuntgres: invalid materialized view import authority")
		}
		auth.state.CurrentUser = bootstrap.Ref
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	type imported struct {
		descriptor *relationDescriptor
		rows       [][]sqltype.Value
		populated  bool
	}
	imports := make([]imported, 0, len(records))
	seen := make(map[ids.ObjectAddress]bool, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !record.Populated && record.Rows != "" {
			return fmt.Errorf("stuntgres: unpopulated snapshot contains rows")
		}
		e.mu.Lock()
		namespace := e.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema}]
		address := e.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.Name}]
		d, ok := e.state.descriptors[address].(*relationDescriptor)
		valid := ok && d.relationKind == materializedViewRelation && !d.populated && !seen[address]
		e.mu.Unlock()
		if !valid {
			return fmt.Errorf("stuntgres: invalid materialized view snapshot target %s.%s", record.Schema, record.Name)
		}
		seen[address] = true
		statements, err := parser.ParseWithOptions(record.Rows, parser.Options{Context: ctx, StandardConformingStrings: true})
		if err != nil {
			return err
		}
		var rows [][]sqltype.Value
		for _, statement := range statements {
			s, ok := statement.(*parser.Insert)
			if !ok || s.Source != nil || len(s.Targets) != 0 || s.Override != 0 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != record.Schema || s.Name.Parts[1].Text != record.Name {
				return fmt.Errorf("stuntgres: invalid materialized view snapshot row statement")
			}
			if s.DefaultValues {
				if len(d.columns) != 0 {
					return fmt.Errorf("stuntgres: snapshot DEFAULT requires zero columns")
				}
				rows = append(rows, []sqltype.Value{})
				continue
			}
			for _, row := range s.Rows {
				if len(row) != len(d.columns) {
					return fmt.Errorf("stuntgres: invalid materialized view snapshot row width")
				}
				for _, raw := range row {
					if err := materializedSnapshotScalar(raw); err != nil {
						return err
					}
				}
			}
			e.mu.Lock()
			b, err := e.describe(ctx, s, auth, e.domain.Ref(auth.catalog.Epoch(), address), input, op, nil, nil)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			if b.parameters.Count != 0 || len(b.querySources) != 0 || len(b.defaults) != 0 {
				return fmt.Errorf("stuntgres: invalid materialized view snapshot row binding")
			}
			environment := input
			environment.Regclass, environment.Regtype = b.regclassInput, b.regtypeInput
			environment.Regrole = b.regroleInput
			environment.Operator = b.operatorInput
			environment.Regproc = b.regprocInput
			environment.Regcollation, environment.Regnamespace = b.regcollationInput, b.regnamespaceInput
			environment.RegTS = b.regTSInput
			environment.ACLItem = b.aclitemInput
			evaluation := EvalContext{Context: ctx, Input: environment}
			for _, expressions := range b.expressions {
				values := make([]sqltype.Value, len(d.columns))
				for i, expression := range expressions {
					value, err := expression.Eval(&evaluation)
					if err != nil {
						return err
					}
					if value.Type() != d.columns[i].Usage.Type {
						return fmt.Errorf("stuntgres: invalid materialized view snapshot value type")
					}
					values[i] = value
				}
				rows = append(rows, values)
			}
		}
		imports = append(imports, imported{d, rows, record.Populated})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := cloneCatalog(e.state)
	for _, item := range imports {
		versions, err := e.allocateRowVersionsLocked(item.rows, nil)
		if err != nil {
			return err
		}
		for i := range versions {
			versions[i].born = e.commitSeq
		}
		d := *item.descriptor
		d.revision, err = e.revisionLocked()
		if err != nil {
			return err
		}
		d.populated = item.populated
		next.descriptors[d.address] = &d
		e.storage[d.address] = &relationStorage{versions: versions}
	}
	e.state = next
	return nil
}
