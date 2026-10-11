package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Every scalar overload goes through the real stored serializers and walkers,
// not just its direct SELECT binder. Called by the registered stored group.
func regexpStoredOverloads(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for i, c := range regexpSignatures() {
		if c.set {
			continue
		}
		t.Run(fmt.Sprintf("stored/%s/%d", c.name, i), func(t *testing.T) {
			name := fmt.Sprintf("stored_overload_%d", i)
			resultType := map[string]string{"INT4": "INTEGER", "BOOL": "BOOLEAN", "TEXT": "TEXT", "_TEXT": "TEXT[]"}[c.typ]
			positional := c.name + "(" + strings.Join(c.values, ",") + ")"
			args := append([]string(nil), c.values...)
			args[0] = "s"
			var named []string
			for j := len(args) - 1; j >= 0; j-- {
				named = append(named, c.formals[j]+"=>"+args[j])
			}
			expr := c.name + "(" + strings.Join(named, ",") + ")"
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(fmt.Sprintf("CREATE TABLE %s(s TEXT,d %s DEFAULT %s,g %s GENERATED ALWAYS AS (%s) STORED,CHECK(%s IS NOT NULL))", name, resultType, positional, resultType, expr, expr), 0))
			run(fixture.Exec(fmt.Sprintf("INSERT INTO %s(s) VALUES(%s)", name, c.values[0]), 1))
			indexSQL := fmt.Sprintf("CREATE INDEX %s_idx ON %s((%s)) WHERE %s IS NOT NULL", name, name, expr, expr)
			index := fixture.Exec(indexSQL, 0)
			if !b.PG && c.typ == "_TEXT" {
				// Array opclasses are an existing type/index capability limit,
				// not a regexp function exclusion. Keep the real PG index proof.
				index = similarReferenceError(indexSQL, fixture.Diagnostic{Code: "42704", Message: `data type text[] has no default operator class for access method "btree"`, Hint: "You must specify an operator class for the index or define a default operator class for the data type."})
				index.SelectRows = false
			}
			run(index)
			if c.typ == "_TEXT" {
				// The selected array function still participates in real
				// stored index predicates over an admitted scalar key.
				run(fixture.Exec(fmt.Sprintf("CREATE INDEX %s_scalar_idx ON %s(s) WHERE %s IS NOT NULL", name, name, expr), 0))
			}
			regexpRoutes(t, p, b, fixture.Select("SELECT d,g FROM "+name, []string{"d", "g"}, []string{c.typ, c.typ}, [][]any{{c.value, c.value}}))
			run(fixture.Exec(fmt.Sprintf("ALTER TABLE %s RENAME COLUMN s TO source", name), 0))
			regexpRoutes(t, p, b, fixture.Select("SELECT d,g FROM "+name, []string{"d", "g"}, []string{c.typ, c.typ}, [][]any{{c.value, c.value}}))
		})
	}
}

func regexpWrongSignatureRoles(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, c := range regexpSignatures() {
		for bad := range c.values {
			values := make([]string, len(c.values))
			types := append([]string(nil), c.sqltypes...)
			for i, v := range c.values {
				values[i] = v + "::" + c.sqltypes[i]
			}
			values[bad] = "true::boolean"
			types[bad] = "boolean"
			q := "SELECT /* é🙂 */ " + c.name + "(" + strings.Join(values, ",") + ")"
			msg := "function " + c.name + "(" + strings.Join(types, ", ") + ") does not exist"
			regexpRoutes(t, p, b, regexpBindError(q, "42883", msg, similarFunctionHint, c.name))
		}
	}
}
