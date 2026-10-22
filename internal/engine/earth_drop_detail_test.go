package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

// Test the diagnostic projection directly over genuine installed descriptors;
// public SQL removal/enforcement/atomicity belongs to the reference scenario.
func TestEarthPrerequisiteRemovalDetail(t *testing.T) {
	e, earth, domain := earthUnitInstalled(t)
	catalogView := e.CatalogRead()
	cube := catalogView.MustExtension(catalogView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
	reader := &dropProvider{owner: e, base: catalogView, ctx: context.Background()}
	extensionEdge := table.DomainDependency{Address: earth.Address(), Target: cube.Address(), Label: "extension earthdistance"}
	memberEdge := table.DomainDependency{Address: domain.Address(), Target: cube.Members[0], Label: "type earth"}
	for _, tc := range []struct {
		name   string
		edges  []table.DomainDependency
		detail string
	}{
		{"extension_before_member", []table.DomainDependency{extensionEdge, memberEdge}, "extension earthdistance depends on extension cube"},
		{"extension_after_member", []table.DomainDependency{memberEdge, extensionEdge}, "extension earthdistance depends on extension cube"},
		{"member_without_reported_owner", []table.DomainDependency{memberEdge}, "type earth depends on type cube"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]table.DomainDependency(nil), tc.edges...)
			err := view.RestrictError(reader, []ids.ObjectAddress{cube.Address()}, tc.edges, 1, cube.Address())
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop extension cube because other objects depend on it", Detail: tc.detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("restriction projection got %#v want %#v", got, want)
			}
			if !reflect.DeepEqual(tc.edges, before) {
				t.Fatal("diagnostic projection changed closure/enforcement edges")
			}
		})
	}
}
