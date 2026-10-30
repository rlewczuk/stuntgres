package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
)

func TestExplainJSONPresentation(t *testing.T) {
	r := &explainRenderer{options: explainOptions{format: "json"}}
	tree := &explainTree{nodeType: "Project", label: "not a machine-readable label", estimate: plan.ExplainEstimate{Rows: 3, Cost: 7}, children: []*explainTree{{nodeType: "Values"}, {nodeType: "CTE", properties: map[string]any{"CTE Name": "Odd,CTE:\n\""}}}}
	r.property(tree, "Output", []string{"'a,b:c''d'::text", "'line\nend'::text"})
	r.repeated(tree.children[0], "Values", []string{"1", "'a,b'"})
	r.repeated(tree.children[0], "Values", []string{"2", "'c:d'"})
	r.repeated(tree, "Aggregate", "count(*)")
	r.repeated(tree, "Aggregate", "sum(x)")
	if err := r.foreignProperties(tree, []catalog.ForeignExplainProperty{{Name: "Node Type", Value: "not structural"}, {Name: "same", Value: "first"}, {Name: "same", Value: "second"}}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(r.jsonNode(tree))
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	const expected = `{"Node Type":"Project","Output":["'a,b:c''d'::text","'line\nend'::text"],"Aggregate":["count(*)","sum(x)"],"Provider Properties":[{"Name":"Node Type","Value":"not structural"},{"Name":"same","Value":"first"},{"Name":"same","Value":"second"}],"Plans":[{"Node Type":"Values","Values":[["1","'a,b'"],["2","'c:d'"]]},{"Node Type":"CTE","CTE Name":"Odd,CTE:\n\""}]}`
	var want map[string]any
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %s; want %s", encoded, expected)
	}
	before := string(encoded)
	if _, err := json.Marshal(r.jsonNode(tree)); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r.jsonNode(tree))
	if string(after) != before {
		t.Fatal("render mutates frozen properties")
	}
	r.options.costs = true
	costed := r.jsonNode(tree)
	if costed["Plan Rows"] != float64(3) || costed["Logical Cost"] != float64(7) || costed["Estimate Model"] == nil {
		t.Fatal(costed)
	}
	for _, key := range []string{"Startup Cost", "Total Cost", "Plan Width", "Actual Rows", "Actual Loops"} {
		if _, exists := costed[key]; exists {
			t.Fatal("invented field", key)
		}
	}
	tree.unknownEstimate = true
	if _, exists := r.jsonNode(tree)["Plan Rows"]; exists {
		t.Fatal("unknown estimate exposed")
	}
	_, r.observation = exec.Observe(context.Background(), true)
	r.options.timing = true
	tree.actual = &exec.Actual{Rows: 6, Loops: 2, Elapsed: 8 * time.Millisecond}
	observed := r.jsonNode(tree)
	if observed["Actual Rows"] != float64(3) || observed["Actual Loops"] != int64(2) || observed["Actual Total Time"] != float64(4) {
		t.Fatal(observed)
	}
	if _, exists := observed["Actual Startup Time"]; exists {
		t.Fatal("fabricated startup")
	}
	tree.actual = &exec.Actual{}
	zero := r.jsonNode(tree)
	if zero["Actual Rows"] != float64(0) || zero["Actual Loops"] != int64(0) {
		t.Fatal(zero)
	}
	if _, exists := zero["Actual Total Time"]; exists {
		t.Fatal("zero-loop timing")
	}
	r.options.timing = false
	tree.actual = &exec.Actual{Rows: 1, Loops: 1, Elapsed: time.Millisecond}
	if _, exists := r.jsonNode(tree)["Actual Total Time"]; exists {
		t.Fatal("timing false ignored")
	}
	text := &explainRenderer{options: explainOptions{format: "text"}}
	plain := &explainTree{}
	text.property(plain, "Output", []string{"x"})
	text.repeated(plain, "Values", []string{"y"})
	if plain.properties != nil {
		t.Fatal("TEXT allocated JSON properties")
	}
}
