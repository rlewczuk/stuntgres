package tables_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestGeneratedVirtualUnsimplifiedTypeSecurity(t *testing.T) {
	_, view, d := generatedUnitDescriptor(t)
	integer := d.Columns[0].Usage.Type
	domain, err := sqltype.Domain(64001, "vd", "public.vd", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(64002, "_vd", domain)
	if err != nil {
		t.Fatal(err)
	}
	enum, err := sqltype.Enum(64003, "ve", "public.ve", []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	enumArray, err := sqltype.Array(64004, "_ve", enum)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ    *sqltype.Descriptor
		domain bool
	}{{domain, true}, {array, true}, {enum, false}, {enumArray, false}} {
		err := table.VirtualDestination(catalog.Column{Name: "b", Usage: tc.typ.Usage()})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(tc, err)
		}
		if tc.domain {
			if diagnostic.Message != `virtual generated column "b" cannot have a domain type` || diagnostic.Detail != "" {
				t.Fatal(diagnostic)
			}
		} else if diagnostic.Message != `virtual generated column "b" cannot have a user-defined type` || diagnostic.Detail != "Virtual generated columns that make use of user-defined types are not yet supported." {
			t.Fatal(diagnostic)
		}
	}
	builtinArray, ok := view.LookupType(1007)
	if !ok {
		t.Fatal("int[] missing")
	}
	if err := table.VirtualDestination(catalog.Column{Name: "b", Usage: builtinArray.Usage()}); err != nil {
		t.Fatal(err)
	}
	hidden := plan.LiteralExpr{Info: expr.Info{Usage: domain.Usage(), Pos: parser.Position{Character: 17}}, Value: sqltype.Null(domain)}
	otherwise := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	tree := plan.CaseExpr{Info: expr.Info{Usage: integer.Usage()}, Arms: []plan.CaseBranch{{Result: hidden}}, Otherwise: otherwise}
	var diagnostic *sqlstate.Error
	if err := table.VirtualGenerationSecurity(tree); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Position != "17" || diagnostic.Message != "generation expression uses user-defined type" {
		t.Fatal(err)
	}
	if err := table.VirtualGenerationSecurity(otherwise); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedVirtualUnsimplifiedRoutineSecurity(t *testing.T) {
	_, view, d := generatedUnitDescriptor(t)
	integer := d.Columns[0].Usage.Type
	otherwise := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	var builtin *catalog.Procedure
	for _, descriptor := range view.State().Descriptors() {
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Builtin != nil && procedure.Volatility == expr.Immutable {
			builtin = procedure
			break
		}
	}
	if builtin == nil {
		t.Fatal("immutable builtin missing")
	}
	user := *builtin
	user.Builtin = nil
	for _, tc := range []struct {
		name      string
		procedure *catalog.Procedure
		reject    bool
	}{
		{"builtin", builtin, false},
		{"user-defined", &user, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Security examines original call provenance even in an unreachable
			// branch, before conditional simplification can remove that call.
			call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), Pos: parser.Position{Character: 17}, RoutineReference: tc.procedure, Property: expr.Properties{Volatility: expr.Immutable}}, Procedure: tc.procedure}
			tree := plan.CaseExpr{Info: expr.Info{Usage: integer.Usage()}, Arms: []plan.CaseBranch{{Result: call}}, Otherwise: otherwise}
			err := table.VirtualGenerationSecurity(tree)
			if !tc.reject {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "generation expression uses user-defined function" || diagnostic.Detail != "Virtual generated columns that make use of user-defined functions are not yet supported." || diagnostic.Position != "17" {
				t.Fatal(err)
			}
			var altered *sqlstate.Error
			if !errors.As(table.AlterCookingError(err), &altered) || altered.Position != "" || altered.Code != diagnostic.Code || altered.Message != diagnostic.Message || altered.Detail != diagnostic.Detail || diagnostic.Position != "17" {
				t.Fatal("ALTER cooking changed diagnostic provenance", altered, diagnostic)
			}
		})
	}
}

func generatedUnitDescriptor(t *testing.T) (*core.Engine, catalog.Read, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	view := e.CatalogRead()
	integer, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	var namespace ids.ObjectAddress
	for address, descriptor := range view.Descriptors() {
		if descriptor.Kind() == ids.NamespaceObject && descriptor.Name() == "public" {
			namespace = address
			break
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: namespace, ObjectName: "g", ObjectRevision: 1, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: true}, {Name: "b", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Index: map[string]int{"a": 0, "b": 1}}
	return e, view, d
}
