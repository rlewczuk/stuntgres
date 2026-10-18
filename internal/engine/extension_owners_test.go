package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgcryptoOwnerMetadata(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	metadata, err := extensionOwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	var records []extensionOwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION pgcrypto;\n"+metadata, parser.Options{
		Context: context.Background(), StandardConformingStrings: true,
		LineComment: func(comment string) error {
			record, found, err := decodeExtensionOwners(comment)
			if err == nil && found {
				records = append(records, record)
			}
			return err
		},
	})
	expected := extensionOwnerRecord{Name: "pgcrypto", Schema: "public", Version: "1.4", Owner: "postgres", Members: []extensionMemberOwner{
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
	for _, installed := range []struct {
		e   *Engine
		ext *extensionDescriptor
	}{{unaccent, unaccentExt}, {intarray, intarrayExt}, {crypto, cryptoExt}, {intagg, intaggExt}} {
		metadata, err := extensionOwnerMetadata(installed.e.CatalogRead(), installed.e.Roles(), installed.ext)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := decodeExtensionOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
		if err != nil || !found {
			t.Fatal("valid manifest rejected", installed.ext.Name(), err)
		}
		for _, kind := range []string{"version", "missing", "extra", "duplicate", "name", "signature", "foreign_procedure", "foreign_dictionary", "foreign_type", "owner"} {
			t.Run(record.Name+"/"+kind, func(t *testing.T) {
				bad := record
				bad.Members = slices.Clone(record.Members)
				switch kind {
				case "version":
					bad.Version = "missing"
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
					bad.Members[0] = extensionMemberOwner{Kind: "procedure", Name: "unaccent", Signature: "25", Owner: "postgres"}
					if record.Name == "unaccent" {
						bad.Members[0] = extensionMemberOwner{Kind: "procedure", Name: "digest", Signature: "25,25", Owner: "postgres"}
					}
				case "foreign_dictionary":
					bad.Members[0] = extensionMemberOwner{Kind: "dictionary", Name: "query_int", Owner: "postgres"}
				case "foreign_type":
					bad.Members[0] = extensionMemberOwner{Kind: "type", Name: "unaccent", Owner: "postgres"}
				case "owner":
					bad.Members[0].Owner = ""
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if _, found, err := decodeExtensionOwners(extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString(data)); !found || err == nil {
					t.Fatal("invalid manifest accepted", bad)
				}
			})
		}
	}
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
		descriptor := remapExtensionDescriptor(before.Get(address), mapping)
		next.SetDescriptor(destination, descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), destination)
	}
	view := e.catalog.read(next)
	remapped := next.MustExtension(mapping[ext.Address()])
	if err := (extensionCatalogProvider{ids.ExtensionClassID}).Validate(view, remapped); err != nil {
		t.Fatal("remapped extension", err)
	}
	for i, original := range ext.Members {
		proc := next.MustProcedure(mapping[original])
		if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal("remapped procedure", err)
		}
		old := before.MustProcedure(original)
		if remapped.Members[i] != proc.Address() || proc.Extension != remapped.Address() || proc.Namespace() != old.Namespace() || proc.Owner != old.Owner || pgcryptoProviderOf(proc) != pgcryptoProviderOf(old) || proc.Signature != old.Signature || proc.Result != old.Result || proc.Strict != old.Strict || proc.Volatility != old.Volatility {
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
	bound, err := binder.bindFunctionProvider(parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "hmac"}}},
		Args: []parser.Expr{literal("what do ya want for nothing?"), literal("Jefe"), literal("sha256")},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(installedCryptoFunctionExpression)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900010}
	consumer := &relationDescriptor{
		ObjectAddress: address,
		Defaults:      map[int32]columnDefault{1: {Expression: call, Dependencies: []ids.ObjectRef{call.ref}}},
		Generations: map[int32]columnGeneration{2: {
			Kind: generationStored, Expression: call, Planned: call, Dependencies: []ids.ObjectRef{call.ref},
		}},
		SQLIndex: &indexDefinition{Keys: []indexKey{{Derived: &derivedIndexKey{
			Definition: call, Execution: call, Dependencies: []ids.ObjectRef{call.ref},
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
		actual := node.(installedCryptoFunctionExpression)
		if actual.ref.Address != mapping[call.ref.Address] || actual.ref.Epoch != next.Epoch() || actual.procedure != next.Get(actual.ref.Address) {
			t.Fatal("stale relocated provider", actual.ref)
		}
		if err := actual.validate(view); err != nil {
			t.Fatal(err)
		}
		value, err := actual.Eval(&EvalContext{Context: context.Background()})
		raw, _ := value.Binary()
		if err != nil || hex.EncodeToString([]byte(raw)) != "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" {
			t.Fatal("relocated HMAC", raw, err)
		}
	}
	assertCall(relocated.Defaults[1].Expression)
	assertCall(relocated.Generations[2].Expression)
	assertCall(relocated.Generations[2].Planned)
	assertCall(relocated.SQLIndex.Keys[0].Derived.Definition)
	assertCall(relocated.SQLIndex.Keys[0].Derived.Execution)
	for _, dependency := range []ids.ObjectRef{
		relocated.Defaults[1].Dependencies[0],
		relocated.Generations[2].Dependencies[0],
		relocated.SQLIndex.Keys[0].Derived.Dependencies[0],
	} {
		if dependency.Address != mapping[call.ref.Address] || dependency.Epoch != next.Epoch() {
			t.Fatal("stale consumer dependency", dependency)
		}
	}
	project := &projectNode{
		querySchema: querySchema{{Name: "hmac", Usage: call.ResultUsage()}},
		child:       &valuesNode{}, expressions: []expr.Expr{call},
	}
	ref := func(ref ids.ObjectRef) ids.ObjectRef {
		if target, ok := mapping[ref.Address]; ok {
			ref.Address = target
		}
		ref.Epoch = next.Epoch()
		return ref
	}
	query, err := remapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal("relocated query", err)
	}
	assertCall(query.(*projectNode).expressions[0])
	if consumer.Defaults[1].Dependencies[0] != call.ref || call.procedure != before.Get(call.ref.Address) {
		t.Fatal("source consumer mutated")
	}
	for _, name := range []string{"gen_random_bytes", "gen_random_uuid", "fips_mode"} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}}}
		if name == "gen_random_bytes" {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 16}}}
		}
		node, err := binder.bindFunctionProvider(raw)
		if err != nil {
			t.Fatal(name, err)
		}
		original := node.(installedCryptoFunctionExpression)
		project := &projectNode{querySchema: querySchema{{Name: "value", Usage: original.ResultUsage()}}, child: &valuesNode{}, expressions: []expr.Expr{original}}
		query, err := remapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
		if err != nil {
			t.Fatal(name, err)
		}
		actual := query.(*projectNode).expressions[0].(installedCryptoFunctionExpression)
		if actual.ref.Address != mapping[original.ref.Address] || actual.ref.Epoch != next.Epoch() || actual.procedure != next.Get(actual.ref.Address) || len(actual.Args) != len(original.Args) || actual.ResultUsage() != original.ResultUsage() || plannedVolatility(actual) != expr.Volatile || !closedPgcryptoDefault(actual) {
			t.Fatal("relocated random/FIPS recipe", name, actual)
		}
		if err := actual.validate(view); err != nil {
			t.Fatal(name, err)
		}
		if original.procedure != before.Get(original.ref.Address) || original.ref.Address == actual.ref.Address {
			t.Fatal("source random/FIPS identity changed")
		}
	}
}
