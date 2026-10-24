package engine

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestEan13RestoreOwnerReplay(t *testing.T) { testIsnRestoreOwnerReplay(t, "ean13", 0) }
func TestIsbnRestoreOwnerReplay(t *testing.T) { testIsnRestoreOwnerReplay(t, "isbn", 3) }

func testIsnRestoreOwnerReplay(t *testing.T, family string, slot int) {
	e, ext, _ := isnUnitInstalled(t)
	base := e.CatalogRead().MustType(ext.Members[slot])
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA ean_owner_dst", "ALTER TYPE public."+family+" RENAME TO moved", "ALTER TYPE public.moved SET SCHEMA ean_owner_dst"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	metadata, err := extension.OwnerMetadata(view, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, recognized, err := extension.DecodeOwners(strings.TrimPrefix(metadata, "-- "))
	if err != nil || !recognized {
		t.Fatal("owner record", recognized, err)
	}
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
	for _, tc := range []struct {
		name   string
		change func(*parser.AlterOwner, *extension.OwnerRecord)
		valid  bool
	}{
		{"captured-current-base", nil, true},
		{"wrong-version", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Version = "0" }, false},
		{"wrong-install-schema", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Schema = "ean_owner_dst" }, false},
		{"wrong-install-owner", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Owner = "pg_signal_backend" }, false},
		{"wrong-extension", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Name = "citext" }, false},
		{"missing-type-role", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Members = append(r.Members[:slot], r.Members[slot+1:]...) }, false},
		{"mutable-spelling-not-role", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Members[slot].Name = "moved" }, false},
		{"wrong-member-kind", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Members[slot].Kind = "procedure" }, false},
		{"type-signature", func(_ *parser.AlterOwner, r *extension.OwnerRecord) { r.Members[slot].Signature = family }, false},
		{"mismatched-target", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Target.Name = "pg_signal_backend" }, false},
		{"identity-role", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Target.Identity = "current" }, false},
		{"empty-role", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Target.Name = "" }, false},
		{"conditional", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.IfExists = true }, false},
		{"unqualified", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Name.Parts = s.Name.Parts[1:] }, false},
		{"old-spelling", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Name.Parts[1].Text = family }, false},
		{"wrong-namespace", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Name.Parts[0].Text = "public" }, false},
		{"companion", func(s *parser.AlterOwner, _ *extension.OwnerRecord) { s.Name.Parts[1].Text = array.Name() }, false},
		{"foreign-family", func(s *parser.AlterOwner, _ *extension.OwnerRecord) {
			s.Name.Parts[0].Text, s.Name.Parts[1].Text = "pg_catalog", "int4"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := record
			captured.Members = append([]extension.MemberOwner(nil), record.Members...)
			raw := &parser.AlterOwner{Kind: "type", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ean_owner_dst"}, {Text: "moved"}}}, Target: parser.RoleSpec{Name: "postgres"}}
			if tc.change != nil {
				tc.change(raw, &captured)
			}
			run := &restoreRun{stage: e, extensionOwners: []extension.OwnerRecord{captured}}
			err := admitRestoreAlterOwner(run, op, raw)
			if (err == nil) != tc.valid {
				t.Fatalf("admission error = %v, want valid %v", err, tc.valid)
			}
			if e.CatalogRead().State() != view.State() {
				t.Fatal("admission published catalog changes")
			}
		})
	}
	raw := &parser.AlterOwner{Kind: "type", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ean_owner_dst"}, {Text: "moved"}}}, Target: parser.RoleSpec{Name: "postgres"}}
	if err := admitRestoreAlterOwner(&restoreRun{stage: e}, op, raw); err == nil {
		t.Fatal("missing owner record admitted")
	}
}
