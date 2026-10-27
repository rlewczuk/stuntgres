package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSettingCallOutputLabelsDescriptionAndOwnership(t *testing.T) {
	for _, tc := range []struct {
		query, label string
		types        []sqltype.ID
		vectors      [][]any
		want         []any
	}{
		{`SELECT pg_catalog.current_setting($1) AS "ReadValue"`, "ReadValue", []sqltype.ID{sqltype.Text}, [][]any{{"application_name"}, {"application_name"}, {nil}}, []any{"initial", "initial", nil}},
		{`SELECT pg_catalog.current_setting($1,$2) value`, "value", []sqltype.ID{sqltype.Text, sqltype.Bool}, [][]any{{"application_name", false}, {"label_missing", true}, {"application_name", nil}}, []any{"initial", nil, nil}},
		{`SELECT pg_catalog.set_config($1,$2,$3) AS "SetValue"`, "SetValue", []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}, [][]any{{"application_name", "one", false}, {"application_name", "two", false}, {"application_name", nil, nil}}, []any{"one", "two", "initial"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, &settingsDefaultProviderForTest{values: map[string]string{"application_name": "initial"}, calls: map[string]int{}})
			enginetest.SessionSQL(t, s, "SET client_min_messages=notice")
			textType, _ := e.CatalogRead().LookupType(sqltype.Text)
			wantColumn := catalog.Column{Name: tc.label, Usage: textType.Usage(), Nullable: true, NullabilityKnown: true}
			start := func() (*session.Operation, *core.StatementScope) {
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				t.Cleanup(op.Done)
				return op, scope
			}
			ast := enginetest.Statement(t, tc.query)
			astSnapshot := enginetest.Statement(t, tc.query)
			query := ast.(*parser.Select)
			call := query.Targets[0].(parser.FunctionExpr)
			argumentsBacking, nameBacking, labelsBacking := &call.Args[0], &call.Name.Parts[0], &query.Labels[0]
			op, scope := start()
			description, err := scope.Describe(ast)
			finish := scope.Finish(err)
			notices := op.DetachNotices()
			op.Done()
			if err != nil || finish != nil || len(notices) != 0 {
				t.Fatal(err, finish, notices)
			}
			if len(description.Results) != 1 || description.Results[0] != wantColumn || description.Parameters.Count != len(tc.types) || len(description.Parameters.Types) != len(tc.types) {
				t.Fatal("noncanonical description", description)
			}
			for i, id := range tc.types {
				typ, _ := e.CatalogRead().LookupType(id)
				if description.Parameters.Types[i] != typ.Usage() {
					t.Fatal("wrong formal", i, description.Parameters)
				}
				parameter, ok := call.Args[i].(parser.ParameterExpr)
				if !ok || parameter.Number != i+1 {
					t.Fatal("wrong parameter index", ast)
				}
			}
			definition := core.PreparedDefinition{Body: ast, Source: tc.query, Description: description}
			columnsBacking, parametersBacking := &description.Results[0], &description.Parameters.Types[0]
			columnsSnapshot, parametersSnapshot := slices.Clone(description.Results), slices.Clone(description.Parameters.Types)
			localOwnerValue(t, s, "application_name", "initial")
			// Description must not create placeholder knowledge or normalize values.
			for _, query := range []string{
				`SELECT set_config('label.unseen','x',false) AS value`,
				`SELECT set_config('application_name','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',false) AS value`,
			} {
				op, scope := start()
				_, err := scope.Describe(enginetest.Statement(t, query))
				finish := scope.Finish(err)
				notices := op.DetachNotices()
				op.Done()
				if err != nil || finish != nil || len(notices) != 0 {
					t.Fatal("Describe published effects", err, finish, notices)
				}
			}
			s.Settings.Mu.Lock()
			placeholder := s.Settings.Placeholders["label.unseen"]
			s.Settings.Mu.Unlock()
			if placeholder {
				t.Fatal("Describe installed placeholder")
			}
			localOwnerValue(t, s, "application_name", "initial")
			var frames [][]sqltype.Value
			var frameSnapshots [][]sqltype.Value
			var rows []exec.RowResult
			for _, vector := range tc.vectors {
				op, scope := start()
				scope.Expected = &definition.Description
				result, err := scope.Execute(definition.Body, len(vector), func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					if !reflect.DeepEqual(info, definition.Description.Parameters) {
						t.Fatal("import signature changed", info)
					}
					for _, previous := range frames {
						if &frame[0] == &previous[0] {
							t.Fatal("reused retained parameter frame")
						}
					}
					for i, raw := range vector {
						if raw == nil {
							frame[i] = sqltype.Null(info.Types[i].Type)
							continue
						}
						text, ok := raw.(string)
						if !ok {
							if raw.(bool) {
								text = "true"
							} else {
								text = "false"
							}
						}
						value, err := info.Types[i].Type.InputWithEnvironment(ctx, text, -1, input)
						if err != nil {
							return err
						}
						frame[i] = value
					}
					for i, value := range frame {
						if value.Type() != info.Types[i].Type || value.IsNull() != (vector[i] == nil) {
							t.Fatal("lost typed input", i, value)
						}
					}
					frames = append(frames, frame)
					frameSnapshots = append(frameSnapshots, slices.Clone(frame))
					return nil
				})
				finish := scope.Finish(err)
				op.Done()
				if err != nil || finish != nil || !scope.Finished || s.Transactions.State != session.TxIdle {
					t.Fatal("execution retained owner", err, finish)
				}
				actual := result.Rows
				if actual == nil || result.Count != 1 || len(actual.Schema()) != 1 || actual.Schema()[0] != wantColumn {
					t.Fatal("wrong canonical query result", result)
				}
				if &actual.Schema()[0] == columnsBacking {
					t.Fatal("execution borrowed retained description schema")
				}
				for _, previous := range rows {
					if &actual.Schema()[0] == &previous.Schema()[0] {
						t.Fatal("execution reused another retained result schema")
					}
				}
				rows = append(rows, actual)
				t.Cleanup(func() { actual.Close() })
				enginetest.Statement(t, `SELECT current_setting($1,$2) AS replacement`)
			}
			if definition.Body != ast || definition.Source != tc.query || !reflect.DeepEqual(ast, astSnapshot) || &call.Args[0] != argumentsBacking || &call.Name.Parts[0] != nameBacking || &query.Labels[0] != labelsBacking || &definition.Description.Results[0] != columnsBacking || &definition.Description.Parameters.Types[0] != parametersBacking || !reflect.DeepEqual(definition.Description.Results, columnsSnapshot) || !reflect.DeepEqual(definition.Description.Parameters.Types, parametersSnapshot) || !reflect.DeepEqual(frames, frameSnapshots) {
				t.Fatal("execution changed retained syntax/signature/frame backing")
			}
			s.RequestEnd(session.CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			for i, row := range rows {
				if len(row.Schema()) != 1 || row.Schema()[0] != wantColumn {
					t.Fatal("ended owner changed detached schema")
				}
				values := make([]sqltype.Value, 1)
				present, err := row.Next(context.Background(), values)
				if !present || err != nil || values[0].Type() != textType || values[0].IsNull() != (tc.want[i] == nil) {
					t.Fatal("detached value lost type/NULL", i, present, err, values)
				}
				if tc.want[i] != nil {
					text, err := values[0].Text()
					if err != nil || text != tc.want[i] {
						t.Fatal("detached value consulted ended owner", text, err)
					}
				}
				if present, err := row.Next(context.Background(), values); present || err != nil {
					t.Fatal("wrong snapshot cardinality", present, err)
				}
				if err := row.Close(); err != nil {
					t.Fatal("closing detached query result", err)
				}
				if present, err := row.Next(context.Background(), values); present || err != nil {
					t.Fatal("closed query result retained readable output", present, err)
				}
				if len(row.Schema()) != 1 || row.Schema()[0] != wantColumn {
					t.Fatal("closing query result changed detached schema")
				}
			}
		})
	}
}

func TestSettingCallOutputLabelsCancellationPublication(t *testing.T) {
	for _, canceled := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "local-success"}[canceled], func(t *testing.T) {
			d, err := settings.Validate(settings.Definition{Name: "demo.label_normalizer", Type: settings.String, BootDefault: "base"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var e *core.Engine
			var owner *session.Session
			normalizations := 0
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input != "transient" {
					return input, nil, nil
				}
				// The real normalizer runs outside every publication lock.
				e.Mu.Lock()
				e.Mu.Unlock()
				owner.Settings.Mu.Lock()
				owner.Settings.Mu.Unlock()
				owner.Mu.Lock()
				owner.Mu.Unlock()
				normalizations++
				if canceled {
					cancel()
				}
				return input, nil, nil
			}
			e = engine.NewWithSettings([]settings.Definition{d}, "", false)
			owner = enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			op, err := owner.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, `SELECT pg_catalog.set_config('demo.label_normalizer','transient',true) AS "LocalValue"`), 0, nil)
			finish := scope.Finish(err)
			notices := op.DetachNotices()
			op.Done()
			if normalizations != 1 || len(notices) != 0 || !scope.Finished || owner.Transactions.State != session.TxIdle {
				t.Fatal("unfinished/repeated/noisy publication", normalizations, notices)
			}
			if canceled {
				if !errors.Is(err, context.Canceled) || !errors.Is(finish, context.Canceled) || result.Rows != nil || result.Count != 0 || scope.Completed {
					t.Fatal("canceled normalization published", result, err, finish)
				}
			} else {
				if err != nil || finish != nil || !scope.Finished {
					t.Fatal(err, finish)
				}
				defer result.Rows.Close()
				typ, _ := e.CatalogRead().LookupType(sqltype.Text)
				want := catalog.Column{Name: "LocalValue", Usage: typ.Usage(), Nullable: true, NullabilityKnown: true}
				actual := result.Rows
				if actual == nil || result.Count != 1 || len(actual.Schema()) != 1 || actual.Schema()[0] != want {
					t.Fatal("local query result lost canonical label/type", result)
				}
				values := make([]sqltype.Value, 1)
				present, err := actual.Next(context.Background(), values)
				if !present || err != nil || values[0].Type() != typ {
					t.Fatal(present, err, values)
				}
				if text, err := values[0].Text(); err != nil || text != "transient" {
					t.Fatal(text, err)
				}
			}
			localOwnerValue(t, owner, "demo.label_normalizer", "base")
			localOwnerValue(t, peer, "demo.label_normalizer", "base")
		})
	}
}
