package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsNativeControls(t *testing.T) {
	ctx := context.Background()
	source := New()
	for _, q := range []string{
		`CREATE ROLE ip_native_reader`,
		`DROP SCHEMA public CASCADE`,
		`CREATE SCHEMA public`,
		`CREATE EXTENSION pageinspect`,
		`CREATE TABLE public.ip_native (a integer, b integer)`,
		`GRANT SELECT ON public.ip_native TO ip_native_reader`,
		`GRANT UPDATE (b) ON public.ip_native TO ip_native_reader`,
		`ALTER EXTENSION pageinspect ADD TABLE public.ip_native`,
		`REVOKE SELECT ON public.ip_native FROM ip_native_reader`,
		`GRANT INSERT ON public.ip_native TO ip_native_reader`,
		`INSERT INTO public.ip_native VALUES (1,2)`,
	} {
		run(t, source, q)
	}
	before, err := control.CaptureInitialPrivileges(ctx, source.CatalogRead(), source.Roles())
	if err != nil {
		t.Fatal(err)
	}
	reader := source.Roles().LookupRef("ip_native_reader")
	found := false
	for address, descriptor := range source.CatalogRead().Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.Name() != "ip_native" {
			continue
		}
		found = true
		history := source.Catalog.State.InitialPrivilege(address)
		if history == nil || history.PrivType != 'e' {
			t.Fatal("ADD did not capture table history")
		}
		var initial, current authorization.Privileges
		for _, entry := range history.ACL.Entries {
			if entry.Grantee.Role == reader {
				initial |= entry.Privileges
			}
		}
		for _, entry := range relation.Security.ACL.Entries {
			if entry.Grantee.Role == reader {
				current |= entry.Privileges
			}
		}
		if initial != authorization.SelectPrivilege || current != authorization.InsertPrivilege {
			t.Fatalf("history/current conflated: %v/%v", initial, current)
		}
		column := address
		column.SubID = relation.Attributes[relation.Index["b"]]
		if record := source.Catalog.State.InitialPrivilege(column); record == nil || len(record.ACL.Entries) != 1 || record.ACL.Entries[0].Privileges != authorization.UpdatePrivilege {
			t.Fatal("column history missing")
		}
	}
	if !found {
		t.Fatal("source table missing")
	}
	direct := New()
	run(t, direct, `CREATE ROLE ip_native_allocator`)
	run(t, direct, `CREATE ROLE ip_native_reader`)
	sourceRoot := source.Catalog.State
	if _, err := direct.ReplaceCatalog(ctx, source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	directHistory, err := control.CaptureInitialPrivileges(ctx, direct.CatalogRead(), direct.Roles())
	if err != nil || !reflect.DeepEqual(before, directHistory) || source.Catalog.State != sourceRoot {
		t.Fatalf("direct import changed history/source: %v", err)
	}
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if source.Catalog.State.InitialPrivilege(public) != nil {
		t.Fatal("recreated public retained bootstrap history")
	}
	var dump bytes.Buffer
	if err := Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), control.InitialPrivilegesMarker) {
		t.Fatal("dump omitted complete historical metadata")
	}
	target := New()
	run(t, target, `CREATE ROLE ip_native_allocator`)
	run(t, target, `CREATE ROLE ip_native_reader`)
	// Public tests cover TEMP collisions through persistent physical sessions;
	// here distinct role identities prove final authority remapping directly.
	if err := Restore(ctx, target, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	after, err := control.CaptureInitialPrivileges(ctx, target.CatalogRead(), target.Roles())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("history changed across replay: %v\nbefore=%#v\nafter=%#v", err, before, after)
	}
	if target.Catalog.State.InitialPrivilege(public) != nil {
		t.Fatal("replay regenerated absent public history")
	}
	for address, record := range target.Catalog.State.InitialPrivileges() {
		for _, entry := range record.ACL.Entries {
			if _, err := target.Roles().Descriptor(entry.Grantor); err != nil {
				t.Fatalf("foreign grantor at %v: %v", address, err)
			}
			if !entry.Grantee.Public {
				if _, err := target.Roles().Descriptor(entry.Grantee.Role); err != nil {
					t.Fatalf("foreign grantee at %v: %v", address, err)
				}
			}
		}
	}
	if got := tuples(run(t, target, `SELECT * FROM public.ip_native`)); len(got) != 1 {
		t.Fatalf("data lost: %v", got)
	}
	var again bytes.Buffer
	if err := Dump(ctx, target, &again); err != nil {
		t.Fatal(err)
	}
	if dump.String() != again.String() {
		t.Fatal("historical metadata dump is not stable")
	}

	old := target.Catalog.State
	badMarker := strings.Replace(dump.String(), control.InitialPrivilegesMarker, "stuntgres-initial-privileges-v2", 1)
	if err := Restore(ctx, target, strings.NewReader(badMarker)); err == nil || target.Catalog.State != old {
		t.Fatal("bad version changed destination")
	}
	duplicate := dump.String() + "-- " + control.InitialPrivilegesMarker + " " + base64.StdEncoding.EncodeToString([]byte(`{"complete":true,"records":[]}`)) + "\n"
	if err := Restore(ctx, target, strings.NewReader(duplicate)); err == nil || target.Catalog.State != old {
		t.Fatal("duplicate inventory changed destination")
	}
	// Parse succeeds and all SQL replays before final identity resolution fails.
	late := dump.String()
	var lines []string
	for _, line := range strings.Split(late, "\n") {
		if strings.HasPrefix(line, "-- "+control.InitialPrivilegesMarker+" ") {
			broken := *before
			broken.Records = append([]control.InitialPrivilegeMetadataRecord(nil), before.Records...)
			broken.Records[0].Name = "ip_missing_final_target"
			comment, err := broken.Comment()
			if err != nil {
				t.Fatal(err)
			}
			line = strings.TrimSuffix(comment, "\n")
		}
		lines = append(lines, line)
	}
	if err := Restore(ctx, target, strings.NewReader(strings.Join(lines, "\n"))); err == nil || target.Catalog.State != old {
		t.Fatal("late resolver failure changed destination")
	}
	if err := Dump(ctx, target, failingWriter{}); err == nil || target.Catalog.State != old {
		t.Fatal("writer failure changed source")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Restore(cancelled, target, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) || target.Catalog.State != old {
		t.Fatal("cancelled restore changed destination", err)
	}
	if err := Reset(ctx, target); err != nil {
		t.Fatal(err)
	}
	if target.Catalog.State.InitialPrivilege(public) == nil {
		t.Fatal("reset did not restore native bootstrap history")
	}
	if old.InitialPrivilegeCount() == 0 || !reflect.DeepEqual(before, after) {
		t.Fatal("reset mutated retained historical capture")
	}
	if err := catalog.ValidateInitialPrivileges(target.Roles(), target.CatalogRead()); err != nil {
		t.Fatal(err)
	}

	plain := New()
	if err := Restore(ctx, plain, strings.NewReader(`CREATE TABLE plain_restore (a integer);`)); err != nil {
		t.Fatal(err)
	}
	if plain.Catalog.State.InitialPrivilege(public) == nil {
		t.Fatal("plain SQL restore incorrectly replaced ordinary bootstrap inventory")
	}
}
