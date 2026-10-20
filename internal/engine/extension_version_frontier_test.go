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

func extensionVersionAvailableFrontier(t *testing.T) {
	for _, first := range []ids.OID{12081, 12085, ids.ExtensionClassID} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			e, publisher, ext := extensionVersionUnitInstalled(t, "1.1")
			op, done := roleOperation(t, e, "postgres")
			defer done()
			capture := func() *StatementScope {
				s := &StatementScope{op: op, owner: op.session.transactions}
				e.mu.Lock()
				err := e.captureCommentSnapshotLocked(s)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			open := func(s *StatementScope, oid ids.OID) *exec.Source {
				_, ref := availableUnitSource(e, oid)
				e.mu.Lock()
				source, err := e.openRelationSourceLocked(s, ref, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
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
			if !reflect.DeepEqual(retained, sources[first].Anonymous) || old.inquiry.commentCatalog.MustExtension(ext.Address()) != ext || fresh.inquiry.commentCatalog.MustExtension(ext.Address()).Version != "1.2" {
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
