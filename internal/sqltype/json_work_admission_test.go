package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/workadmission"
)

// These are actual private provider calls, not SQL or synthetic admission work.
func TestJSONWorkAdmissionRefusalAndClassPrecision(t *testing.T) {
	types := testTypes()
	background := context.Background()
	document := jsonFunctionUnitInput(t, types[JSONB], `{"a":[1,2]}`)
	array := jsonFunctionUnitInput(t, types[JSONB], `[1,2]`)
	path := jsonFunctionUnitInput(t, types[1009], `{a}`)
	row := jsonbRecordUnitType(t, 29100, CompositeMember{1, "a", types[Int4].Usage()})
	eachRow := jsonbRecordUnitType(t, 29102, CompositeMember{1, "key", types[Text].Usage()}, CompositeMember{2, "value", types[JSONB].Usage()})
	refused := errors.New("JSON work credit refused")
	cases := []struct {
		class workadmission.Class
		run   func(context.Context) error
	}{
		{workadmission.WorkJSONArrayCount, func(ctx context.Context) error {
			n, err := jsonArrayLength(ctx, `[1,2]`)
			if err != nil && n != 0 {
				t.Fatal("partial count", n)
			}
			return err
		}},
		{workadmission.WorkJSONObjectExpand, func(ctx context.Context) error {
			p := jsonEachUnitPlan(t, types, "json_each")
			c, err := p.Open(ctx, jsonFunctionUnitInput(t, types[JSON], `{"a":1}`))
			defer c.Close()
			if err != nil && c.result != nil {
				t.Fatal("published failed cursor")
			}
			return err
		}},
		{workadmission.WorkJSONStripNulls, func(ctx context.Context) error {
			text, err := jsonStripNulls(ctx, `{"a":null,"b":1}`, false)
			if err != nil && text != "" {
				t.Fatal("partial stripped output")
			}
			return err
		}},
		{workadmission.WorkJSONBContainment, func(ctx context.Context) error {
			_, err := jsonbContains(ctx, &document.jsonb.root, &document.jsonb.root)
			return err
		}},
		{workadmission.WorkJSONBObjectExpand, func(ctx context.Context) error {
			c := JSONEachCursor{result: eachRow, document: document}
			defer c.Close()
			v, ok, err := c.Next(ctx)
			if err != nil && (ok || v != (Value{}) || c.index != 0) {
				t.Fatal("advanced refused member")
			}
			return err
		}},
		{workadmission.WorkJSONBArrayExpand, func(ctx context.Context) error {
			c := JSONBElementCursor{result: types[JSONB], payload: array.jsonb}
			defer c.Close()
			v, ok, err := c.Next(ctx)
			if err != nil && (ok || v != (Value{}) || c.index != 0) {
				t.Fatal("advanced refused element")
			}
			return err
		}},
		{workadmission.WorkJSONBExistence, func(ctx context.Context) error {
			_, err := jsonbExists(ctx, &document.jsonb.root, "a")
			return err
		}},
		{workadmission.WorkJSONBPathDelete, func(ctx context.Context) error {
			_, changed, err := jsonbSetPath(ctx, document.jsonb.root, path, jsonbNode{}, false, jsonbDelete)
			if err != nil && changed {
				t.Fatal("published refused mutation")
			}
			return err
		}},
		{workadmission.WorkJSONBPretty, func(ctx context.Context) error {
			text, err := jsonbPrettyOutput(ctx, &document.jsonb.root)
			if err != nil && text != "" {
				t.Fatal("partial pretty output")
			}
			return err
		}},
		{workadmission.WorkJSONRecordExpand, func(ctx context.Context) error {
			scan := jsonScanResult{record: &jsonRecordDocument{}, recordName: "json_to_record", workClass: workadmission.WorkJSONRecordExpand}
			_, err := types[JSON].scanJSON(ctx, `{"a":1}`, &scan)
			return err
		}},
		{workadmission.WorkJSONBRecordPopulate, func(ctx context.Context) error {
			v, err := JSONBPopulateRecord(ctx, row.Usage(), Null(row), document, InputEnvironment{})
			if err != nil && v != (Value{}) {
				t.Fatal("published refused record")
			}
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(string(rune('A'+tc.class)), func(t *testing.T) {
			ctx := workadmission.Activate(workadmission.With(background, func(_ context.Context, class workadmission.Class) error {
				if class != tc.class {
					t.Fatalf("wrong class: got %v want %v", class, tc.class)
				}
				return refused
			}))
			if err := tc.run(ctx); err != refused {
				t.Fatalf("refusal identity: %v", err)
			}
		})
	}
	if text, err := types[JSONB].Output(background, document); err != nil || text != `{"a": [1, 2]}` {
		t.Fatal("input changed", text, err)
	}
}

func TestJSONWorkAdmissionProcessingIsolation(t *testing.T) {
	types := testTypes()
	refused := errors.New("unexpected processing admission")
	ctx := workadmission.Activate(workadmission.With(context.Background(), func(context.Context, workadmission.Class) error { return refused }))
	for _, id := range []ID{JSON, JSONB} {
		if _, err := types[id].Input(ctx, `{"a":[1,2]}`, -1); err != nil {
			t.Fatal("input consumed processing credit", err)
		}
	}
	scan := jsonScanResult{classify: true}
	if _, err := types[JSON].scanJSON(ctx, `{"a":1}`, &scan); err != nil {
		t.Fatal("classification consumed processing credit", err)
	}
	document := jsonFunctionUnitInput(t, types[JSONB], `{"a":1}`)
	path := jsonFunctionUnitInput(t, types[1009], `{a}`)
	if _, _, err := jsonbSetPath(ctx, document.jsonb.root, path, jsonbNode{kind: jsonbNull}, true, jsonbReplace); err != nil {
		t.Fatal("replacement consumed deletion credit", err)
	}
	c := JSONBElementCursor{result: types[JSONB]}
	if v, ok, err := c.Next(ctx); err != nil || ok || !v.IsNull() {
		t.Fatal("EOF admitted", v, ok, err)
	}
	c.Close()
}

func TestJSONWorkAdmissionCancellationAndCursorOwnership(t *testing.T) {
	types := testTypes()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := workadmission.Activate(workadmission.With(parent, func(context.Context, workadmission.Class) error {
		cancel()
		return nil
	}))
	input := jsonFunctionUnitInput(t, types[JSONB], `[{"a":1}]`)
	c := JSONBElementCursor{result: types[JSONB], payload: input.jsonb}
	if v, ok, err := c.Next(ctx); err != context.Canceled || ok || v != (Value{}) || c.index != 0 {
		t.Fatal("cancellation published or advanced", v, ok, err)
	}
	v, ok, err := c.Next(context.Background())
	if err != nil || !ok {
		t.Fatal("refusal damaged cursor", err)
	}
	c.Close()
	if text, err := types[JSONB].Output(context.Background(), v); err != nil || text != `{"a": 1}` {
		t.Fatal("owned output lost after close", text, err)
	}
}
