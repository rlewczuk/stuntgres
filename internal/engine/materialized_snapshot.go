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

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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

func (t *dumpRelation) emitMaterializedSnapshot(ctx context.Context, catalog catalog.Read, emit func(string) error) error {
	var rows strings.Builder
	if err := t.emitRows(ctx, catalog, func(line string) error { rows.WriteString(line); return nil }); err != nil {
		return err
	}
	record := materializedSnapshot{Schema: t.schema, Name: t.name, Populated: t.descriptor.Populated, Rows: rows.String()}
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
	input, _ := sessionPresentation(op.Session)
	input.ExactSnapshot = true
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		var bootstrap authorization.RoleDescriptor
		bootstrap, err = auth.Roles.Descriptor(auth.State.Authenticated)
		if err == nil && (!bootstrap.Superuser || e.importTargets == nil) {
			err = fmt.Errorf("stuntgres: invalid materialized view import authority")
		}
		auth.State.CurrentUser = bootstrap.Ref
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	type imported struct {
		descriptor *catalog.Relation
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
		namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema})
		address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.Name})
		d, ok := e.catalog.state.Relation(address)
		valid := ok && d.RelationKind == catalog.MaterializedViewRelation && !d.Populated && !seen[address]
		e.mu.Unlock()
		if !valid {
			return fmt.Errorf("stuntgres: invalid materialized view snapshot target %s.%s", record.Schema, record.Name)
		}
		seen[address] = true
		var domainReplays []typedef.PositionedDomainReplay
		statements, err := parser.ParseWithOptions(record.Rows, parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
			record, recognized, err := typedef.DecodeDomainReplay(comment)
			if err == nil && recognized {
				domainReplays = append(domainReplays, typedef.PositionedDomainReplay{Record: record, End: position.Offset + len(comment)})
			}
			return err
		}})
		if err != nil {
			return err
		}
		domainReplayPairs, err := typedef.PairDomainReplay(record.Rows, statements, domainReplays)
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
				if len(d.Columns) != 0 {
					return fmt.Errorf("stuntgres: snapshot DEFAULT requires zero columns")
				}
				rows = append(rows, []sqltype.Value{})
				continue
			}
			for _, row := range s.Rows {
				if len(row) != len(d.Columns) {
					return fmt.Errorf("stuntgres: invalid materialized view snapshot row width")
				}
				for _, raw := range row {
					if err := materializedSnapshotScalar(raw); err != nil {
						return err
					}
				}
			}
			var replay *typedef.DomainReplayRecord
			if captured, ok := domainReplayPairs[s]; ok {
				replay = &captured
			}
			values, err := e.importMaterializedSnapshotRow(ctx, op, auth, d, s, input, replay)
			if err != nil {
				return err
			}
			rows = append(rows, values...)
		}
		imports = append(imports, imported{d, rows, record.Populated})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := catalog.CloneState(e.catalog.state)
	for _, item := range imports {
		versions, err := e.storage.AllocateRowVersionsLocked(item.rows, nil)
		if err != nil {
			return err
		}
		for i := range versions {
			versions[i].Born = e.visibility.CommitSeq()
		}
		d := *item.descriptor
		d.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		d.Populated = item.populated
		next.SetDescriptor(d.Address(), &d)
		e.storage.InstallRelationLocked(d.Address(), &storage.Relation{Versions: versions})
	}
	e.catalog.state = next
	return nil
}

// Captured cells use the same exact INSERT binder transport as ordinary private
// table replay. A real statement scope also retains normal CHECK providers for
// all other non-NULL conversions; no constraint suppression is introduced here.
func (e *Engine) importMaterializedSnapshotRow(ctx context.Context, op *Operation, auth AuthorizationView, d *catalog.Relation, s *parser.Insert, input sqltype.InputEnvironment, replay *typedef.DomainReplayRecord) (rows [][]sqltype.Value, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			rows = nil
		}
	}()
	if err = scope.gate(s); err != nil {
		return nil, err
	}
	scope.input = input
	scope.routineAuth = auth
	if replay != nil {
		capture, captureErr := e.captureDomainReplay(scope, s, *replay, auth, d)
		if captureErr != nil {
			return nil, captureErr
		}
		if operationReplayOrZero(op).domainReplay != nil {
			return nil, fmt.Errorf("stuntgres: nested native replay transport")
		}
		operationReplay(op).domainReplay = capture
		defer func() { operationReplay(op).domainReplay = nil }()
	}
	e.mu.Lock()
	b, err := describe(ctx, e.analysisHost(op), s, auth, e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), input, nil, nil)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if b.Parameters.Count != 0 || len(b.QuerySources) != 0 || len(b.Insert.Defaults) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot row binding")
	}
	environment := input
	environment.Regclass, environment.Regtype = b.Resolvers.Regclass, b.Resolvers.Regtype
	environment.Regrole, environment.Operator, environment.Regproc = b.Resolvers.Regrole, b.Resolvers.Operator, b.Resolvers.Regproc
	environment.Regcollation, environment.Regnamespace = b.Resolvers.Regcollation, b.Resolvers.Regnamespace
	environment.RegTS, environment.ACLItem = b.Resolvers.RegTS, b.Resolvers.ACLItem
	environment.FTS = b.Resolvers.FTS
	environment.TypeDiagnosticName, environment.EnumMemberSafe = b.Resolvers.TypeDiagnosticName, b.Resolvers.EnumMemberSafe
	scope.routineDependencies = b.Dependencies
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: environment, Output: scope.output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
	evaluation.Input.DomainChecks = &evaluation
	for _, expressions := range b.Expressions {
		values := make([]sqltype.Value, len(d.Columns))
		for i, expression := range expressions {
			expression, _, err = preflightCoercionsWithPolicy(&evaluation, expression, false)
			if err != nil {
				return nil, err
			}
			value, evalErr := expression.Eval(&evaluation)
			if evalErr != nil {
				return nil, evalErr
			}
			if value.Type() != d.Columns[i].Usage.Type {
				return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot value type")
			}
			values[i] = value
		}
		rows = append(rows, values)
	}
	return rows, nil
}
