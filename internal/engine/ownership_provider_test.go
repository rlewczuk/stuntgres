package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
				member := original.state.descriptors[extension.members[index]]
				owner, _, _ := descriptorACL(member)
				stage := e.BeginCatalogStage(original)
				defer e.DiscardCatalogStage(stage)
				if owner == (authorization.PrincipalRef{}) {
					if member.Kind() != ids.TSTemplateObject {
						t.Fatal("unexpected ownerless member", member)
					}
					if err := stageOwnership(stage, member, source.Ref); err == nil {
						t.Fatal("template acquired an owner")
					}
					if e.state != original.state || stage.descriptors[member.Address()] != nil {
						t.Fatal("ownerless template mutation")
					}
					return
				}
				if err := stageOwnership(stage, member, source.Ref); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, stage)
				sourceGeneration := e.state.descriptors[member.Address()]
				if result, err := e.Execute(op, &parser.ReassignOwned{Sources: []parser.RoleSpec{{Name: "own_source"}}, Target: parser.RoleSpec{Name: "own_target"}}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
					t.Fatal("canonical member reassignment", result, err)
				}
				reassigned := e.state.descriptors[member.Address()]
				actual, _, _ := descriptorACL(reassigned)
				if actual != target.Ref || e.state.descriptors[extension.address] != extension || sourceGeneration == reassigned {
					t.Fatal("independent member reassignment")
				}
				previous, _, _ := descriptorACL(sourceGeneration)
				if previous != source.Ref {
					t.Fatal("old member generation mutated")
				}
				committed, roles := e.state, e.roles
				dropMode := parser.DropRestrict
				if mode == "CASCADE" {
					dropMode = parser.DropCascade
				}
				result, err := e.Execute(op, &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "own_target"}}, Mode: dropMode}, 0, nil)
				var diagnostic *sqlstate.Error
				noun := ""
				switch d := member.(type) {
				case *procedureDescriptor:
					switch d.signature {
					case unaccentTextSignature:
						noun = "function unaccent(text)"
					case unaccentDictionarySignature:
						noun = "function unaccent(regdictionary,text)"
					case unaccentInitSignature:
						noun = "function unaccent_init(internal)"
					case unaccentLexizeSignature:
						noun = "function unaccent_lexize(internal,internal,internal,internal)"
					default:
						t.Fatal("unexpected real procedure", d)
					}
				case *tsDictionaryDescriptor:
					noun = "text search dictionary unaccent"
				default:
					t.Fatal("unexpected owned member", member)
				}
				expected := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + noun + " because extension unaccent requires it", Hint: "You can drop extension unaccent instead."}
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, expected) || result.Count != 0 || result.Rows != nil || e.state != committed || e.roles != roles {
					t.Fatal("foreign member refusal/atomicity", result, err, diagnostic)
				}
				// Include the extension owner as an actual second source. Its canonical
				// e-edge includes every member, irrespective of each independent owner.
				extensionOwner := roleAttributeCreate(t, e, op, "own_extension", false, parser.RoleOptions{})
				transfer := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(transfer)
				if err := stageOwnership(transfer, extension, extensionOwner.Ref); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, transfer)
				if result, err := e.Execute(op, &parser.DropOwned{Sources: []parser.RoleSpec{{Name: "own_extension"}, {Name: "own_target"}}, Mode: dropMode}, 0, nil); err != nil || result.Count != 0 || result.Rows != nil {
					t.Fatal("canonical owner union", result, err)
				}
				if e.state.descriptors[extension.address] != nil {
					t.Fatal("extension survived complete union")
				}
				for _, address := range extension.members {
					if e.state.descriptors[address] != nil {
						t.Fatal("canonical member survived", address)
					}
				}
				for _, address := range extension.members {
					if original.state.descriptors[address] == nil {
						t.Fatal("retained member generation mutated", address)
					}
				}
			})
		}
	}
}
