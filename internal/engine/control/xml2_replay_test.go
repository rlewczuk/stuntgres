package control

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestInstallationReplayNonrelocatablePlacement(t *testing.T) {
	for _, tc := range []struct {
		name, schema      string
		present, detached bool
		namespace         ids.OID
	}{
		{"public", "public", true, false, ids.PublicNamespaceOID},
		{"quoted", "XML2 control 雪", true, false, 900001},
		{"renamed_detached", "XML2 control 雪", true, true, 900001},
		{"absent_header", "XML2 control 雪", false, true, 900001},
		{"relocated_catalog", "moved_catalog", true, false, ids.PGCatalogNamespaceOID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			source.CatalogView.Owner().Extensions = map[string]catalog.ExtensionImplementation{"xml2": extension.StockXML2}
			catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
			namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: tc.namespace}
			if tc.namespace == ids.PGCatalogNamespaceOID {
				original := source.CatalogView.MustNamespace(namespace)
				relocated := *original
				relocated.ObjectName = tc.schema
				source.State.UnbindName(catalog.DescriptorNameKey(original))
				source.State.SetDescriptor(namespace, &relocated)
				source.State.BindName(catalog.DescriptorNameKey(&relocated), namespace)
				preamble, err := dumpBuiltinSchemaPreamble(source.State, source.DomainRoles)
				if err != nil || !slices.Equal(preamble, []string{"ALTER SCHEMA \"pg_catalog\" RENAME TO \"moved_catalog\";\n"}) {
					t.Fatal("authentic namespace preamble", preamble, err)
				}
			} else if tc.namespace != ids.PublicNamespaceOID {
				ns := &catalog.Namespace{ObjectAddress: namespace, ObjectRevision: 1, ObjectName: tc.schema, Owner: source.Bootstrap}
				source.State.SetDescriptor(namespace, ns)
				source.State.BindName(catalog.DescriptorNameKey(ns), namespace)
			}
			ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 50000}, ObjectRevision: 1, ObjectName: "xml2", Version: "1.2", Owner: source.Bootstrap, TargetNamespace: namespace}
			names := []string{"xml_valid", "xpath_string", "xpath_number", "xpath_bool"}
			results := []ids.OID{16, 25, 700, 16}
			var members []ids.ObjectAddress
			var declarations []catalog.Descriptor
			for slot, name := range names {
				a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 50001 + ids.OID(slot)}
				signature, volatility := "25,25", expr.Immutable
				display := catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/pgxml", HasBinary: true}
				if slot == 0 {
					signature = "25"
					volatility = expr.Stable
					display = catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "xml_is_well_formed"}
				}
				declarations = append(declarations, &catalog.Procedure{ObjectAddress: a, ObjectNamespace: namespace, ObjectRevision: 1, ObjectName: name, Extension: ext.Address(), Owner: source.Bootstrap, Signature: catalog.ProcedureSignature(signature), Result: results[slot], RoutineKind: catalog.FunctionRoutine, Volatility: volatility, Strict: true, Installed: catalog.InstalledRoutineDeclaration{Parallel: 's'}, Display: display, Implementation: extension.StockXML2})
				members = append(members, a)
			}
			recipe := catalog.NewInstallation(ext.Address(), "xml2", "1.2", extension.StockXML2, members)
			recipe.Declarations = declarations
			ext.Installation = recipe
			if tc.present {
				ext.Members = slices.Clone(members)
				if tc.detached {
					ext.Members = slices.Delete(ext.Members, 1, 2)
				}
				source.State.SetDescriptor(ext.Address(), ext)
				source.State.BindName(catalog.DescriptorNameKey(ext), ext.Address())
			}
			var survivor *catalog.Procedure
			for slot, d := range declarations {
				if !tc.present && slot != 1 {
					continue
				}
				p := *d.(*catalog.Procedure)
				p.Origin = &catalog.InstalledOrigin{Installation: recipe, Declaration: slot}
				if slot == 1 && tc.detached {
					p.ObjectName = "retained_xml2_text"
					p.Extension = ids.ObjectAddress{}
				}
				source.State.SetDescriptor(p.Address(), &p)
				source.State.BindName(catalog.DescriptorNameKey(&p), p.Address())
				if slot == 1 {
					survivor = &p
				}
			}
			if err := extension.StockXML2.ValidateProcedure(source.CatalogView, survivor); err != nil {
				t.Fatal("fixture lacks authentic sealed declaration", err)
			}
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog, DomainRoles: source.DomainRoles, OriginalRoles: source.OriginalRoles}}
			items, err := script.installations()
			if err != nil || len(items) != 1 {
				t.Fatal("real origin cohort", items, err)
			}
			schedule, err := script.newInstallationSchedule(items)
			if err != nil {
				t.Fatal(err)
			}
			creation := items[0].schema
			if items[0].recipe != recipe || tc.present && (items[0].ext != ext || creation != tc.schema || !items[0].retainedSchema) || !tc.present && (items[0].ext != nil || items[0].retainedSchema || creation == tc.schema) {
				t.Fatal("creation authority", items[0])
			}
			if err := schedule.install(0); err != nil {
				t.Fatal(err)
			}
			if !schedule.installed[0] || schedule.currentSchema[0] != creation || schedule.active["xml2"] != 0 {
				t.Fatal("real schedule lost cohort", schedule.currentSchema)
			}
			// Exercise the ordinary complete detach/placement/owner renderer too.
			script = &dumpScript{Capture: script.Capture}
			if err := script.prepareInstallationReplay(context.Background()); err != nil {
				t.Fatal(err)
			}
			text := strings.Join(script.installationStatements, "")
			create := "CREATE EXTENSION \"xml2\" WITH SCHEMA " + catalog.QuoteIdentifier(creation) + " VERSION '1.2';\n"
			if !strings.Contains(text, create) || strings.Contains(text, "ALTER EXTENSION \"xml2\" SET SCHEMA") || strings.Contains(text, "DROP SCHEMA "+catalog.QuoteIdentifier(tc.schema)+";\n") {
				t.Fatal("invalid nonrelocatable header replay", text)
			}
			if tc.present {
				if strings.Contains(text, "CREATE SCHEMA "+catalog.QuoteIdentifier(tc.schema)) || strings.Contains(text, "__stuntgres_install_") || strings.Contains(text, "DROP EXTENSION \"xml2\"") {
					t.Fatal("surviving target treated as scratch", text)
				}
			} else {
				created := strings.Index(text, "CREATE SCHEMA "+catalog.QuoteIdentifier(creation))
				headerDrop := strings.Index(text, "DROP EXTENSION \"xml2\";\n")
				schemaDrop := strings.Index(text, "DROP SCHEMA "+catalog.QuoteIdentifier(creation)+";\n")
				if created < 0 || headerDrop <= strings.Index(text, create) || schemaDrop <= headerDrop || strings.Count(text, "DROP SCHEMA "+catalog.QuoteIdentifier(creation)+";\n") != 1 || len(script.membershipStatements) != 0 {
					t.Fatal("absent-header scratch lifecycle", text)
				}
			}
			if tc.detached {
				detach := strings.Index(text, "ALTER EXTENSION \"xml2\" DROP FUNCTION "+catalog.QuoteIdentifier(creation)+".\"xpath_string\"")
				rename := strings.Index(text, " RENAME TO \"retained_xml2_text\";")
				owner := strings.Index(text, "ALTER FUNCTION "+catalog.QuoteIdentifier(tc.schema)+".\"retained_xml2_text\"")
				if detach < 0 || rename <= detach || owner <= rename {
					t.Fatal("rename/detach/authority order", text)
				}
				for _, membership := range script.membershipStatements {
					if strings.Contains(membership, "retained_xml2_text") {
						t.Fatal("detached scalar reattached", membership)
					}
				}
			}
			if source.CatalogView.Get(survivor.Address()) != survivor || survivor.Origin.Installation != recipe || recipe.Declarations[1].Name() != "xpath_string" {
				t.Fatal("rendering mutated sealed source")
			}
		})
	}
}
