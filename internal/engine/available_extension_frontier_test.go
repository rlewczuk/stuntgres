package engine

import (
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise the real opener's lock-consumption boundary, not a copied projection:
// publication completes after the first source opens and before the next opens.
// All three choices of first occurrence must retain the statement's old frontier.
func TestAvailableExtensionsStatementFrontier(t *testing.T) {
	for _, first := range []OID{12081, 12085, ExtensionClassID} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			publisher, release := roleOperation(t, e, "postgres")
			defer release()
			stage := extensionUnitStage(t, e, publisher)
			defer e.DiscardCatalogStage(stage)
			capture := func() *StatementScope {
				t.Helper()
				s := &StatementScope{op: op, owner: op.session.transactions}
				e.mu.Lock()
				err := e.captureCommentSnapshotLocked(s)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			open := func(s *StatementScope, oid OID) *querySource {
				t.Helper()
				d, ref := availableUnitSource(e, oid)
				e.mu.Lock()
				source, err := e.openRelationSourceLocked(s, ref, e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), queryRowMark{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(source.close)
				for _, row := range source.anonymous {
					for i, value := range row {
						if value.Type() != d.columns[i].Usage.Type {
							t.Fatal("source lost generation-owned type", oid, i)
						}
					}
				}
				return source
			}
			old := capture()
			before := old.commentCatalog.state
			sources := map[OID]*querySource{first: open(old, first)}
			retained := make([][]sqltype.Value, len(sources[first].anonymous))
			for i, row := range sources[first].anonymous {
				retained[i] = append([]sqltype.Value(nil), row...)
			}
			// The opener returned with e.mu released, as in recursive input opening.
			// Complete an authentic peer publication in this exact gap, no sleeps.
			extensionUnitPublish(t, e, publisher, stage)
			fresh := capture()
			if fresh.commentCatalog.state == before || old.commentCatalog.state != before {
				t.Fatal("publication did not advance only the fresh statement frontier")
			}
			for _, oid := range []OID{12081, 12085, ExtensionClassID} {
				if oid != first {
					sources[oid] = open(old, oid)
				}
			}
			if !reflect.DeepEqual(retained, sources[first].anonymous) {
				t.Fatal("publication mutated already-opened rows")
			}
			assertAvailable := func(source *querySource, oid OID, installed bool) {
				t.Helper()
				var unaccentVersion any
				if installed {
					unaccentVersion = "1.1"
				}
				expected := map[string][]any{
					"unaccent":      {"unaccent", "1.1", unaccentVersion, "text search dictionary that removes accents"},
					"intarray":      {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"},
					"intagg":        {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"},
					"fuzzystrmatch": {"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"},
					"pgcrypto":      {"pgcrypto", "1.4", nil, "cryptographic functions"},
				}
				if oid == 12085 {
					expected["unaccent"] = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, "text search dictionary that removes accents"}
					expected["intarray"] = []any{"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}
					expected["intagg"] = []any{"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}
					expected["fuzzystrmatch"] = []any{"fuzzystrmatch", "1.2", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
					expected["pgcrypto"] = []any{"pgcrypto", "1.4", false, true, true, true, nil, nil, "cryptographic functions"}
				}
				for _, row := range source.anonymous {
					name, err := row[0].Text()
					if err != nil {
						t.Fatal("inventory name", oid, err)
					}
					want, ok := expected[name]
					if !ok || len(row) != len(want) {
						t.Fatal("unexpected or duplicate inventory row", oid, name, row)
					}
					delete(expected, name)
					for i, value := range row {
						if value.IsNull() {
							if want[i] != nil {
								t.Fatal("unexpected inventory NULL", oid, name, i, want[i])
							}
							continue
						}
						var got any
						if value.Type().ID() == sqltype.Bool {
							got, err = value.Boolean()
						} else {
							got, err = value.Text()
						}
						if err != nil || got != want[i] {
							t.Fatal("inventory value", oid, name, i, got, want[i], err)
						}
					}
				}
				if len(expected) != 0 {
					t.Fatal("missing inventory rows", oid, expected)
				}
			}
			assertAvailable(sources[12081], 12081, false)
			assertAvailable(sources[12085], 12085, false)
			if sources[ExtensionClassID].count() != 0 {
				t.Error("old statement pg_extension must have no installed row")
			}
			for _, oid := range []OID{12081, 12085, ExtensionClassID} {
				source := open(fresh, oid)
				if oid != ExtensionClassID {
					assertAvailable(source, oid, true)
					continue
				}
				if source.count() != 1 {
					t.Fatal("fresh statement must see exactly the genuine installation", source.count())
				}
				name, nameErr := source.anonymous[0][1].Text()
				version, versionErr := source.anonymous[0][5].Text()
				if nameErr != nil || versionErr != nil || name != "unaccent" || version != "1.1" {
					t.Fatal("fresh installed identity", name, version, nameErr, versionErr)
				}
			}
		})
	}
}
