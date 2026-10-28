package reference_test

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// IDs deliberately never enter cross-backend observations: only their equivalence
// classes within this one invocation are portable. Values retain exact decimals.
func jsonpathMethodsPairs(t *testing.T, p *fixture.Pair, executor fixture.Executor, q string, args []any, keys []string, values []string, classes []int) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"changed"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "JSONB" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing identity array", rows.Err())
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var pairs []struct {
		ID    json.Number     `json:"id"`
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pairs); err != nil {
		t.Fatal(raw, err)
	}
	if len(pairs) != len(keys) || len(keys) != len(values) || len(keys) != len(classes) {
		t.Fatalf("pairs=%s want keys=%v", raw, keys)
	}
	ids := make(map[int]string)
	seen := make(map[string]int)
	for i, pair := range pairs {
		if pair.Key != keys[i] {
			t.Fatalf("pair[%d] key=%s want %s", i, pair.Key, keys[i])
		}
		if values[i] == "<id>" {
			n, err := json.Number(pair.Value).Int64()
			if err != nil || n < 0 {
				t.Fatalf("pair[%d] value is not an identity: %s", i, pair.Value)
			}
		} else if string(pair.Value) != values[i] {
			t.Fatalf("pair[%d] value=%s want %s", i, pair.Value, values[i])
		}
		id, err := pair.ID.Int64()
		if err != nil || id < 0 {
			t.Fatalf("invalid ID %q: %v", pair.ID, err)
		}
		if old, ok := ids[classes[i]]; ok && old != pair.ID.String() {
			t.Fatalf("class %d split: %s", classes[i], raw)
		}
		if class, ok := seen[pair.ID.String()]; ok && class != classes[i] {
			t.Fatalf("distinct classes %d/%d collided: %s", class, classes[i], raw)
		}
		ids[classes[i]] = pair.ID.String()
		seen[pair.ID.String()] = classes[i]
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra rows or terminal error", rows.Err())
	}
}

func jsonpathMethodsIdentityRoutes(t *testing.T, p *fixture.Pair, c *sql.Conn, doc, path, vars string, keys, values []string, classes []int) {
	t.Helper()
	q := `SELECT jsonb_path_query_array($1::jsonb,$2::jsonpath,$3::jsonb) AS changed`
	args := []any{doc, path, vars}
	jsonpathMethodsPairs(t, p, c, q, args, keys, values, classes)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	jsonpathMethodsPairs(t, p, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, args, keys, values, classes)
}

func TestReferenceJSONPathMethodsKeyValue(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{jsonpathMethodsO, `$.keyvalue().key`, ``, `["x", "y", "z"]`},
		{jsonpathMethodsO, `$.keyvalue().value`, ``, `["20", 32, null]`},
		{jsonpathMethodsO, `$.keyvalue().type()`, ``, `["object", "object", "object"]`},
		{`{}`, `$.keyvalue()`, ``, `[]`},
		{`{"bb":[1,2],"a":{"x":1}}`, `$.keyvalue().value`, ``, `[{"x": 1}, [1, 2]]`},
		{jsonpathMethodsO, `$.keyvalue()?(@.key=="y" && @.id>=0).value.string()`, ``, `["32"]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonpathMethodsIdentityRoutes(t, p, c, jsonpathMethodsO, `$.keyvalue()`, `{}`, []string{"x", "y", "z"}, []string{`"20"`, `32`, `null`}, []int{0, 0, 0})
	})
}

func TestReferenceJSONPathMethodsIdentity(t *testing.T) {
	jsonpathNavigateCases(t, []jsonpathNavigateCase{
		{`{"a":1}`, `$.keyvalue().id != $v.keyvalue().id`, `{"v":{"a":1}}`, `[true]`},
		{`0`, `$a.keyvalue().id != $b.keyvalue().id`, `{"a":{"x":1},"b":{"x":1}}`, `[true]`},
		{`{"a":1}`, `$.keyvalue()?(exists($v.keyvalue())).id == $.keyvalue().id`, `{"v":{"a":1}}`, `[true]`},
	})
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonpathMethodsIdentityRoutes(t, p, c, `[{"a":1,"b":2},{"a":1,"b":2}]`, `$[0,0,1].keyvalue()`, `{}`, []string{"a", "b", "a", "b", "a", "b"}, []string{`1`, `2`, `1`, `2`, `1`, `2`}, []int{0, 0, 0, 0, 1, 1})
		// Repeated source object, fresh generated wrapper/value bases per pair.
		jsonpathMethodsIdentityRoutes(t, p, c, `{"a":{"x":1,"y":2}}`, `$[0,0].keyvalue().value.keyvalue()`, `{}`, []string{"x", "y", "x", "y"}, []string{`1`, `2`, `1`, `2`}, []int{0, 0, 1, 1})
		// Predicate operand switching to vars/root must restore the current origin.
		jsonpathMethodsIdentityRoutes(t, p, c, `[{"a":1},{"a":1}]`, `$[0,0,1]?(exists($v.keyvalue()) && exists($.keyvalue())).keyvalue()`, `{"v":{"a":1}}`, []string{"a", "a", "a"}, []string{`1`, `1`, `1`}, []int{0, 0, 1})
		jsonpathMethodsIdentityRoutes(t, p, c, `{"a":1,"b":2}`, `$.keyvalue().keyvalue()`, `{}`, []string{"id", "key", "value", "id", "key", "value"}, []string{"<id>", `"a"`, `1`, "<id>", `"b"`, `2`}, []int{0, 0, 0, 1, 1, 1})
	})
}
