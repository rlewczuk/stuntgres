package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Native SQL GRANT/REVOKE is excluded. Real SetObjectACL on a nonsuperuser
// owner supplies the counterpart of the PostgreSQL-only disclosure phases.
func TestPrimaryKeyCurrentACLDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "keys", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				e, owner, parent, index := primaryKeyUnitFixture(t)
				actor, ok := e.Roles().Lookup("pk_owner")
				if !ok || actor.Superuser || parent.Owner != actor.Ref {
					t.Fatal(actor, parent.Owner)
				}
				duplicate := primaryKeyUnitInsert(1)
				typeBuild := &parser.AlterTable{Name: primaryKeyUnitName("t"), Actions: []parser.AlterTableAction{{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "a"}, TypeName: parser.TypeName{Name: "bigint"}, Using: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}}}}}
				var heldDuplicate, heldType StatementDescription
				if prepared {
					var err error
					heldDuplicate, err = e.Describe(owner, duplicate)
					if err != nil {
						t.Fatal(err)
					}
					heldType, err = e.Describe(owner, typeBuild)
					if err != nil {
						t.Fatal(err)
					}
				}
				grant := func(attribute int32, rights authorization.Privileges) {
					view := e.CatalogRead()
					address := parent.address
					address.SubID = attribute
					ref := e.catalog.domain.Ref(view.Epoch(), address)
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}}
					if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
						t.Fatal(err)
					}
				}
				grant(0, authorization.InsertPrivilege)
				switch mode {
				case "partial":
					grant(1, authorization.SelectPrivilege)
				case "keys":
					grant(1, authorization.SelectPrivilege)
					grant(2, authorization.SelectPrivilege)
				case "table":
					grant(0, authorization.InsertPrivilege|authorization.SelectPrivilege)
				}
				view := e.CatalogRead()
				current := view.MustRelation(parent.address)
				auth, err := owner.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				allowed, err := uniqueKeyDetailAllowed(auth, e.catalog.domain.Ref(view.Epoch(), parent.address), index.SQLIndex.keys)
				if err != nil || allowed != (mode == "keys" || mode == "table") {
					t.Fatal("actual current key disclosure authority", allowed, err)
				}
				before, store, root := e.catalog.state, e.storage[parent.address], e.indexRoots[index.address]
				execute := func(ast parser.Statement, held *StatementDescription) (Result, error) {
					if !prepared {
						return e.Execute(owner, ast, 0, nil)
					}
					scope, err := e.StartStatement(owner)
					if err != nil {
						return Result{}, err
					}
					scope.Expected = held
					result, err := scope.Execute(ast, 0, nil)
					scope.Finish(err)
					return result, err
				}
				result, err := execute(duplicate, &heldDuplicate)
				detail := ""
				if allowed {
					detail = "Key (a, b)=(1, protected) already exists."
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "pk"`, Detail: detail, Schema: "public", Table: "t", Constraint: "pk"})
				if result.Count != 0 || result.Rows != nil {
					t.Fatal(result)
				}
				result, err = execute(typeBuild, &heldType)
				detail = "Duplicate keys exist."
				if allowed {
					detail = "Key (a, b)=(0, protected) is duplicated."
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "pk"`, Detail: detail, Schema: "public", Table: "t", Constraint: "pk"})
				if result.Count != 0 || result.Rows != nil || e.catalog.state != before || e.storage[parent.address] != store || e.indexRoots[index.address] != root || e.catalog.state.Get(parent.address) != current {
					t.Fatal("failed runtime/TYPE disclosed or published protected state", result)
				}
				lockEmpty(t, e)
			})
		}
	}
}
