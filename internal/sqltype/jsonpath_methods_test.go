package sqltype

import (
	"context"
	"sync"
	"testing"
)

func TestJSONPathMethodsRetainedNodes(t *testing.T) {
	types := testTypes()
	values := navigateValues(t, types, `[{"a":1,"b":2},{"a":3}]`, `$[*].keyvalue()`, `{}`, false)
	before, err := jsonbOutput(context.Background(), &values[0].jsonb.root)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*jsonbNode
	_, err = executeJSONPath(context.Background(), values[0], values[1], values[2], func(n *jsonbNode) (bool, error) { nodes = append(nodes, n); return true, nil })
	if err != nil || len(nodes) != 3 {
		t.Fatal(len(nodes), err)
	}
	texts := make([]string, len(nodes))
	for i, n := range nodes {
		texts[i], err = jsonbOutput(context.Background(), n)
		if err != nil {
			t.Fatal(err)
		}
		for j := range i {
			if n == nodes[j] {
				t.Fatal("generated wrapper reused", i, j)
			}
		}
	}
	p := navigatePlan(t, types)
	for range 10 {
		if _, err := p.Evaluate(context.Background(), values); err != nil {
			t.Fatal(err)
		}
	}
	for i, n := range nodes {
		text, err := jsonbOutput(context.Background(), n)
		if err != nil || text != texts[i] {
			t.Fatal("retained node mutated", i, text, err)
		}
	}
	after, err := jsonbOutput(context.Background(), &values[0].jsonb.root)
	if err != nil || after != before {
		t.Fatal("input mutated", after, err)
	}
}

func TestJSONPathMethodsIsolation(t *testing.T) {
	types := testTypes()
	p := navigatePlan(t, types)
	values := navigateValues(t, types, `[{"a":1,"b":2},{"a":1,"b":2}]`, `$[0,0,1].keyvalue()`, `{}`, false)
	root, path := values[0].jsonb, values[1].jsonpath
	// Compare identity relationships, not allocator values across invocations.
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 25 {
				var nodes []*jsonbNode
				_, err := executeJSONPath(context.Background(), values[0], values[1], values[2], func(n *jsonbNode) (bool, error) { nodes = append(nodes, n); return true, nil })
				if err != nil || len(nodes) != 6 {
					t.Errorf("execution: %d %v", len(nodes), err)
					return
				}
				ids := make([]string, len(nodes))
				for i, n := range nodes {
					if len(n.fields) != 3 || n.fields[0].key != "id" || n.fields[1].key != "key" || n.fields[2].key != "value" {
						t.Errorf("wrapper shape: %+v", n)
						return
					}
					ids[i], err = jsonbOutput(context.Background(), &n.fields[0].value)
					if err != nil {
						t.Error(err)
						return
					}
				}
				if ids[0] != ids[1] || ids[0] != ids[2] || ids[0] != ids[3] || ids[4] != ids[5] || ids[0] == ids[4] {
					t.Errorf("identity classes: %v", ids)
					return
				}
				v, err := p.Evaluate(context.Background(), values)
				if err != nil || v.jsonb == nil {
					t.Error("plan execution", err)
					return
				}
			}
		})
	}
	wg.Wait()
	if values[0].jsonb != root || values[1].jsonpath != path {
		t.Fatal("replaced immutable payload")
	}
	printed, err := printJSONPath(context.Background(), path)
	if err != nil || printed != values[1].text {
		t.Fatal("mutated path", printed, err)
	}
}
