package table

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func relationOptionUnit(name, text string) parser.IndexOption {
	return parser.IndexOption{Name: parser.Identifier{Text: name}, Value: parser.Value{Kind: parser.StringValue, Text: text}, HasValue: true}
}

func TestRelationOptionsRegistry(t *testing.T) {
	if len(relationOptionDefinitions) != 24 {
		t.Fatal(len(relationOptionDefinitions))
	}
	toast := 0
	names := map[string]bool{}
	for _, d := range relationOptionDefinitions {
		if names[d.name] {
			t.Fatal("duplicate registry name", d.name)
		}
		names[d.name] = true
		if d.toast {
			toast++
		}
	}
	if toast != 18 {
		t.Fatal("TOAST inventory", toast)
	}
	for _, tc := range []struct {
		name, text string
		integer    int64
		real       float64
		boolean    bool
		enum       string
	}{
		{name: "fillfactor", text: "070", integer: 56},
		{name: "fillfactor", text: "69.5", integer: 70},
		{name: "parallel_workers", text: "2.5", integer: 2},
		{name: "autovacuum_vacuum_scale_factor", text: "0.250", real: 0.25},
		{name: "autovacuum_enabled", text: "ye", boolean: true},
		{name: "autovacuum_enabled", text: "OFF"},
		{name: "vacuum_index_cleanup", text: "FALSE", enum: "off"},
	} {
		got, err := validateRelationOption(relationOptionUnit(tc.name, tc.text))
		if err != nil || got.Text != tc.text || got.Integer != tc.integer || got.Real != tc.real || got.Boolean != tc.boolean || got.Enum != tc.enum {
			t.Fatalf("%+v: %+v %v", tc, got, err)
		}
	}
}

func TestRelationOptionsImmutableCandidate(t *testing.T) {
	old := &catalog.Relation{RowType: sqltype.Int4, Columns: []catalog.Column{{Name: "id"}}, Attributes: []int32{1}, Index: map[string]int{"id": 0}, StorageOptions: catalog.RelationOptions{Base: []catalog.RelationOption{{Name: "fillfactor", Text: "70", Kind: catalog.RelationOptionInteger, Integer: 70}, {Name: "autovacuum_enabled", Text: "OFF", Kind: catalog.RelationOptionBoolean}}, Toast: []catalog.RelationOption{{Name: "vacuum_truncate", Text: "OFF", Kind: catalog.RelationOptionBoolean}}}}
	absent, absentErr := RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.ResetRelationOptions, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "unknown"}}}})
	if absentErr != nil || absent != old {
		t.Fatal("absent reset copied descriptor", absentErr)
	}
	retained := append([]catalog.RelationOption(nil), old.StorageOptions.Base...)
	next, err := RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{relationOptionUnit("fillfactor", "80")}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.StorageOptions.Base, retained) || next.StorageOptions.Base[0].Name != "autovacuum_enabled" || next.StorageOptions.Base[1].Integer != 80 {
		t.Fatal("order or published input mutated")
	}
	if &old.StorageOptions.Base[0] == &next.StorageOptions.Base[0] || &old.StorageOptions.Toast[0] != &next.StorageOptions.Toast[0] {
		t.Fatal("copy-on-write ownership")
	}
	if next.RowType != old.RowType || &next.Columns[0] != &old.Columns[0] || &next.Attributes[0] != &old.Attributes[0] {
		t.Fatal("metadata-only candidate changed row shape")
	}
	reset, err := RelationOptionsCandidate(next, parser.AlterTableAction{Action: parser.ResetRelationOptions, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "fillfactor"}}, {Name: parser.Identifier{Text: "autovacuum_enabled"}}}})
	if err != nil || reset.StorageOptions.Base != nil || len(next.StorageOptions.Base) != 2 {
		t.Fatal("reset absence/immutability", err)
	}
}

func TestRelationOptionsPhasesAndLocks(t *testing.T) {
	for _, kind := range []parser.AlterTableActionKind{parser.SetRelationOptions, parser.ResetRelationOptions} {
		a := parser.AlterTableAction{Action: kind, Options: []parser.IndexOption{relationOptionUnit("fillfactor", "70")}}
		if AlterActionPhase(kind) != 9 || InheritanceRecursiveAction(&catalog.Relation{}, a, false) || InheritanceRecursiveAction(&catalog.Relation{}, a, true) {
			t.Fatal("root-only misc phase")
		}
		raw := &parser.AlterTable{Actions: []parser.AlterTableAction{a}}
		if AlterTableLockMode(raw) != lock.ShareUpdateExclusive {
			t.Fatal("option lock")
		}
		raw.Actions = append(raw.Actions, parser.AlterTableAction{Action: parser.AddForeignKey})
		if AlterTableLockMode(raw) != lock.ShareRowExclusive {
			t.Fatal("mixed FK lock")
		}
		raw.Actions = append(raw.Actions, parser.AlterTableAction{Action: parser.AddColumn})
		if AlterTableLockMode(raw) != lock.AccessExclusive {
			t.Fatal("mixed shape lock")
		}
		raw.Actions = []parser.AlterTableAction{{Action: kind, Options: []parser.IndexOption{{Namespace: "toast", Name: parser.Identifier{Text: "user_catalog_table"}}}}}
		if AlterTableLockMode(raw) != lock.AccessExclusive {
			t.Fatal("user catalog lock")
		}
	}
}

func TestRelationOptionsValidationOrder(t *testing.T) {
	toast := relationOptionUnit("autovacuum_enabled", "bad")
	toast.Namespace = "toast"
	_, err := RelationOptionsCandidate(&catalog.Relation{}, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{toast, relationOptionUnit("fillfactor", "9")}})
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "22023" || d.Message != `value 9 out of bounds for option "fillfactor"` {
		t.Fatal("base validation must precede TOAST", err)
	}
	invalid := relationOptionUnit("fillfactor", "bad")
	_, err = RelationOptionsCandidate(&catalog.Relation{}, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{invalid, relationOptionUnit("fillfactor", "70")}})
	d, ok = err.(*sqlstate.Error)
	if !ok || d.Message != `invalid value for integer option "fillfactor": bad` {
		t.Fatal("first invalid input before later duplicate", err)
	}
}

func TestRelationOptionsBoundsText(t *testing.T) {
	for _, tc := range []struct {
		name, text, detail string
	}{
		{"fillfactor", "9.4", `Valid values are between "10" and "100".`},
		{"fillfactor", "0x09", `Valid values are between "10" and "100".`},
		{"fillfactor", "9e0", `Valid values are between "10" and "100".`},
		{"fillfactor", " 9 ", `Valid values are between "10" and "100".`},
		{"autovacuum_vacuum_scale_factor", "101.000", `Valid values are between "0.000000" and "100.000000".`},
	} {
		t.Run(tc.name+"/"+tc.text, func(t *testing.T) {
			_, err := validateRelationOption(relationOptionUnit(tc.name, tc.text))
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `value ` + tc.text + ` out of bounds for option "` + tc.name + `"`, Detail: tc.detail}
			if !reflect.DeepEqual(err, want) {
				t.Fatalf("bounds diagnostic = %#v, want %#v", err, want)
			}
		})
	}
	// Range checks use the rounded integer, not the explicit diagnostic text.
	entry, err := validateRelationOption(relationOptionUnit("fillfactor", "9.5"))
	if err != nil || entry.Integer != 10 || entry.Text != "9.5" {
		t.Fatalf("rounded valid boundary = %+v, %v", entry, err)
	}
}

func TestRelationOptionsTransformationOrder(t *testing.T) {
	bounds := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}
	namespaceError := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `unrecognized parameter namespace "bogus"`}
	nameError := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid option name "a=b": must not contain "="`}
	toastName := relationOptionUnit("a=b", "1")
	toastName.Namespace = "toast"
	unknownNamespace := relationOptionUnit("a=b", "1")
	unknownNamespace.Namespace = "bogus"
	for _, tc := range []struct {
		name    string
		options []parser.IndexOption
		want    *sqlstate.Error
	}{
		{"namespace before its name", []parser.IndexOption{unknownNamespace}, namespaceError},
		{"base value before toast name", []parser.IndexOption{relationOptionUnit("fillfactor", "9"), toastName}, bounds},
		{"toast name after base value", []parser.IndexOption{toastName, relationOptionUnit("fillfactor", "9")}, bounds},
		{"valid base before toast name", []parser.IndexOption{relationOptionUnit("fillfactor", "80"), toastName}, nameError},
		{"toast name after valid base", []parser.IndexOption{toastName, relationOptionUnit("fillfactor", "80")}, nameError},
		{"namespace before base value", []parser.IndexOption{unknownNamespace, relationOptionUnit("fillfactor", "9")}, namespaceError},
		{"base value after namespace", []parser.IndexOption{relationOptionUnit("fillfactor", "9"), unknownNamespace}, namespaceError},
		{"base name before base value", []parser.IndexOption{relationOptionUnit("a=b", "1"), relationOptionUnit("fillfactor", "9")}, nameError},
		{"base value after base name", []parser.IndexOption{relationOptionUnit("fillfactor", "9"), relationOptionUnit("a=b", "1")}, nameError},
		{"first structural error", []parser.IndexOption{relationOptionUnit("a=b", "1"), unknownNamespace}, nameError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &catalog.Relation{StorageOptions: catalog.RelationOptions{
				Base:  []catalog.RelationOption{{Name: "fillfactor", Text: "70", Kind: catalog.RelationOptionInteger, Integer: 70}},
				Toast: []catalog.RelationOption{{Name: "autovacuum_enabled", Text: "OFF", Kind: catalog.RelationOptionBoolean}},
			}}
			base := append([]catalog.RelationOption(nil), old.StorageOptions.Base...)
			toast := append([]catalog.RelationOption(nil), old.StorageOptions.Toast...)
			next, err := RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: tc.options})
			if next != nil || !reflect.DeepEqual(err, tc.want) {
				t.Fatalf("candidate = %v, diagnostic = %#v, want %#v", next, err, tc.want)
			}
			if !reflect.DeepEqual(old.StorageOptions.Base, base) || !reflect.DeepEqual(old.StorageOptions.Toast, toast) {
				t.Fatal("failed transformation mutated published options")
			}
		})
	}
	// Preserve the partition-parent gate between base and TOAST processing.
	_, err := RelationOptionsCandidate(&catalog.Relation{Partition: &catalog.PartitionDefinition{}}, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{toastName, relationOptionUnit("fillfactor", "80")}})
	wantPartition := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot specify storage parameters for a partitioned table", Hint: "Specify storage parameters for its leaf partitions instead."}
	if !reflect.DeepEqual(err, wantPartition) {
		t.Fatalf("partition gate diagnostic = %#v, want %#v", err, wantPartition)
	}
}

func TestRelationOptionsResetTransformation(t *testing.T) {
	old := &catalog.Relation{StorageOptions: catalog.RelationOptions{Base: []catalog.RelationOption{{Name: "fillfactor", Text: "70", Kind: catalog.RelationOptionInteger, Integer: 70}}}}
	unknown := parser.IndexOption{Namespace: "bogus", Name: parser.Identifier{Text: "a=b"}}
	next, err := RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.ResetRelationOptions, Options: []parser.IndexOption{unknown, unknown}})
	if err != nil || next != old {
		t.Fatalf("unknown repeated RESET changed descriptor: %v, %v", next, err)
	}
	remove := parser.IndexOption{Name: parser.Identifier{Text: "fillfactor"}}
	next, err = RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.ResetRelationOptions, Options: []parser.IndexOption{unknown, remove, remove}})
	if err != nil || next == old || next.StorageOptions.Base != nil || old.StorageOptions.Base[0].Text != "70" {
		t.Fatalf("RESET removal/absence/immutability: %v, %v", next, err)
	}
	unknown.HasValue = true
	next, err = RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.ResetRelationOptions, Options: []parser.IndexOption{remove, unknown}})
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "RESET must not include values for parameters"}
	if next != nil || !reflect.DeepEqual(err, want) || old.StorageOptions.Base[0].Text != "70" {
		t.Fatalf("RESET value admission: %v, %#v", next, err)
	}
}
