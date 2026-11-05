package fdw

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestFDWStageTableDeclarationConstraints(t *testing.T) {
	keyword := parser.Position{Character: 31}
	named := parser.Position{Character: 19}
	for _, tc := range []struct {
		name              string
		element           parser.TableElement
		message, position string
	}{
		{"primary", parser.TableElement{Unique: &parser.UniqueConstraint{Primary: true, Pos: keyword}}, "primary key", "31"},
		{"unique", parser.TableElement{Unique: &parser.UniqueConstraint{Pos: named}}, "unique", "19"},
		{"foreign", parser.TableElement{ForeignKey: &parser.ForeignKeyConstraint{Pos: keyword}}, "foreign key", "31"},
		{"named_foreign", parser.TableElement{ForeignKey: &parser.ForeignKeyConstraint{Pos: keyword, NamedDeclarationPos: named}}, "foreign key", "19"},
		{"exclusion", parser.TableElement{Exclusion: &parser.ExclusionConstraint{Pos: keyword}}, "exclusion", "31"},
		{"named_exclusion", parser.TableElement{Exclusion: &parser.ExclusionConstraint{Pos: keyword, NamedDeclarationPos: named}}, "exclusion", "19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckTableDeclaration(&parser.CreateTable{Elements: []parser.TableElement{tc.element}})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message+" constraints are not supported on foreign tables" || diagnostic.Position != tc.position {
				t.Fatalf("diagnostic=%+v error=%v", diagnostic, err)
			}
			if tc.element.Exclusion == nil {
				column := &parser.Column{Constraints: []parser.ColumnConstraint{{Unique: tc.element.Unique, ForeignKey: tc.element.ForeignKey}}}
				if err := CheckAddedColumn(column); !errors.As(err, &diagnostic) || diagnostic.Position != tc.position || diagnostic.Message != tc.message+" constraints are not supported on foreign tables" {
					t.Fatalf("added-column diagnostic=%+v error=%v", diagnostic, err)
				}
			}
		})
	}
	identity := &parser.Column{Constraints: []parser.ColumnConstraint{{Identity: &parser.IdentityDefinition{}}}}
	if err := CheckAddedColumn(identity); err != nil {
		t.Fatal(err)
	}
}

func TestFDWStageTableLikeSyntaxOwnership(t *testing.T) {
	options := []parser.LikeOption{{Name: "all", Including: true}, {Name: "defaults"}}
	like := &parser.TableLike{Options: options}
	raw := &parser.CreateTable{Elements: []parser.TableElement{{Like: like}}}
	normalized := TableSyntax(raw)
	if normalized == raw || normalized.Elements[0].Like == like || !reflect.DeepEqual(like.Options, options) || len(like.Options) != 2 {
		t.Fatal("normalization changed caller-owned LIKE syntax")
	}
	want := append(append([]parser.LikeOption(nil), options...), parser.LikeOption{Name: "indexes"}, parser.LikeOption{Name: "identity"}, parser.LikeOption{Name: "storage"}, parser.LikeOption{Name: "compression"})
	if !reflect.DeepEqual(normalized.Elements[0].Like.Options, want) {
		t.Fatalf("normalized=%+v want=%+v", normalized.Elements[0].Like.Options, want)
	}
	plain := &parser.CreateTable{Elements: []parser.TableElement{{Column: &parser.Column{Name: "id"}}}}
	if TableSyntax(plain) != plain {
		t.Fatal("ordinary declaration was copied")
	}
}

func TestFDWTableOptionAdmission(t *testing.T) {
	parse := func(source string) *parser.ForeignDDL {
		t.Helper()
		statements, err := parser.Parse(source)
		if err != nil || len(statements) != 1 { t.Fatal(source, err) }
		return statements[0].(*parser.ForeignDDL)
	}
	leaf := &catalog.Relation{ObjectName: "f", RelationKind: catalog.ForeignRelation, Membership: &catalog.PartitionMembership{}}
	replay := func(parser.Statement, *catalog.Relation) bool { return false }
	for _, source := range []string{
		"ALTER FOREIGN TABLE public.f OPTIONS(SET filename '/x')",
		"ALTER FOREIGN TABLE IF EXISTS public.f ALTER COLUMN txt OPTIONS(force_null 'true')",
		"ALTER FOREIGN TABLE public.f OPTIONS(SET format 'csv'),ALTER COLUMN txt OPTIONS(DROP force_null,ADD force_not_null 'off')",
	} {
		t.Run(source, func(t *testing.T) {
			raw := parse(source)
			before := parse(source)
			admitted := AlterAdmission(raw)
			if admitted != raw { t.Fatal("metadata admission lost the original command") }
			target, ok, err := table.AdmitTarget([]table.TargetEntry{table.TargetAdmission, TargetAdmission}, admitted, true)
			if err != nil || !ok || !reflect.DeepEqual(target.Name, raw.Name) || target.Mode != lock.AccessExclusive || !target.OwnerOnly || !target.Rename || target.IfExists != raw.IfExists {
				t.Fatal("foreign options lost ordinary target admission", target, ok, err)
			}
			if _, ok, err := TargetAdmission(admitted, false); ok || err != nil { t.Fatal("prepare acquired a metadata target", ok, err) }
			if err := partition.ConsumerBoundary(catalog.Read{}, admitted, leaf, replay); err != nil { t.Fatal("metadata options rejected as hierarchy alteration", err) }
			pending := *leaf
			pending.Membership = &catalog.PartitionMembership{DetachPending: true}
			var diagnostic *sqlstate.Error
			if err := partition.ConsumerBoundary(catalog.Read{}, admitted, &pending, replay); !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != `cannot alter partition "f" with an incomplete detach` || diagnostic.Hint != partition.DetachFinalizeHint {
				t.Fatal("metadata options bypassed pending detach", err)
			}
			if !reflect.DeepEqual(raw, before) { t.Fatal("admission mutated the retained command") }
		})
	}
	for _, source := range []string{
		"ALTER FOREIGN TABLE public.f ADD COLUMN extra integer",
		"ALTER FOREIGN TABLE public.f ADD COLUMN extra integer OPTIONS(force_null 'true')",
		"ALTER FOREIGN TABLE public.f OPTIONS(SET filename '/x'),ADD COLUMN extra integer",
		"ALTER FOREIGN TABLE public.f ALTER COLUMN txt SET DEFAULT 'x'",
		"ALTER FOREIGN TABLE public.f OWNER TO CURRENT_USER",
		"ALTER FOREIGN TABLE public.f RENAME TO other",
	} {
		t.Run(source, func(t *testing.T) {
			raw := parse(source)
			if _, ok, err := TargetAdmission(raw, true); ok || err != nil { t.Fatal("non-option command acquired metadata admission", ok, err) }
			admitted := AlterAdmission(raw)
			if admitted == raw { t.Fatal("structural/default command escaped its ordinary projection") }
			var diagnostic *sqlstate.Error
			if err := partition.ConsumerBoundary(catalog.Read{}, admitted, leaf, replay); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "altering partition hierarchies is unsupported" {
				t.Fatal("ordinary hierarchy boundary changed", err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		change func(*parser.ForeignDDL)
	}{
		{"empty", func(s *parser.ForeignDDL) { s.TableActions = nil }},
		{"empty_options", func(s *parser.ForeignDDL) { s.TableActions[0].Options = nil }},
		{"wrong_kind", func(s *parser.ForeignDDL) { s.Kind = "server" }},
		{"constructor", func(s *parser.ForeignDDL) { s.Table = &parser.CreateTable{} }},
		{"if_not_exists", func(s *parser.ForeignDDL) { s.IfNotExists = true }},
		{"owner", func(s *parser.ForeignDDL) { s.Owner = &parser.RoleSpec{Name: "other"} }},
		{"support", func(s *parser.ForeignDDL) { s.Support = []parser.ForeignSupportOption{{Kind: "handler"}} }},
		{"outer_options", func(s *parser.ForeignDDL) { s.Options = s.TableActions[0].Options }},
		{"add_column_options", func(s *parser.ForeignDDL) { s.TableActions[0].ColumnOptions = s.TableActions[0].Options }},
		{"role", func(s *parser.ForeignDDL) { s.Role.Public = true }},
		{"import", func(s *parser.ForeignDDL) { s.RemoteSchema.Text = "remote" }},
		{"version", func(s *parser.ForeignDDL) { s.VersionSet = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := parse("ALTER FOREIGN TABLE public.f ALTER COLUMN txt OPTIONS(force_null 'true')")
			tc.change(raw)
			if _, ok, err := TargetAdmission(raw, true); ok || err != nil { t.Fatal("noisy command acquired metadata admission", ok, err) }
			admitted := AlterAdmission(raw)
			if admitted == raw { t.Fatal("noisy command retained metadata admission") }
			if _, ok, err := TargetAdmission(admitted, true); ok || err != nil { t.Fatal("projection acquired metadata admission", ok, err) }
		})
	}
	empty := &parser.AlterTable{}
	if _, ok, err := TargetAdmission(empty, true); ok || err != nil { t.Fatal("empty ordinary ALTER became foreign metadata", ok, err) }
	var diagnostic *sqlstate.Error
	if err := partition.ConsumerBoundary(catalog.Read{}, empty, leaf, replay); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" { t.Fatal("empty ordinary hierarchy ALTER was admitted", err) }
}
