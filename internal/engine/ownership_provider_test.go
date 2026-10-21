package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDropOwnedUnionCanonicalExtensionMembers(t *testing.T) {
	for index := range 6 {
		for _, mode := range []string{"RESTRICT", "CASCADE"} {
			t.Run(string(rune('a'+index))+"/"+mode, func(t *testing.T) {
				e, op, extension := extensionUnitInstalled(t)
				source := roleAttributeCreate(t, e, op, "own_source", false, parser.RoleOptions{})
				target := roleAttributeCreate(t, e, op, "own_target", false, parser.RoleOptions{})
				original := e.CatalogRead()
				member := original.Get(extension.Members[index])
				owner, _, _ := access.DescriptorACL(member)
				stage := e.BeginCatalogStage(original)
				defer e.DiscardCatalogStage(stage)
				if owner == (authorization.PrincipalRef{}) {
					if member.Kind() != ids.TSTemplateObject {
						t.Fatal("unexpected ownerless member", member)
					}
					if err := role.StageOwnership(stage, member, source.Ref); err == nil {
						t.Fatal("template acquired an owner")
					}
					if e.catalog.state != original.State() || stage.Get(member.Address()) != nil {
						t.Fatal("ownerless template mutation")
					}
					return
				}
				if err := role.StageOwnership(stage, member, source.Ref); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, stage)
				sourceGeneration := e.catalog.state.Get(member.Address())
				if result, err := e.Execute(op, &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "own_source"}}, Target: parser.RoleSpec{Name: "own_target"}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
					t.Fatal("canonical member reassignment", result, err)
				}
				reassigned := e.catalog.state.Get(member.Address())
				actual, _, _ := access.DescriptorACL(reassigned)
				if actual != target.Ref || e.catalog.state.Get(extension.Address()) != extension || sourceGeneration == reassigned {
					t.Fatal("independent member reassignment")
				}
				previous, _, _ := access.DescriptorACL(sourceGeneration)
				if previous != source.Ref {
					t.Fatal("old member generation mutated")
				}
				committed, roles := e.catalog.state, e.roles.published
				dropMode := parser.DropRestrict
				if mode == "CASCADE" {
					dropMode = parser.DropCascade
				}
				result, err := e.Execute(op, &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "own_target"}}, Mode: dropMode}, 0, nil)
				var diagnostic *sqlstate.Error
				noun := ""
				switch d := member.(type) {
				case *catalog.Procedure:
					switch d.Signature {
					case catalog.UnaccentTextSignature:
						noun = "function unaccent(text)"
					case catalog.UnaccentDictionarySignature:
						noun = "function unaccent(regdictionary,text)"
					case catalog.UnaccentInitSignature:
						noun = "function unaccent_init(internal)"
					case catalog.UnaccentLexizeSignature:
						noun = "function unaccent_lexize(internal,internal,internal,internal)"
					default:
						t.Fatal("unexpected real procedure", d)
					}
				case *catalog.TSDictionary:
					noun = "text search dictionary unaccent"
				default:
					t.Fatal("unexpected owned member", member)
				}
				expected := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + noun + " because extension unaccent requires it", Hint: "You can drop extension unaccent instead."}
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, expected) || result.Count != 0 || result.Rows != nil || e.catalog.state != committed || e.roles.published != roles {
					t.Fatal("foreign member refusal/atomicity", result, err, diagnostic)
				}
				// Include the extension owner as an actual second source. Its canonical
				// e-edge includes every member, irrespective of each independent owner.
				extensionOwner := roleAttributeCreate(t, e, op, "own_extension", false, parser.RoleOptions{})
				transfer := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(transfer)
				if err := role.StageOwnership(transfer, extension, extensionOwner.Ref); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, transfer)
				if result, err := e.Execute(op, &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "own_extension"}, {Name: "own_target"}}, Mode: dropMode}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
					t.Fatal("canonical owner union", result, err)
				}
				if e.catalog.state.Get(extension.Address()) != nil {
					t.Fatal("extension survived complete union")
				}
				for _, address := range extension.Members {
					if e.catalog.state.Get(address) != nil {
						t.Fatal("canonical member survived", address)
					}
				}
				for _, address := range extension.Members {
					if original.Get(address) == nil {
						t.Fatal("retained member generation mutated", address)
					}
				}
			})
		}
	}
}
