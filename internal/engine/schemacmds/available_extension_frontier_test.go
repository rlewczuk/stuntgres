package schemacmds_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the real opener's lock-consumption boundary, not a copied projection:
// publication completes after the first source opens and before the next opens.
// All three choices of first occurrence must retain the statement's old frontier.
func TestAvailableExtensionsStatementFrontier(t *testing.T) {
	for _, first := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			publisher, release := enginetest.RoleOperation(t, e, "postgres")
			defer release()
			stage := extensionUnitStage(t, e, publisher)
			defer e.DiscardCatalogStage(stage)
			capture := func() *core.StatementScope {
				t.Helper()
				s := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
				e.Mu.Lock()
				err := e.CaptureCommentSnapshotLocked(s)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			open := func(s *core.StatementScope, oid ids.OID) *exec.Source {
				t.Helper()
				d, ref := availableUnitSource(e, oid)
				e.Mu.Lock()
				source, err := core.EngineOpenRelationSourceLocked(e, s, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
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
			before := old.Inquiry.CommentCatalog.State()
			sources := map[ids.OID]*exec.Source{first: open(old, first)}
			retained := make([][]sqltype.Value, len(sources[first].Anonymous))
			for i, row := range sources[first].Anonymous {
				retained[i] = append([]sqltype.Value(nil), row...)
			}
			// The opener returned with e.mu released, as in recursive input opening.
			// Complete an authentic peer publication in this exact gap, no sleeps.
			extensionUnitPublish(t, e, publisher, stage)
			fresh := capture()
			if fresh.Inquiry.CommentCatalog.State() == before || old.Inquiry.CommentCatalog.State() != before {
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
					"amcheck":       {"amcheck", "1.5", nil, "functions for verifying relation integrity"},
					"citext":        {"citext", "1.8", nil, "data type for case-insensitive character strings"},
					"cube":          {"cube", "1.5", nil, "data type for multidimensional cubes"},
					"earthdistance": {"earthdistance", "1.2", nil, "calculate great-circle distances on the surface of the Earth"},
					"hstore":        {"hstore", "1.8", nil, "data type for storing sets of (key, value) pairs"},
					"unaccent":      {"unaccent", "1.1", unaccentVersion, "text search dictionary that removes accents"},
					"intarray":      {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"},
					"intagg":        {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"},
					"isn":           {"isn", "1.3", nil, "data types for international product numbering standards"},
					"lo":            {"lo", "1.2", nil, "Large Object maintenance"},
					"ltree":         {"ltree", "1.3", nil, "data type for hierarchical tree-like structures"},
					"seg":           {"seg", "1.4", nil, "data type for representing line segments or floating-point intervals"},
					"tablefunc":     {"tablefunc", "1.0", nil, "functions that manipulate whole tables, including crosstab"},
					"pgrowlocks":    {"pgrowlocks", "1.2", nil, "show row-level locking information"},
					"fuzzystrmatch": {"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"},
					"pgcrypto":      {"pgcrypto", "1.4", nil, "cryptographic functions"},
					"uuid-ossp":     {"uuid-ossp", "1.1", nil, "generate universally unique identifiers (UUIDs)"},
				}
				if oid == 12085 {
					expected["amcheck"] = []any{"amcheck", "1.5", false, true, false, true, nil, nil, "functions for verifying relation integrity"}
					expected["citext"] = []any{"citext", "1.8", false, true, true, true, nil, nil, "data type for case-insensitive character strings"}
					expected["cube"] = []any{"cube", "1.5", false, true, true, true, nil, nil, "data type for multidimensional cubes"}
					expected["earthdistance"] = []any{"earthdistance", "1.2", false, true, false, true, nil, "{cube}", "calculate great-circle distances on the surface of the Earth"}
					expected["hstore"] = []any{"hstore", "1.8", false, true, true, true, nil, nil, "data type for storing sets of (key, value) pairs"}
					expected["unaccent"] = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, "text search dictionary that removes accents"}
					expected["intarray"] = []any{"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}
					expected["intagg"] = []any{"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}
					expected["isn"] = []any{"isn", "1.3", false, true, true, true, nil, nil, "data types for international product numbering standards"}
					expected["lo"] = []any{"lo", "1.2", false, true, true, true, nil, nil, "Large Object maintenance"}
					expected["ltree"] = []any{"ltree", "1.3", false, true, true, true, nil, nil, "data type for hierarchical tree-like structures"}
					expected["seg"] = []any{"seg", "1.4", false, true, true, true, nil, nil, "data type for representing line segments or floating-point intervals"}
					expected["tablefunc"] = []any{"tablefunc", "1.0", false, true, true, true, nil, nil, "functions that manipulate whole tables, including crosstab"}
					expected["pgrowlocks"] = []any{"pgrowlocks", "1.2", false, true, false, true, nil, nil, "show row-level locking information"}
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
						} else if value.Type().ID() == sqltype.NameArray {
							got, err = value.NamesOutput()
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

func extensionVersionAvailableFrontier(t *testing.T) {
	for _, first := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			e, publisher, ext := extensionVersionUnitInstalled(t, "1.1")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			capture := func() *core.StatementScope {
				s := &core.StatementScope{Op: op, Owner: op.Session.Transactions}
				e.Mu.Lock()
				err := e.CaptureCommentSnapshotLocked(s)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			open := func(s *core.StatementScope, oid ids.OID) *exec.Source {
				_, ref := availableUnitSource(e, oid)
				e.Mu.Lock()
				source, err := core.EngineOpenRelationSourceLocked(e, s, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(source.Close)
				return source
			}
			old := capture()
			sources := map[ids.OID]*exec.Source{first: open(old, first)}
			retained := make([][]sqltype.Value, len(sources[first].Anonymous))
			for i, row := range sources[first].Anonymous {
				retained[i] = append([]sqltype.Value(nil), row...)
			}
			if err := extensionUnitExecute(t, e, publisher, extensionVersionUnitUpdate()); err != nil {
				t.Fatal(err)
			}
			fresh := capture()
			for _, oid := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
				if oid != first {
					sources[oid] = open(old, oid)
				}
			}
			if !reflect.DeepEqual(retained, sources[first].Anonymous) || old.Inquiry.CommentCatalog.MustExtension(ext.Address()) != ext || fresh.Inquiry.CommentCatalog.MustExtension(ext.Address()).Version != "1.2" {
				t.Fatal("old frontier mutated during update")
			}
			assert := func(source *exec.Source, oid ids.OID, version string) {
				if oid == ids.ExtensionClassID {
					if source.Count() != 1 {
						t.Fatal("extension frontier count")
					}
					got, err := source.Anonymous[0][5].Text()
					if err != nil || got != version {
						t.Fatal("extension frontier", got, version, err)
					}
					return
				}
				count := 0
				seen := map[string]bool{}
				for _, row := range source.Anonymous {
					name, err := row[0].Text()
					if err != nil {
						t.Fatal(err)
					}
					if name != "fuzzystrmatch" {
						continue
					}
					count++
					if oid == 12081 {
						def, _ := row[1].Text()
						installed, _ := row[2].Text()
						if def != "1.2" || installed != version {
							t.Fatal("default frontier", def, installed, version)
						}
					} else {
						v, _ := row[1].Text()
						installed, _ := row[2].Boolean()
						if (v != "1.1" && v != "1.2") || seen[v] || installed != (v == version) {
							t.Fatal("versions frontier", v, installed, version)
						}
						seen[v] = true
					}
				}
				want := 1
				if oid == 12085 {
					want = 2
				}
				if count != want {
					t.Fatal("frontier multiplicity", oid, count, want)
				}
			}
			for _, oid := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
				assert(sources[oid], oid, "1.1")
				assert(open(fresh, oid), oid, "1.2")
			}
		})
	}
}

func extensionUnitExecute(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) error {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		// A malformed unit AST or an unexpected engine panic must not leave
		// statement admission held while roleOperation closes the session.
		// Preserve the panic; this defer only performs the normal scope unwind.
		if !finished {
			_ = scope.Finish(errors.New("stuntgres: extension unit statement unwound"))
		}
	}()
	_, err = scope.Execute(stmt, 0, nil)
	finished = true
	return scope.Finish(err)
}
