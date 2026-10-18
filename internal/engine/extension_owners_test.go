package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
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
			t.Fatal("valid manifest rejected", installed.ext.name, err)
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
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.address: {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	next := cloneCatalog(before.state)
	for address := range mapping {
		delete(next.names, descriptorNameKey(before.Get(address)))
		delete(next.descriptors, address)
	}
	for address, destination := range mapping {
		descriptor := remapExtensionDescriptor(before.Get(address), mapping)
		next.descriptors[destination] = descriptor
		next.names[descriptorNameKey(descriptor)] = destination
	}
	view := e.catalog.read(next)
	remapped := next.MustExtension(mapping[ext.address])
	if err := (extensionCatalogProvider{ids.ExtensionClassID}).Validate(view, remapped); err != nil {
		t.Fatal("remapped extension", err)
	}
	for i, original := range ext.members {
		proc := next.MustProcedure(mapping[original])
		if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, proc); err != nil {
			t.Fatal("remapped procedure", err)
		}
		old := before.MustProcedure(original)
		if remapped.members[i] != proc.address || proc.extension != remapped.address || proc.namespace != old.namespace || proc.owner != old.owner || proc.crypto != old.crypto || proc.signature != old.signature || proc.result != old.result || proc.strict != old.strict || proc.volatility != old.volatility {
			t.Fatal("remapped member identity", proc)
		}
		if old.address != original || old.extension != ext.address || ext.members[i] != original {
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
		address:  address,
		defaults: map[int32]columnDefault{1: {expression: call, dependencies: []ids.ObjectRef{call.ref}}},
		generations: map[int32]columnGeneration{2: {
			kind: generationStored, expression: call, planned: call, dependencies: []ids.ObjectRef{call.ref},
		}},
		sqlIndex: &indexDefinition{keys: []indexKey{{derived: &derivedIndexKey{
			definition: call, execution: call, dependencies: []ids.ObjectRef{call.ref},
		}}}},
	}
	next.descriptors[address] = consumer
	next.epoch++
	if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
		t.Fatal("relocated consumer", err)
	}
	relocated := next.MustRelation(address)
	assertCall := func(node BoundExpr) {
		t.Helper()
		actual := node.(installedCryptoFunctionExpression)
		if actual.ref.Address != mapping[call.ref.Address] || actual.ref.Epoch != next.epoch || actual.procedure != next.Get(actual.ref.Address) {
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
	assertCall(relocated.defaults[1].expression)
	assertCall(relocated.generations[2].expression)
	assertCall(relocated.generations[2].planned)
	assertCall(relocated.sqlIndex.keys[0].derived.definition)
	assertCall(relocated.sqlIndex.keys[0].derived.execution)
	for _, dependency := range []ids.ObjectRef{
		relocated.defaults[1].dependencies[0],
		relocated.generations[2].dependencies[0],
		relocated.sqlIndex.keys[0].derived.dependencies[0],
	} {
		if dependency.Address != mapping[call.ref.Address] || dependency.Epoch != next.epoch {
			t.Fatal("stale consumer dependency", dependency)
		}
	}
	project := &projectNode{
		querySchema: querySchema{{Name: "hmac", Usage: call.ResultUsage()}},
		child:       &valuesNode{}, expressions: []BoundExpr{call},
	}
	ref := func(ref ids.ObjectRef) ids.ObjectRef {
		if target, ok := mapping[ref.Address]; ok {
			ref.Address = target
		}
		ref.Epoch = next.epoch
		return ref
	}
	query, err := remapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal("relocated query", err)
	}
	assertCall(query.(*projectNode).expressions[0])
	if consumer.defaults[1].dependencies[0] != call.ref || call.procedure != before.Get(call.ref.Address) {
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
		project := &projectNode{querySchema: querySchema{{Name: "value", Usage: original.ResultUsage()}}, child: &valuesNode{}, expressions: []BoundExpr{original}}
		query, err := remapViewQuery(view, project, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
		if err != nil {
			t.Fatal(name, err)
		}
		actual := query.(*projectNode).expressions[0].(installedCryptoFunctionExpression)
		if actual.ref.Address != mapping[original.ref.Address] || actual.ref.Epoch != next.epoch || actual.procedure != next.Get(actual.ref.Address) || len(actual.Args) != len(original.Args) || actual.ResultUsage() != original.ResultUsage() || plannedVolatility(actual) != Volatile || !closedPgcryptoDefault(actual) {
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
