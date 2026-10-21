package access

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestRowSecurityKindDelegatesOtherKinds(t *testing.T) {
	relation := func(kind catalog.RelationKind) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}, ObjectName: "r", RelationKind: kind}
	}
	fallback := errors.New("fallback")
	var calls []string
	tableKind := func(d *catalog.Relation, operation string) error {
		calls = append(calls, operation)
		return fallback
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation} {
		if err := RowSecurityKind(relation(kind), parser.EnableRowSecurity, tableKind); err != nil {
			t.Fatal(kind, err)
		}
	}
	var e *sqlstate.Error
	err := RowSecurityKind(relation(catalog.ViewRelation), parser.ForceRowSecurity, tableKind)
	if !errors.As(err, &e) || e.Code != "42809" || e.Message != `ALTER action FORCE ROW SECURITY cannot be performed on relation "r"` || e.Detail != "This operation is not supported for views." {
		t.Fatal(err)
	}
	for _, kind := range []catalog.RelationKind{catalog.CatalogRelation, catalog.ForeignRelation} {
		if err := RowSecurityKind(relation(kind), parser.NoForceRowSecurity, tableKind); err != fallback {
			t.Fatal(kind, err)
		}
	}
	if len(calls) != 2 || calls[0] != "NO FORCE ROW SECURITY" || calls[1] != "NO FORCE ROW SECURITY" {
		t.Fatal(calls)
	}
}
