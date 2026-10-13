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
			if sources[12081].count() != 1 || !sources[12081].anonymous[0][2].IsNull() {
				t.Error("old statement availability must have NULL installed_version")
			}
			if sources[12085].count() != 1 {
				t.Fatal("old statement version inventory changed")
			}
			if installed, err := sources[12085].anonymous[0][2].Boolean(); err != nil || installed {
				t.Error("old statement version must be uninstalled", installed, err)
			}
			if sources[ExtensionClassID].count() != 0 {
				t.Error("old statement pg_extension must have no installed row")
			}
			for _, oid := range []OID{12081, 12085, ExtensionClassID} {
				source := open(fresh, oid)
				if source.count() != 1 {
					t.Fatal("fresh statement must see genuine installation", oid, source.count())
				}
				index := 2
				if oid == ExtensionClassID {
					index = 5
				}
				if oid == 12085 {
					if installed, err := source.anonymous[0][index].Boolean(); err != nil || !installed {
						t.Fatal("fresh installed flag", installed, err)
					}
				} else if version, err := source.anonymous[0][index].Text(); err != nil || version != "1.1" {
					t.Fatal("fresh installed version", oid, version, err)
				}
			}
		})
	}
}
