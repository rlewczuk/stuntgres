package core_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// A released composite is created by its installation even after detachment;
// an ordinary composite is still created by its own event after foreign ADD.
// Both have backing relations, but mutable membership cannot select the creator.
func TestExtensionOwnershipCrosstabReplayCreators(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign_%t", foreign), func(t *testing.T) {
			source := engine.New()
			op, done := enginetest.RoleOperation(t, source, "postgres")
			defer done()
			for _, q := range []string{
				"CREATE SCHEMA installed",
				"CREATE SCHEMA moved",
				"CREATE EXTENSION tablefunc SCHEMA installed",
				`CREATE EXTENSION "uuid-ossp"`,
				"CREATE TYPE public.ordinary_member AS (v text)",
				`ALTER EXTENSION "uuid-ossp" ADD TYPE public.ordinary_member`,
				"CREATE VIEW public.ordinary_consumer AS SELECT NULL::public.ordinary_member AS v",
			} {
				enginetest.RoleExecute(t, source, op, q)
			}
			installed := source.CatalogRead()
			ext := installed.MustExtension(installed.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}))
			if len(ext.Installation.Declarations) != 16 {
				t.Fatal("incomplete genuine tablefunc recipe")
			}
			for n := 2; n <= 4; n++ {
				for _, q := range []string{
					fmt.Sprintf("ALTER EXTENSION tablefunc DROP FUNCTION installed.crosstab%d(text)", n),
					fmt.Sprintf("ALTER EXTENSION tablefunc DROP TYPE installed.tablefunc_crosstab_%d", n),
					fmt.Sprintf("ALTER TYPE installed.tablefunc_crosstab_%d SET SCHEMA moved", n),
					fmt.Sprintf("CREATE VIEW public.named_consumer_%d AS SELECT NULL::moved.tablefunc_crosstab_%d AS v", n, n),
				} {
					enginetest.RoleExecute(t, source, op, q)
				}
				if foreign {
					enginetest.RoleExecute(t, source, op, fmt.Sprintf(`ALTER EXTENSION "uuid-ossp" ADD TYPE moved.tablefunc_crosstab_%d`, n))
				}
			}
			enginetest.RoleExecute(t, source, op, "DROP EXTENSION tablefunc")
			captured := source.CatalogRead()
			var output bytes.Buffer
			if err := engine.Dump(context.Background(), source, &output); err != nil {
				t.Fatal("duplicate released composite creator", err)
			}
			if strings.Count(output.String(), `CREATE TYPE "public"."ordinary_member" AS`) != 1 || strings.Contains(output.String(), `CREATE TYPE "moved"."tablefunc_crosstab_`) {
				t.Fatal("creator selection lost ordinary member or duplicated released declaration")
			}
			for _, d := range captured.Descriptors() {
				relation, ok := d.(*catalog.Relation)
				if !ok || relation.RelationKind != catalog.CompositeRelation {
					continue
				}
				typ := captured.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
				predecessor, err := (control.Resolver{}).Predecessor(captured, relation.Address(), ids.ObjectAddress{})
				if err != nil {
					t.Fatal(err)
				}
				if catalog.DescriptorOrigin(typ) != nil {
					if predecessor != 0 {
						t.Fatal("released backing relation acquired an ordinary creator event")
					}
				} else if predecessor != relation.Address().ObjectID {
					t.Fatal("ordinary member lost its own creator event")
				}
			}
			target := engine.New()
			if err := engine.Restore(context.Background(), target, bytes.NewReader(output.Bytes())); err != nil {
				t.Fatal(err)
			}
			view := target.CatalogRead()
			if view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}) != (ids.ObjectAddress{}) {
				t.Fatal("replay resurrected retired origin extension")
			}
			ns := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "moved"})
			for n := 2; n <= 4; n++ {
				typ := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: fmt.Sprintf("tablefunc_crosstab_%d", n)}))
				array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
				relation := view.MustRelation(typ.OwningRelation)
				if catalog.DescriptorOrigin(typ) == nil || relation.RowType != typ.SQLType.ID() || array.SQLType.Element().ID() != typ.SQLType.ID() || array.Namespace() != ns || (typ.Extension != (ids.ObjectAddress{})) != foreign || array.Extension != typ.Extension {
					t.Fatal("lost released root/array/backing relation or current membership", n)
				}
			}
			if source.CatalogRead().State() != captured.State() {
				t.Fatal("dump mutated captured source")
			}
		})
	}
}
