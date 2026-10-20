package engine

import (
	"fmt"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise the real opener's lock-consumption boundary, not a copied projection:
// publication completes after the first source opens and before the next opens.
// All three choices of first occurrence must retain the statement's old frontier.
func TestAvailableExtensionsStatementFrontier(t *testing.T) {
	for _, first := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
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
				s := &StatementScope{op: op, owner: op.Session.Transactions}
				e.mu.Lock()
				err := e.captureCommentSnapshotLocked(s)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			open := func(s *StatementScope, oid ids.OID) *exec.Source {
				t.Helper()
				d, ref := availableUnitSource(e, oid)
				e.mu.Lock()
				source, err := e.openRelationSourceLocked(s, ref, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(source.Close)
				for _, row := range source.Anonymous {
					for i, value := range row {
						if value.Type() != d.Columns[i].Usage.Type {
							t.Fatal("source lost generation-owned type", oid, i)
						}
					}
				}
				return source
			}
			old := capture()
			before := old.inquiry.commentCatalog.State()
			sources := map[ids.OID]*exec.Source{first: open(old, first)}
			retained := make([][]sqltype.Value, len(sources[first].Anonymous))
			for i, row := range sources[first].Anonymous {
				retained[i] = append([]sqltype.Value(nil), row...)
			}
			// The opener returned with e.mu released, as in recursive input opening.
			// Complete an authentic peer publication in this exact gap, no sleeps.
			extensionUnitPublish(t, e, publisher, stage)
			fresh := capture()
			if fresh.inquiry.commentCatalog.State() == before || old.inquiry.commentCatalog.State() != before {
				t.Fatal("publication did not advance only the fresh statement frontier")
			}
			for _, oid := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
				if oid != first {
					sources[oid] = open(old, oid)
				}
			}
			if !reflect.DeepEqual(retained, sources[first].Anonymous) {
				t.Fatal("publication mutated already-opened rows")
			}
			assertAvailable := func(source *exec.Source, oid ids.OID, installed bool) {
				t.Helper()
				var unaccentVersion any
				if installed {
					unaccentVersion = "1.1"
				}
				expected := map[string][]any{
					"cube":          {"cube", "1.5", nil, "data type for multidimensional cubes"},
					"unaccent":      {"unaccent", "1.1", unaccentVersion, "text search dictionary that removes accents"},
					"intarray":      {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"},
					"intagg":        {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"},
					"fuzzystrmatch": {"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"},
					"pgcrypto":      {"pgcrypto", "1.4", nil, "cryptographic functions"},
					"uuid-ossp":     {"uuid-ossp", "1.1", nil, "generate universally unique identifiers (UUIDs)"},
				}
				if oid == 12085 {
					expected["cube"] = []any{"cube", "1.5", false, true, true, true, nil, nil, "data type for multidimensional cubes"}
					expected["unaccent"] = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, "text search dictionary that removes accents"}
					expected["intarray"] = []any{"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}
					expected["intagg"] = []any{"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}
					expected["fuzzystrmatch"] = []any{"fuzzystrmatch", "1.2", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
					expected["pgcrypto"] = []any{"pgcrypto", "1.4", false, true, true, true, nil, nil, "cryptographic functions"}
					expected["uuid-ossp"] = []any{"uuid-ossp", "1.1", false, true, true, true, nil, nil, "generate universally unique identifiers (UUIDs)"}
					versions := make(map[string][]any, len(expected)+1)
					for name, row := range expected {
						versions[name+"\x00"+row[1].(string)] = row
					}
					expected = versions
					expected["fuzzystrmatch\x001.1"] = []any{"fuzzystrmatch", "1.1", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
				}
				for _, row := range source.Anonymous {
					name, err := row[0].Text()
					if err != nil {
						t.Fatal("inventory name", oid, err)
					}
					if oid == 12085 {
						version, versionErr := row[1].Text()
						if versionErr != nil {
							t.Fatal(versionErr)
						}
						name += "\x00" + version
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
			if sources[ids.ExtensionClassID].Count() != 0 {
				t.Error("old statement pg_extension must have no installed row")
			}
			for _, oid := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
				source := open(fresh, oid)
				if oid != ids.ExtensionClassID {
					assertAvailable(source, oid, true)
					continue
				}
				if source.Count() != 1 {
					t.Fatal("fresh statement must see exactly the genuine installation", source.Count())
				}
				name, nameErr := source.Anonymous[0][1].Text()
				version, versionErr := source.Anonymous[0][5].Text()
				if nameErr != nil || versionErr != nil || name != "unaccent" || version != "1.1" {
					t.Fatal("fresh installed identity", name, version, nameErr, versionErr)
				}
			}
		})
	}
	t.Run("version_update", extensionVersionAvailableFrontier)
}
