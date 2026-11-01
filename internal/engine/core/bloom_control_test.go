package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestBloomControlIdentity(t *testing.T) {
	source := bloomSetup(t)
	for _, sql := range []string{
		"CREATE SCHEMA bloom_control",
		"ALTER EXTENSION bloom SET SCHEMA bloom_control",
		"CREATE INDEX bloom_expr_idx ON bloom_rows USING bloom((i+1) bloom_control.int4_ops) WITH(length=7,col32=4095) WHERE i IS NOT NULL",
		"CREATE VIEW bloom_control.v AS SELECT i,s FROM public.bloom_rows WHERE i IS NOT NULL",
		"CREATE ACCESS METHOD bloom_alias TYPE INDEX HANDLER bloom_control.blhandler",
		"CREATE OPERATOR CLASS public.bloom_control_alias_int4 DEFAULT FOR TYPE integer USING bloom_alias AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)",
		"CREATE INDEX bloom_alias_idx ON bloom_rows USING bloom_alias(i) WITH(length=7)",
		"ALTER EXTENSION bloom DROP ACCESS METHOD bloom",
	} { enginetest.Run(t, source, sql) }
	original, old := bloomRoot(t, source, "bloom_rows_idx")
	oldStore := source.Storage.Relations[original.SQLIndex.Parent]
	rows, signatures := hashCancellationRows(oldStore.Versions), bloomSignatures(old)
	oldMethod, _ := source.CatalogRead().LookupAccessMethod("bloom")
	view := source.CatalogRead()
	ext, _ := view.Extension(oldMethod.Origin.Installation.Cohort)
	metadata, err := extension.OwnerMetadata(view, source.Roles(), ext)
	if err != nil { t.Fatal(err) }
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found { t.Fatal("owner metadata did not decode", found, err) }
	methodMember := -1
	for i, member := range record.Members {
		if member.Kind == "access_method" {
			methodMember = i
			if member.Owner != "" || member.ACL != nil { t.Fatal("ownerless AM acquired owner/ACL", member) }
		} else if member.Owner != "postgres" {
			t.Fatal("schema-owned bloom declaration lost owner", member)
		}
	}
	principal, acl, _ := access.DescriptorACL(oldMethod)
	if methodMember < 0 || principal != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || access.ClassPrivileges(oldMethod) != 0 {
		t.Fatal("access method is not ownerless and ungrantable")
	}
	candidate := catalog.CloneState(view.State())
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), source.Roles(), []extension.OwnerRecord{record}); err != nil || changed {
		t.Fatal("genuine bloom authority failed to import unchanged", changed, err)
	}
	for _, field := range []string{"owner", "acl"} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		if field == "owner" { bad.Members[methodMember].Owner = "postgres" } else { bad.Members[methodMember].ACL = &extension.MemberACL{} }
		candidate := catalog.CloneState(view.State())
		_, err := extension.ImportOwners(context.Background(), view.WithState(candidate), source.Roles(), []extension.OwnerRecord{bad})
		var sql interface{ SQLState() string }
		if err == nil || err.Error() != "stuntgres: invalid extension owner metadata" || errors.As(err, &sql) {
			t.Fatal("invented access-method authority accepted", field, err)
		}
		for address, descriptor := range view.Descriptors() {
			if candidate.Get(address) != descriptor { t.Fatal("rejected owner metadata changed candidate", field, address) }
		}
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil { t.Fatal(err) }
	target := engine.New()
	enginetest.Run(t, target, "CREATE TABLE discarded(i integer)")
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	destination, rebuilt := bloomRoot(t, target, "bloom_rows_idx")
	store := target.Storage.Relations[destination.SQLIndex.Parent]
	method, _ := target.CatalogRead().LookupAccessMethod("bloom")
	alias, _ := target.CatalogRead().LookupAccessMethod("bloom_alias")
	if method == oldMethod || method.Origin.Installation == oldMethod.Origin.Installation || method.Extension != (ids.ObjectAddress{}) || alias.Origin != nil || alias.Handler != method.Handler || alias.Address() == method.Address() {
		t.Fatal("symbolic installed/alias/current-membership identities collapsed", method, alias)
	}
	if err := (catalog.AccessMethodProvider{}).Validate(target.CatalogRead(), method); err != nil { t.Fatal(err) }
	if err := (catalog.AccessMethodProvider{}).Validate(target.CatalogRead(), alias); err != nil { t.Fatal(err) }
	if !method.Origin.Valid(method.Address()) || method.Origin.Installation.Implementation != extension.StockBloom { t.Fatal("destination did not install genuine provider") }
	if rebuilt == old || rebuilt.Bloom == old.Bloom || store == oldStore || rebuilt.Bloom.Storage != store || rebuilt.Construction == old.Construction {
		t.Fatal("native replay retained source roots/storage/construction")
	}
	bloomAssertRoot(t, target, "bloom_rows_idx", 7)
	aliasIndex, aliasRoot := bloomRoot(t, target, "bloom_alias_idx")
	if aliasIndex.SQLIndex.MethodIdentity() != alias.Address().ObjectID || aliasRoot.Bloom.Storage != store || len(aliasRoot.Entries) != 7 {
		t.Fatal("ordinary alias class/index lost destination identity")
	}
	for i, signed := range rebuilt.Bloom.Entries {
		if &signed.Signature[0] == &old.Bloom.Entries[i].Signature[0] { t.Fatal("destination shared source signature backing") }
	}
	expression, expressionRoot := bloomRoot(t, target, "bloom_expr_idx")
	if expression.SQLIndex.Keys[0].Derived == nil || expression.SQLIndex.Predicate == nil || !expression.SQLIndex.Keys[0].ExplicitClass || expressionRoot.Bloom.Options.Words != 1 || expressionRoot.Bloom.Options.Bits[31] != 4095 || len(expressionRoot.Entries) != 5 {
		t.Fatal("expression/predicate/class/options lost during replay", expression.SQLIndex, expressionRoot.Bloom)
	}
	lexical := false
	for _, option := range expression.StorageOptions.Base { if option.Name == "length" && option.Text == "7" && option.Integer == 7 { lexical = true } }
	if !lexical { t.Fatal("effective width overwrote lexical length") }
	before := target.Catalog.State
	bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nALTER INDEX bloom_rows_idx SET(length=0);")...)
	if err := engine.Restore(context.Background(), target, bytes.NewReader(bad)); err == nil { t.Fatal("late failed restore succeeded") }
	if target.Catalog.State != before || target.Storage.IndexRoots[destination.Address()] != rebuilt { t.Fatal("late restore failure replaced destination authority") }
	base, cancel := context.WithCancel(context.Background())
	interrupted := &bloomPhaseContext{cancelAtErrCheck: &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}, phase: "AddBloomValue"}
	err = engine.Restore(interrupted, target, bytes.NewReader(dump.Bytes()))
	cancel()
	if !errors.Is(err, context.Canceled) || target.Catalog.State != before || target.Storage.IndexRoots[destination.Address()] != rebuilt { t.Fatal("canceled restore published destination", err) }
	enginetest.Run(t, target, "INSERT INTO bloom_rows VALUES(10,'q')")
	bloomAssertRoot(t, target, "bloom_rows_idx", 8)
	bloomAssertRoot(t, target, "bloom_expr_idx", 6)
	if source.Storage.IndexRoots[original.Address()] != old || !reflect.DeepEqual(rows, oldStore.Versions) || !reflect.DeepEqual(signatures, bloomSignatures(old)) { t.Fatal("native controls mutated source ownership/backing") }
	// The composed replacement seam must independently remint and rehash, too.
	imported := engine.New()
	if _, err := imported.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil { t.Fatal(err) }
	_, transported := bloomRoot(t, imported, "bloom_rows_idx")
	if transported.Bloom == old.Bloom || transported.Bloom.Storage == oldStore || &transported.Bloom.Entries[0].Signature[0] == &old.Bloom.Entries[0].Signature[0] { t.Fatal("replacement transplanted source signatures") }
}
