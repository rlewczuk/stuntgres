package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPgcryptoOwnerMetadata(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION pgcrypto;\n"+metadata, parser.Options{
		Context: context.Background(), StandardConformingStrings: true,
		LineComment: func(comment string, _ parser.Position) error {
			record, found, err := extension.DecodeOwners(comment)
			if err == nil && found {
				records = append(records, record)
			}
			return err
		},
	})
	expected := extension.OwnerRecord{Name: "pgcrypto", Schema: "public", Version: "1.4", Owner: "postgres", Members: []extension.MemberOwner{
		{Kind: "procedure", Name: "digest", Signature: "25,25", Owner: "postgres"},
		{Kind: "procedure", Name: "digest", Signature: "17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "hmac", Signature: "25,25,25", Owner: "postgres"},
		{Kind: "procedure", Name: "hmac", Signature: "17,17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "crypt", Signature: "25,25", Owner: "postgres"},
		{Kind: "procedure", Name: "gen_salt", Signature: "25", Owner: "postgres"},
		{Kind: "procedure", Name: "gen_salt", Signature: "25,23", Owner: "postgres"},
		{Kind: "procedure", Name: "encrypt", Signature: "17,17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "decrypt", Signature: "17,17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "encrypt_iv", Signature: "17,17,17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "decrypt_iv", Signature: "17,17,17,25", Owner: "postgres"},
		{Kind: "procedure", Name: "gen_random_bytes", Signature: "23", Owner: "postgres"},
		{Kind: "procedure", Name: "gen_random_uuid", Signature: "", Owner: "postgres"},
		{Kind: "procedure", Name: "fips_mode", Signature: "", Owner: "postgres"},
	}}
	if err != nil || len(records) != 1 || !reflect.DeepEqual(records[0], expected) {
		t.Fatal("owner metadata", records, err)
	}
}

func TestExtensionOwnerManifestAdmission(t *testing.T) {
	unaccent, _, unaccentExt := extensionUnitInstalled(t)
	intarray, intarrayExt, _ := intarrayUnitInstalled(t)
	crypto, cryptoExt := pgcryptoUnitInstalled(t)
	intagg, intaggExt := intaggUnitInstalled(t)
	fuzzyBase, _, fuzzyBaseExt := extensionVersionUnitInstalled(t, "1.1")
	fuzzyUpdated, _, fuzzyUpdatedExt := extensionVersionUnitInstalled(t, "1.2")
	for _, installed := range []struct {
		e   *Engine
		ext *catalog.Extension
	}{{unaccent, unaccentExt}, {intarray, intarrayExt}, {crypto, cryptoExt}, {intagg, intaggExt}, {fuzzyBase, fuzzyBaseExt}, {fuzzyUpdated, fuzzyUpdatedExt}} {
		metadata, err := extension.OwnerMetadata(installed.e.CatalogRead(), installed.e.Roles(), installed.ext)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
		if err != nil || !found {
			t.Fatal("valid manifest rejected", installed.ext.Name(), err)
		}
		for _, kind := range []string{"version", "graph_only", "missing", "extra", "duplicate", "name", "signature", "foreign_procedure", "foreign_dictionary", "foreign_type", "owner", "dm_in_base"} {
			t.Run(record.Name+"/"+record.Version+"/"+kind, func(t *testing.T) {
				bad := record
				bad.Members = slices.Clone(record.Members)
				switch kind {
				case "version":
					bad.Version = "missing"
				case "graph_only":
					bad.Version = "1.0"
				case "dm_in_base":
					if record.Name != "fuzzystrmatch" || record.Version != "1.1" {
						return
					}
					bad.Members[0] = extension.MemberOwner{Kind: "procedure", Name: "daitch_mokotoff", Signature: "25", Owner: "postgres"}
				case "missing":
					bad.Members = bad.Members[1:]
				case "extra":
					bad.Members = append(bad.Members, bad.Members[0])
				case "duplicate":
					bad.Members[1] = bad.Members[0]
				case "name":
					bad.Members[0].Name = "hmac"
				case "signature":
					bad.Members[0].Signature = "17,25,25"
				case "foreign_procedure":
					bad.Members[0] = extension.MemberOwner{Kind: "procedure", Name: "unaccent", Signature: "25", Owner: "postgres"}
					if record.Name == "unaccent" {
						bad.Members[0] = extension.MemberOwner{Kind: "procedure", Name: "digest", Signature: "25,25", Owner: "postgres"}
					}
				case "foreign_dictionary":
					bad.Members[0] = extension.MemberOwner{Kind: "dictionary", Name: "query_int", Owner: "postgres"}
				case "foreign_type":
					bad.Members[0] = extension.MemberOwner{Kind: "type", Name: "unaccent", Owner: "postgres"}
				case "owner":
					bad.Members[0].Owner = ""
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if _, found, err := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data)); !found || err == nil {
					t.Fatal("invalid manifest accepted", bad)
				}
			})
		}
	}
	extensionVersionOwnerImports(t)
}

func TestPgcryptoControlRemap(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	before := e.CatalogRead()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	next := catalog.CloneState(before.State())
	for address := range mapping {
		next.UnbindName(catalog.DescriptorNameKey(before.Get(address)))
		next.RemoveDescriptor(address)
	}
	for address, destination := range mapping {
		descriptor := extension.RemapDescriptor(before.Get(address), mapping)
		next.SetDescriptor(destination, descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), destination)
	}
	view := e.catalog.read(next)
	remapped := next.MustExtension(mapping[ext.Address()])
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, remapped); err != nil {
		t.Fatal("remapped extension", err)
	}
	for i, original := range ext.Members {
		proc := next.MustProcedure(mapping[original])
		if err := (extension.CatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal("remapped procedure", err)
		}
		old := before.MustProcedure(original)
		if remapped.Members[i] != proc.Address() || proc.Extension != remapped.Address() || proc.Namespace() != old.Namespace() || proc.Owner != old.Owner || extension.PgcryptoProviderOf(proc) != extension.PgcryptoProviderOf(old) || proc.Signature != old.Signature || proc.Result != old.Result || proc.Strict != old.Strict || proc.Volatility != old.Volatility {
			t.Fatal("remapped member identity", proc)
		}
		if old.Address() != original || old.Extension != ext.Address() || ext.Members[i] != original {
			t.Fatal("source generation mutated")
		}
	}
	// Force actual provider relocation: source procedure addresses are absent
	// from the destination, so retag-before-reference-remap cannot resolve them.
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	bound, err := binder.BindFunctionProvider(parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "hmac"}}},
		Args: []parser.Expr{literal("what do ya want for nothing?"), literal("Jefe"), literal("sha256")},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledCryptoExpr)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900010}
	consumer := &catalog.Relation{
		ObjectAddress: address,
		ColumnMeta: catalog.ColumnMetadata{
			Defaults: map[int32]catalog.ColumnDefault{1: {Expression: call, Dependencies: []ids.ObjectRef{call.Ref}}},
			Generations: map[int32]catalog.ColumnGeneration{2: {
				Kind: catalog.GenerationStored, Expression: call, Planned: call, Dependencies: []ids.ObjectRef{call.Ref},
			}},
		},
		SQLIndex: &catalog.IndexDefinition{Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{
			Definition: call, Execution: call, Dependencies: []ids.ObjectRef{call.Ref},
		}}}},
	}
	next.SetDescriptor(address, consumer)
	next.SetEpoch(next.Epoch() + 1)
	if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
		t.Fatal("relocated consumer", err)
	}
	relocated := next.MustRelation(address)
	assertCall := func(node expr.Expr) {
		t.Helper()
		actual := node.(plan.InstalledCryptoExpr)
		if actual.Ref.Address != mapping[call.Ref.Address] || actual.Ref.Epoch != next.Epoch() || actual.Procedure != next.Get(actual.Ref.Address) {
			t.Fatal("stale relocated provider", actual.Ref)
		}
		if err := stored.ValidateInstalledCrypto(actual, view); err != nil {
			t.Fatal(err)
		}
		value, err := actual.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		raw, _ := value.Binary()
		if err != nil || hex.EncodeToString([]byte(raw)) != "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" {
			t.Fatal("relocated HMAC", raw, err)
		}
	}
	assertCall(relocated.ColumnMeta.Defaults[1].Expression)
	assertCall(relocated.ColumnMeta.Generations[2].Expression)
	assertCall(relocated.ColumnMeta.Generations[2].Planned)
	assertCall(relocated.SQLIndex.Keys[0].Derived.Definition)
	assertCall(relocated.SQLIndex.Keys[0].Derived.Execution)
	for _, dependency := range []ids.ObjectRef{
		relocated.ColumnMeta.Defaults[1].Dependencies[0],
		relocated.ColumnMeta.Generations[2].Dependencies[0],
		relocated.SQLIndex.Keys[0].Derived.Dependencies[0],
	} {
		if dependency.Address != mapping[call.Ref.Address] || dependency.Epoch != next.Epoch() {
			t.Fatal("stale consumer dependency", dependency)
		}
	}
	project := &plan.ProjectNode{
		QuerySchema: plan.QuerySchema{{Name: "hmac", Usage: call.ResultUsage()}},
		Child:       &plan.ValuesNode{}, Expressions: []expr.Expr{call},
	}
	ref := func(ref ids.ObjectRef) ids.ObjectRef {
		if target, ok := mapping[ref.Address]; ok {
			ref.Address = target
		}
		ref.Epoch = next.Epoch()
		return ref
	}
	query, err := stored.RemapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal("relocated query", err)
	}
	assertCall(query.(*plan.ProjectNode).Expressions[0])
	if consumer.ColumnMeta.Defaults[1].Dependencies[0] != call.Ref || call.Procedure != before.Get(call.Ref.Address) {
		t.Fatal("source consumer mutated")
	}
	for _, name := range []string{"gen_random_bytes", "gen_random_uuid", "fips_mode"} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}}}
		if name == "gen_random_bytes" {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 16}}}
		}
		node, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(name, err)
		}
		original := node.(plan.InstalledCryptoExpr)
		project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "value", Usage: original.ResultUsage()}}, Child: &plan.ValuesNode{}, Expressions: []expr.Expr{original}}
		query, err := stored.RemapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
		if err != nil {
			t.Fatal(name, err)
		}
		actual := query.(*plan.ProjectNode).Expressions[0].(plan.InstalledCryptoExpr)
		if actual.Ref.Address != mapping[original.Ref.Address] || actual.Ref.Epoch != next.Epoch() || actual.Procedure != next.Get(actual.Ref.Address) || len(actual.Args) != len(original.Args) || actual.ResultUsage() != original.ResultUsage() || exec.PlannedVolatility(actual) != expr.Volatile || !analyze.ClosedPgcryptoDefault(actual) {
			t.Fatal("relocated random/FIPS recipe", name, actual)
		}
		if err := stored.ValidateInstalledCrypto(actual, view); err != nil {
			t.Fatal(name, err)
		}
		if original.Procedure != before.Get(original.Ref.Address) || original.Ref.Address == actual.Ref.Address {
			t.Fatal("source random/FIPS identity changed")
		}
	}
}
