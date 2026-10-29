package control

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestArrayNameTraceFiniteUTF8(t *testing.T) {
	parents := []string{"lo", "_lo_1", strings.Repeat("_", 63), strings.Repeat("é", 30) + "ab", strings.Repeat("界", 20) + "ab", strings.Repeat("😀", 15) + "ab", strings.Repeat("é", 31) + "a"}
	histories := [][]uint64{{0}, {1}, {12}, {0, 0}, {1, 0}, {0, 1}, {0, 12, 0, 1}, {0, 0, 0, 0, 0, 0}, {1, 0, 12, 0, 0, 1}}
	for _, parent := range parents {
		for _, history := range histories {
			target := parent
			for _, suffix := range history {
				target = typedef.AssociatedArrayNameCandidate(target, suffix)
			}
			// A scalar and its array cannot share a key. Unsuffixed all-underscore
			// fixed points are mathematical candidates, not legal array identities.
			if target == parent {
				continue
			}
			trace, err := arrayNameTrace(parent, target)
			if err != nil {
				t.Fatalf("parent=%q target=%q: %v", parent, target, err)
			}
			actual := parent
			for _, suffix := range trace {
				actual = typedef.AssociatedArrayNameCandidate(actual, suffix)
			}
			if actual != target || len(actual) > 63 || !utf8.ValidString(actual) {
				t.Fatalf("trace=%v actual=%q target=%q", trace, actual, target)
			}
		}
	}
	if _, err := arrayNameTrace("lo", "unrelated"); err == nil {
		t.Fatal("unreachable identity accepted")
	}
}

func TestArrayIdentityReplayRealOwnerModel(t *testing.T) {
	for _, test := range []struct{ name, parent, target string }{
		{"triple", "lo", "___lo"}, {"numeric", "lo", "_lo_1"}, {"mixed", "lo", "__lo_1"},
		{"utf8", strings.Repeat("é", 30) + "ab", "__" + strings.Repeat("é", 29) + "_1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
			integer, ok := source.CatalogView.LookupType(sqltype.Int4)
			if !ok {
				t.Fatal("missing integer")
			}
			domain, err := sqltype.Domain(70000, test.parent, "public."+test.parent, integer.Usage())
			if err != nil {
				t.Fatal(err)
			}
			namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			parent := &catalog.Type{SQLType: domain, ObjectNamespace: namespace, ObjectRevision: 1, Owner: source.Bootstrap, Array: 70001}
			p := newInstallationTypeReplay(source.CatalogView, context.Background())
			if err := p.names.Reserve(parent); err != nil {
				t.Fatal(err)
			}
			p.names.Associate(parent)
			originalCount := len(p.names.Names)
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView}, installationTypeReplay: p}
			var statements []string
			if err := script.replayArrayName("public", parent, test.target, &statements); err != nil {
				t.Fatal(err)
			}
			if p.names.Names[parent.Array].Name != test.target || p.names.Names[domain.ID()].Name != test.parent || p.names.Arrays[domain.ID()] != parent.Array || len(p.names.Names) != originalCount {
				t.Fatal("final identities or retired helper occupancy diverged", p.names.Names[parent.Array])
			}
			sql := strings.Join(statements, "")
			if strings.Count(sql, "CREATE DOMAIN ") != strings.Count(sql, "DROP DOMAIN ") {
				t.Fatal("helper domain/array leaked", sql)
			}
			if strings.Contains(sql, "ALTER TYPE "+replayTypeSQL("public", test.target)+" ") {
				t.Fatal("generated array mutated directly", sql)
			}
			if len(statements) == 0 {
				t.Fatal("identity was fabricated without real SQL")
			}
		})
	}
}
