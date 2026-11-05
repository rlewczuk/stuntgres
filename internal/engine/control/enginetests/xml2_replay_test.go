package enginetests

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// The actual provider, ordinary SQL publication and complete dump schedule own
// these declarations. In particular, no test constructs the SQL wrapper bodies.
func TestInstallationReplayNonrelocatablePlacement(t *testing.T) {
	for _, tc := range []struct {
		name, schema      string
		present, detached bool
		catalog           bool
	}{
		{"public", "public", true, false, false},
		{"quoted", "XML2 control 雪", true, false, false},
		{"renamed_detached", "XML2 control 雪", true, true, false},
		{"absent_header", "XML2 control 雪", false, true, false},
		{"relocated_catalog", "moved_catalog", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := engine.New()
			op, done := enginetest.RoleOperation(t, source, "postgres")
			enginetest.RoleExecute(t, source, op, "CREATE ROLE replay_placement_owner")
			installSchema := tc.schema
			if tc.catalog {
				installSchema = "pg_catalog"
			} else if tc.schema != "public" {
				enginetest.RoleExecute(t, source, op, "CREATE SCHEMA "+catalog.QuoteIdentifier(tc.schema))
			}
			enginetest.RoleExecute(t, source, op, "CREATE EXTENSION xml2 WITH SCHEMA "+catalog.QuoteIdentifier(installSchema))
			if tc.catalog {
				enginetest.RoleExecute(t, source, op, "ALTER SCHEMA pg_catalog RENAME TO moved_catalog")
			}
			qualified := catalog.QuoteIdentifier(tc.schema) + "."
			if tc.detached {
				enginetest.RoleExecute(t, source, op, "ALTER EXTENSION xml2 DROP FUNCTION "+qualified+"xpath_string(text,text)")
				if !tc.present {
					enginetest.RoleExecute(t, source, op, "DROP EXTENSION xml2")
				}
				enginetest.RoleExecute(t, source, op, "ALTER FUNCTION "+qualified+"xpath_string(text,text) RENAME TO retained_xml2_text")
				enginetest.RoleExecute(t, source, op, "ALTER FUNCTION "+qualified+"retained_xml2_text(text,text) OWNER TO replay_placement_owner")
			}
			done()

			view := source.CatalogRead()
			namespace := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: tc.schema})
			if namespace == (ids.ObjectAddress{}) || tc.catalog && namespace.ObjectID != ids.PGCatalogNamespaceOID {
				t.Fatal("actual installation namespace identity lost", namespace)
			}
			survivorName := "xpath_string"
			if tc.detached {
				survivorName = "retained_xml2_text"
			}
			address := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: survivorName, Signature: "25,25"})
			survivor, ok := view.Procedure(address)
			if !ok || extension.StockXML2.ValidateProcedure(view, survivor) != nil || !survivor.Origin.Valid(address) {
				t.Fatal("source lacks genuine installed survivor", survivor)
			}
			recipe := survivor.Origin.Installation
			if len(recipe.Members) != 10 || len(recipe.Declarations) != 10 || recipe.Declarations[1].Name() != "xpath_string" || recipe.Declarations[4].Name() != "xpath_table" {
				t.Fatal("source lost complete canonical ten-member cohort", recipe)
			}
			members := slices.Clone(recipe.Members)
			declarations := slices.Clone(recipe.Declarations)
			before := make(map[ids.ObjectAddress]catalog.Procedure)
			originals := make(map[ids.ObjectAddress]*catalog.Procedure)
			for _, descriptor := range view.Descriptors() {
				p, ok := descriptor.(*catalog.Procedure)
				if !ok || extension.XML2ProviderOf(p) != extension.StockXML2 {
					continue
				}
				if p.Origin.Installation != recipe || extension.StockXML2.ValidateProcedure(view, p) != nil {
					t.Fatal("source contains a foreign or invalid cohort", p)
				}
				before[p.Address()], originals[p.Address()] = *p, p
			}
			wantCount := 10
			if !tc.present {
				wantCount = 1
			}
			if len(before) != wantCount {
				t.Fatal("source current member inventory", len(before), wantCount)
			}

			var out bytes.Buffer
			if err := engine.Dump(context.Background(), source, &out); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			prefix := "CREATE EXTENSION \"xml2\" WITH SCHEMA "
			var create, creation string
			for _, line := range strings.SplitAfter(text, "\n") {
				if strings.HasPrefix(line, prefix) {
					if create != "" || !strings.HasSuffix(line, " VERSION '1.2';\n") {
						t.Fatal("noncanonical or repeated cohort creation", text)
					}
					create = line
					creation = strings.TrimSuffix(strings.TrimPrefix(line, prefix), " VERSION '1.2';\n")
				}
			}
			if create == "" || strings.Contains(text, "ALTER EXTENSION \"xml2\" SET SCHEMA") || strings.Contains(text, "DROP SCHEMA "+catalog.QuoteIdentifier(tc.schema)+";\n") {
				t.Fatal("invalid nonrelocatable placement schedule", text)
			}
			if tc.present {
				if creation != catalog.QuoteIdentifier(tc.schema) || strings.Contains(text, "__stuntgres_install_") || strings.Contains(text, "DROP EXTENSION \"xml2\"") {
					t.Fatal("surviving installation treated as scratch", text)
				}
				// A user target is created once by ordinary namespace replay,
				// before installation; builtin targets are never recreated.
				count := 0
				if tc.schema != "public" && !tc.catalog {
					count = 1
				}
				createSchema := "CREATE SCHEMA " + catalog.QuoteIdentifier(tc.schema)
				if strings.Count(text, createSchema) != count || count == 1 && strings.Index(text, createSchema) >= strings.Index(text, create) {
					t.Fatal("target namespace replay repeated or misordered", text)
				}
			} else {
				created := strings.Index(text, "CREATE SCHEMA "+creation+" AUTHORIZATION \"postgres\";\n")
				headerDrop := strings.Index(text, "DROP EXTENSION \"xml2\";\n")
				schemaDropSQL := "DROP SCHEMA " + creation + ";\n"
				schemaDrop := strings.Index(text, schemaDropSQL)
				if !strings.HasPrefix(creation, "\"__stuntgres_install_") || creation == catalog.QuoteIdentifier(tc.schema) || created < 0 || created >= strings.Index(text, create) || headerDrop <= strings.Index(text, create) || schemaDrop <= headerDrop || strings.Count(text, schemaDropSQL) != 1 || strings.Contains(text, "ALTER EXTENSION \"xml2\" ADD ") {
					t.Fatal("absent-header scratch lifecycle lost", text)
				}
			}
			if tc.detached {
				detach := strings.Index(text, "ALTER EXTENSION \"xml2\" DROP FUNCTION "+creation+".\"xpath_string\"")
				rename := strings.Index(text, " RENAME TO \"retained_xml2_text\";\n")
				owner := strings.Index(text, "ALTER FUNCTION "+qualified+"\"retained_xml2_text\"")
				if detach < 0 || rename <= detach || owner <= rename || !strings.Contains(text[owner:], " OWNER TO \"replay_placement_owner\";\n") {
					t.Fatal("detach/rename/current-authority schedule lost", text)
				}
				for _, line := range strings.Split(text, "\n") {
					if strings.HasPrefix(line, "ALTER EXTENSION ") && strings.Contains(line, " ADD FUNCTION ") && strings.Contains(line, "retained_xml2_text") {
						t.Fatal("detached survivor was reattached", line)
					}
				}
			}
			if tc.catalog {
				preamble := "ALTER SCHEMA \"pg_catalog\" RENAME TO \"moved_catalog\";\n"
				if strings.Count(text, preamble) != 1 || strings.Index(text, preamble) >= strings.Index(text, create) {
					t.Fatal("authentic builtin namespace preamble lost", text)
				}
			}
			for address, p := range originals {
				if view.Get(address) != p || !reflect.DeepEqual(before[address], *p) || p.Origin.Installation != recipe {
					t.Fatal("dump mutated immutable source declaration", address)
				}
			}
			if !slices.Equal(recipe.Members, members) || !slices.Equal(recipe.Declarations, declarations) || recipe.Declarations[1].Name() != "xpath_string" {
				t.Fatal("dump mutated sealed canonical recipe")
			}

			target := engine.New()
			targetOp, targetDone := enginetest.RoleOperation(t, target, "postgres")
			enginetest.RoleExecute(t, target, targetOp, "CREATE ROLE replay_placement_owner")
			targetDone()
			if err := engine.Restore(context.Background(), target, bytes.NewReader(out.Bytes())); err != nil {
				t.Fatal(err)
			}
			restored := target.CatalogRead()
			restoredNS := restored.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: tc.schema})
			if restoredNS == (ids.ObjectAddress{}) || tc.catalog && restoredNS.ObjectID != ids.PGCatalogNamespaceOID {
				t.Fatal("restore changed retained namespace identity", restoredNS)
			}
			restoredAddress := restored.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: restoredNS, Name: survivorName, Signature: "25,25"})
			p, ok := restored.Procedure(restoredAddress)
			if !ok || extension.StockXML2.ValidateProcedure(restored, p) != nil || p.Origin.Installation == recipe || len(p.Origin.Installation.Declarations) != 10 || len(p.Origin.Installation.Members) != 10 {
				t.Fatal("restore lost genuine remapped complete cohort", p)
			}
			header := restored.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"})
			if (header != (ids.ObjectAddress{})) != tc.present || (p.Extension == (ids.ObjectAddress{})) != tc.detached {
				t.Fatal("restore changed header presence or detached membership", header, p)
			}
			if tc.detached {
				owner, ok := target.Roles().Lookup("replay_placement_owner")
				if !ok || p.Owner != owner.Ref {
					t.Fatal("restore lost current survivor owner", p)
				}
			}
			count := 0
			for _, descriptor := range restored.Descriptors() {
				member, ok := descriptor.(*catalog.Procedure)
				if !ok || extension.XML2ProviderOf(member) != extension.StockXML2 {
					continue
				}
				count++
				if member.Origin.Installation != p.Origin.Installation || extension.StockXML2.ValidateProcedure(restored, member) != nil || member.Namespace() != restoredNS {
					t.Fatal("restore lost current declaration placement/cohort", member)
				}
			}
			if count != wantCount {
				t.Fatal("restore changed current member inventory", count, wantCount)
			}
		})
	}
}
