package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

// Inspect actual frozen captures and renderer output, not SQL result parity.
// Public Dump/Restore and PostgreSQL replay witnesses live in reference/.
func TestLtreeLqueryStoredLiteralTransport(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE SCHEMA lq_transport")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree SCHEMA lq_transport")
	for _, tc := range []struct {
		name, input, want string
	}{
		{"leaf", "'Top.*{1,2}.Child'::lq_transport.lquery", `CAST('Top.*{1,2}.Child' AS "lq_transport"."lquery")`},
		{"null", "NULL::lq_transport.lquery", `CAST(NULL AS "lq_transport"."lquery")`},
		{"outer", "'Top.*{1,2}.Child'::lq_transport.lquery::lq_transport.lquery", `CAST(CAST('Top.*{1,2}.Child' AS "lq_transport"."lquery") AS "lq_transport"."lquery")`},
		{"conversion", "'Top.*{1,2}.Child'::lq_transport.lquery::text", `CAST(CAST('Top.*{1,2}.Child' AS "lq_transport"."lquery") AS TEXT)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "lq_transport_" + tc.name
			enginetest.SessionSQL(t, s, "CREATE VIEW "+name+" AS SELECT "+tc.input+" AS value")
			relation := preparedFoldStoredRelation(t, e, name)
			view := e.CatalogRead()
			definition := stored.ViewOf(relation)
			root := definition.Query().Root
			casts := maps.Clone(definition.CastUsages())
			literals := maps.Clone(definition.Literals())
			if len(casts) == 0 || len(literals) == 0 {
				t.Fatal("genuine frozen leaf/cast captures missing")
			}
			if err := definition.Validate(view, relation); err != nil {
				t.Fatal(err)
			}
			sql, err := stored.ViewDefinitionSQL(context.Background(), view, relation, stored.Presentation{})
			if err != nil || !strings.Contains(sql, tc.want+` AS "value"`) || strings.Count(sql, "CAST(") != strings.Count(tc.want, "CAST(") {
				t.Fatalf("captured leaf annotation/outer conversion: got %q, want expression %q: %v", sql, tc.want, err)
			}
			// Eliding presentation must not elide authentication of any source
			// cast, even a numerically equal descriptor of the installed type.
			for path, usage := range casts {
				foreign := *usage.Type
				rejected := definition.With(func(parts *stored.ViewParts) {
					parts.CastUsages = maps.Clone(parts.CastUsages)
					usage.Type = &foreign
					parts.CastUsages[path] = usage
				})
				if err := rejected.Validate(view, relation); err == nil {
					t.Fatal("foreign frozen cast generation accepted", path)
				}
			}
			if definition.Query().Root != root || !reflect.DeepEqual(definition.CastUsages(), casts) || !reflect.DeepEqual(definition.Literals(), literals) {
				t.Fatal("render/rejection changed accepted source captures")
			}
		})
	}
}
