package enginetests_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func policyUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.PolicyClassID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth}}
}

func TestPGPolicyMetadata(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := policyUnitInput(t, f, f.e.CatalogRead())
	names := []string{"oid", "polname", "polrelid", "polcmd", "polpermissive", "polroles", "polqual", "polwithcheck"}
	types := []sqltype.ID{26, 19, 26, 18, 16, 1028, 194, 194}
	for i, column := range in.Source.Columns {
		fact := in.Source.ColumnMeta.CatalogAttributes[i]
		collation := uint32(0)
		if i == 1 || i >= 6 {
			collation = 950
		}
		dimensions := int16(0)
		if i == 5 {
			dimensions = 1
		}
		if column.Name != names[i] || column.Usage.Type.ID() != types[i] || column.OriginTable != 3256 || column.OriginAttribute != int16(i+1) || !column.NullabilityKnown || column.Nullable != (i >= 6) || fact.NotNull != (i < 6) || fact.Collation != collation || fact.Dimensions != dimensions {
			t.Fatal("policy physical column", i, column, fact)
		}
	}
	if !pgcatalog.CatalogHasIndexes(3256) || pgcatalog.ClassKind(in.Catalog, in.Source) != "r" {
		t.Fatal("policy physical relation facts")
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("policy row/array links")
	}
	produce := registeredCatalogProducer(3256)
	rows, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil || rows == nil || rows.Anonymous == nil || len(rows.Anonymous) != 0 || rows.Ref != in.Ref {
		t.Fatal("typed empty policy capture", rows, err)
	}
}

func TestPGPolicyProducerAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	produce := registeredCatalogProducer(3256)
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.SessionSQL(t, s, "CREATE TABLE authority_policy(id INT)")
			enginetest.SessionSQL(t, s, "CREATE POLICY p ON authority_policy USING(id>0)")
		}
		in := policyUnitInput(t, f, f.e.CatalogRead())
		if rows, err := produce(in); err != nil || rows == nil || len(rows) != map[bool]int{false: 0, true: 1}[populated] {
			t.Fatal("valid authority", rows, err)
		}
		reject := func(label string, bad pgcatalog.Input) {
			t.Helper()
			rows, err := produce(bad)
			if rows != nil || err == nil {
				t.Fatal(label, "accepted authority", rows, err)
			}
		}
		foreign := engine.New().CatalogRead()
		for _, owner := range []*catalog.Owner{nil, foreign.Owner()} {
			bad := in
			bad.Owner = owner
			reject("owner", bad)
		}
		bad := in
		bad.Catalog = catalog.Read{}
		reject("zero catalog", bad)
		bad = in
		bad.Ref.Epoch++
		reject("epoch", bad)
		bad = in
		bad.Ref = foreign.Owner().Domain.Ref(in.Ref.Epoch, in.Ref.Address)
		reject("domain", bad)
		bad = in
		bad.Ref.Address.SubID = 1
		reject("address", bad)
		bad = in
		clone := *in.Source
		bad.Source = &clone
		reject("cloned source", bad)
		bad = in
		bad.Roles = authorization.Read{}
		reject("zero roles", bad)
		bad = in
		bad.Runtime = core.EngineCatalogRuntime{}
		reject("zero runtime authority", bad)
		for _, shape := range []string{"width", "name", "type", "nullability"} {
			state := catalog.CloneState(in.Catalog.State())
			clone := *in.Source
			clone.Columns = append([]catalog.Column(nil), in.Source.Columns...)
			switch shape {
			case "width":
				clone.Columns = clone.Columns[:7]
			case "name":
				clone.Columns[0].Name = "foreign"
			case "type":
				clone.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Oid)
			case "nullability":
				clone.Columns[7].Nullable = false
			}
			state.SetDescriptor(clone.Address(), &clone)
			bad := policyUnitInput(t, f, f.e.Catalog.Read(state))
			reject(shape, bad)
		}
		if populated {
			address := enginetest.CatalogRef(t, f.e, in.Catalog, "authority_policy").Address
			for _, malformed := range []string{"oid", "command", "roles", "principal", "public", "payload"} {
				state := catalog.CloneState(in.Catalog.State())
				relation := *in.Catalog.MustRelation(address)
				relation.Security.Policies = append([]catalog.PolicyDefinition(nil), relation.Security.Policies...)
				p := &relation.Security.Policies[0]
				switch malformed {
				case "oid":
					p.OID = 0
				case "command":
					p.Command = "bad"
				case "roles":
					p.Roles = nil
				case "principal":
					p.Roles = []catalog.PolicyRole{{Ref: (&authorization.Domain{}).Principal(10)}}
				case "public":
					p.Roles = []catalog.PolicyRole{{Public: true, Ref: f.owner}}
				case "payload":
					p.Using = stored.NewPolicy(nil, nil, nil)
				}
				state.SetDescriptor(address, &relation)
				bad := policyUnitInput(t, f, f.e.Catalog.Read(state))
				reject(malformed, bad)
			}
		}
	}
}

func TestPGPolicyPayloadOwnership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{"CREATE TABLE policy_owner(id INT)", "CREATE TABLE policy_provider(id INT)", "CREATE POLICY p ON policy_owner USING(EXISTS(SELECT 1 FROM policy_provider WHERE policy_provider.id=policy_owner.id))"} {
		enginetest.SessionSQL(t, s, q)
	}
	before := f.e.CatalogRead()
	address := enginetest.CatalogRef(t, f.e, before, "policy_owner").Address
	relation := before.MustRelation(address)
	payload := stored.PolicyOf(relation.Security.Policies[0].Using)
	presentation := payload.Presentation()
	// Policy binding owns an expression frame, not a fabricated SELECT.
	// Display must preserve that exact frame and every real nested SELECT.
	root, ok := presentation.Query().Root.(*plan.ProjectNode)
	if !ok || root.Syntax != nil || presentation.Syntax() != nil || presentation.Frames()[1] != root || len(presentation.Queries()) == 0 {
		t.Fatal("fixture lacks actual frozen expression/subquery frames")
	}
	frames := make(map[uint32]*plan.ProjectNode, len(presentation.Frames()))
	for frame, project := range presentation.Frames() {
		frames[frame] = project
	}
	sources := append([]stored.ViewLeaf(nil), presentation.Sources()...)
	manifest := append([]catalog.ExpressionDependency(nil), presentation.Dependencies()...)
	if len(manifest) == 0 || len(sources) < 2 {
		t.Fatal("fixture lacks complete owner/provider manifest")
	}
	in := policyUnitInput(t, f, before)
	rows, err := registeredCatalogProducer(3256)(in)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	carrier, err := rows[0][6].Tree()
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := carrier.(stored.PolicyCarrier)
	if !ok || actual.Policy != payload || actual.Policy.Presentation() != presentation || !reflect.DeepEqual(actual.Policy.Expression(), payload.Expression()) {
		t.Fatal("detached policy/presentation/expression")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if text, err := rows[0][6].Type().Output(canceled, rows[0][6]); text != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled lazy output", text, err)
	}
	raw, err := rows[0][6].Type().Output(t.Context(), rows[0][6])
	if err != nil || !strings.Contains(raw, "SubLink") || strings.Contains(raw, "SELECT ") {
		t.Fatal("structural subquery output", raw, err)
	}
	display, err := stored.PolicyDisplay(t.Context(), before, relation, actual, stored.Presentation{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stored.PolicyDisplay(canceled, before, relation, actual, stored.Presentation{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled display", err)
	}
	if presentation.Query().Root != root || root.Syntax != nil || !reflect.DeepEqual(presentation.Frames(), frames) {
		t.Fatal("display replaced or mutated frozen expression/query frames")
	}
	for _, q := range []string{"ALTER POLICY p ON policy_owner USING(id>9)", "ALTER POLICY p ON policy_owner RENAME TO renamed", "DROP POLICY renamed ON policy_owner", "CREATE POLICY p ON policy_owner USING(false)"} {
		enginetest.SessionSQL(t, s, q)
	}
	if f.e.CatalogRead().MustRelation(address).Security.Policies[0].Using == payload || payload.Presentation() != presentation || !reflect.DeepEqual(presentation.Sources(), sources) {
		t.Fatal("replacement changed predecessor")
	}
	if !reflect.DeepEqual(presentation.Dependencies(), manifest) || &actual.Policy.Presentation().Dependencies()[0] != &presentation.Dependencies()[0] {
		t.Fatal("carrier lost original manifest backing")
	}
	retained, err := stored.PolicyDisplay(t.Context(), before, relation, actual, stored.Presentation{})
	if err != nil || retained != display {
		t.Fatal("retained display adopted replacement", retained, display, err)
	}
	if presentation.Query().Root != root || !reflect.DeepEqual(presentation.Frames(), frames) {
		t.Fatal("retained display changed original frame identities")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				text, err := rows[0][6].Type().Output(context.Background(), rows[0][6])
				if err != nil || text != raw {
					t.Error("concurrent lazy output", text, err)
				}
			}
		}()
	}
	wg.Wait()
	typ := rows[0][6].Type()
	clone := *relation
	if _, err := stored.PolicyTree(t.Context(), before, &clone, typ, payload); err == nil {
		t.Fatal("copied policy owner admitted")
	}
	if _, err := stored.PolicyTree(t.Context(), f.e.CatalogRead(), relation, typ, payload); err == nil {
		t.Fatal("retired policy owner admitted")
	}
}

func TestPGPolicyDisplayRendering(t *testing.T) {
	for _, tc := range []struct {
		name, expression, plain, pretty string
	}{
		{
			"scalar",
			"(SELECT policy_provider.id>0 FROM policy_provider ORDER BY policy_provider.id LIMIT 1)",
			"( SELECT (policy_provider.id > 0)\n   FROM policy_provider\n  ORDER BY policy_provider.id\n LIMIT 1)",
			"( SELECT policy_provider.id > 0\n   FROM policy_provider\n  ORDER BY policy_provider.id\n LIMIT 1)",
		},
		{
			"window",
			"(SELECT row_number() OVER(ORDER BY policy_provider.id)>0 FROM policy_provider LIMIT 1)",
			"( SELECT (row_number() OVER (ORDER BY policy_provider.id) > 0)\n   FROM policy_provider\n LIMIT 1)",
			"( SELECT row_number() OVER (ORDER BY policy_provider.id) > 0\n   FROM policy_provider\n LIMIT 1)",
		},
		{
			"row",
			"ROW(id,allowed)=(SELECT 1,true)",
			"((id, allowed) = ( SELECT 1,\n    true))",
			"((id, allowed) = ( SELECT 1,\n    true))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			s := enginetest.StartSession(t, f.e, nil)
			for _, q := range []string{
				"CREATE TABLE policy_owner(id INT, allowed BOOL)",
				"CREATE TABLE policy_provider(id INT)",
				"CREATE POLICY p ON policy_owner USING(" + tc.expression + ")",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			view := f.e.CatalogRead()
			relation := view.MustRelation(enginetest.CatalogRef(t, f.e, view, "policy_owner").Address)
			payload := stored.PolicyOf(relation.Security.Policies[0].Using)
			root := payload.Presentation().Query().Root
			typ, _ := view.LookupType(sqltype.PgNodeTree)
			value, err := stored.PolicyTree(t.Context(), view, relation, typ, payload)
			if err != nil {
				t.Fatal(err)
			}
			tree, err := value.Tree()
			if err != nil {
				t.Fatal(err)
			}
			for _, pretty := range []bool{false, true} {
				want := tc.plain
				if pretty {
					want = tc.pretty
				}
				got, err := stored.PolicyDisplay(t.Context(), view, relation, tree.(stored.PolicyCarrier), stored.Presentation{
					Pretty: pretty,
					Path: []ids.ObjectAddress{
						{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
						{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
					},
				})
				if err != nil || got != want {
					t.Fatalf("pretty=%t got %q, %v; want %q", pretty, got, err, want)
				}
				if payload.Presentation().Query().Root != root || tree.(stored.PolicyCarrier).Policy != payload {
					t.Fatal("display changed frozen policy ownership")
				}
			}
		})
	}
}

func TestPGPolicyDisplayIdentityKeywords(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{
		"CREATE TABLE policy_owner(id INT)",
		"CREATE POLICY p ON policy_owner USING(current_user=current_role)",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	view := f.e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, f.e, view, "policy_owner").Address)
	payload := stored.PolicyOf(relation.Security.Policies[0].Using)
	root := payload.Presentation().Query().Root
	for _, pretty := range []bool{false, true} {
		want := "(CURRENT_USER = CURRENT_ROLE)"
		if pretty {
			want = "CURRENT_USER = CURRENT_ROLE"
		}
		got, err := stored.PolicyDisplay(t.Context(), view, relation, stored.PolicyCarrier{Policy: payload}, stored.Presentation{Pretty: pretty})
		if err != nil || got != want {
			t.Fatalf("pretty=%t got %q, %v; want %q", pretty, got, err, want)
		}
		replay, err := stored.PolicySQL(t.Context(), view, payload)
		if err != nil || replay != "(current_user = current_role)" {
			t.Fatalf("pretty=%t changed replay: %q, %v", pretty, replay, err)
		}
		if payload.Presentation().Query().Root != root || stored.PolicyOf(relation.Security.Policies[0].Using) != payload {
			t.Fatal("display changed frozen policy ownership")
		}
	}
}

func TestPGPolicyDisplayRestoreSources(t *testing.T) {
	for _, tc := range []struct {
		name, condition, want, source string
	}{
		{"table", "EXISTS(SELECT 1 FROM policy_provider WHERE policy_provider.id=policy_owner.id)",
			"(EXISTS ( SELECT 1\n   FROM policy_provider\n  WHERE (policy_provider.id = policy_owner.id)))", "FROM policy_provider"},
		{"user_alias", "EXISTS(SELECT 1 FROM policy_provider AS __view_source_0(kept) WHERE __view_source_0.kept=policy_owner.id)",
			"(EXISTS ( SELECT 1\n   FROM policy_provider __view_source_0(kept)\n  WHERE (__view_source_0.kept = policy_owner.id)))", "__view_source_0(kept)"},
		{"derived", "EXISTS(SELECT 1 FROM (SELECT id FROM policy_provider) AS d(kept) WHERE d.kept=policy_owner.id)", "", "d(kept)"},
		{"values", "EXISTS(SELECT 1 FROM (VALUES(1)) AS v(kept) WHERE v.kept=policy_owner.id)", "", "v(kept)"},
		{"function", "EXISTS(SELECT 1 FROM generate_series(1,2) AS f(kept) WHERE f.kept=policy_owner.id)", "", "f(kept)"},
		{"join", "EXISTS(SELECT 1 FROM policy_provider AS a JOIN policy_provider AS b ON a.id=b.id WHERE a.id=policy_owner.id)", "", "JOIN policy_provider b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			s := enginetest.StartSession(t, f.e, nil)
			for _, q := range []string{
				"CREATE TABLE policy_owner(id INT)",
				"CREATE TABLE policy_provider(id INT)",
				"INSERT INTO policy_owner VALUES(1),(3)",
				"INSERT INTO policy_provider VALUES(1)",
				"CREATE POLICY p ON policy_owner USING(" + tc.condition + ")",
				"ALTER TABLE policy_owner ENABLE ROW LEVEL SECURITY",
				"ALTER TABLE policy_owner FORCE ROW LEVEL SECURITY",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			path := []ids.ObjectAddress{
				{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
				{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
			}
			display := func(view catalog.Read, relation *catalog.Relation, payload *stored.Policy, pretty bool) string {
				t.Helper()
				got, err := stored.PolicyDisplay(t.Context(), view, relation, stored.PolicyCarrier{Policy: payload}, stored.Presentation{Pretty: pretty, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			capture := func() (catalog.Read, *catalog.Relation, *stored.Policy) {
				view := f.e.CatalogRead()
				relation := view.MustRelation(enginetest.CatalogRef(t, f.e, view, "policy_owner").Address)
				return view, relation, stored.PolicyOf(relation.Security.Policies[0].Using)
			}
			originalView, originalRelation, original := capture()
			originalRoot, originalExpression := original.Presentation().Query().Root, original.Expression()
			want := [2]string{display(originalView, originalRelation, original, false), display(originalView, originalRelation, original, true)}
			if tc.want != "" && want[0] != tc.want {
				t.Fatalf("original logical display %q; want %q", want[0], tc.want)
			}
			if !strings.Contains(want[0], tc.source) {
				t.Fatalf("missing genuine source names %q in %q", tc.source, want[0])
			}
			dump := func() string {
				t.Helper()
				var out bytes.Buffer
				if err := engine.Dump(t.Context(), f.e, &out); err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			stream := dump()
			if tc.name == "table" && strings.Contains(stream, "__view_source_") {
				t.Fatal("policy replay invented a logical alias", stream)
			}
			for round := range 2 {
				if err := engine.Restore(t.Context(), f.e, strings.NewReader(stream)); err != nil {
					t.Fatal(err)
				}
				if got := dump(); got != stream {
					t.Fatalf("round %d changed canonical replay\n%s\n%s", round, stream, got)
				}
				view, relation, payload := capture()
				for i, pretty := range []bool{false, true} {
					if got := display(view, relation, payload, pretty); got != want[i] {
						t.Fatalf("round %d pretty=%t got %q; want %q", round, pretty, got, want[i])
					}
				}
			}
			enginetest.SessionSQL(t, s, "ALTER TABLE policy_provider RENAME TO renamed_provider")
			enginetest.SessionSQL(t, s, "ALTER TABLE renamed_provider RENAME COLUMN id TO renamed_id")
			renamedView, renamedRelation, renamed := capture()
			renamedWant := [2]string{display(renamedView, renamedRelation, renamed, false), display(renamedView, renamedRelation, renamed, true)}
			if tc.name == "table" {
				expected := "(EXISTS ( SELECT 1\n   FROM renamed_provider\n  WHERE (renamed_provider.renamed_id = policy_owner.id)))"
				if renamedWant[0] != expected {
					t.Fatalf("renamed captured identities %q; want %q", renamedWant[0], expected)
				}
			}
			stream = dump()
			if err := engine.Restore(t.Context(), f.e, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			if got := dump(); got != stream {
				t.Fatal("renamed provider replay is not stable", stream, got)
			}
			view, relation, payload := capture()
			for i, pretty := range []bool{false, true} {
				if got := display(view, relation, payload, pretty); got != renamedWant[i] {
					t.Fatalf("renamed restore pretty=%t got %q; want %q", pretty, got, renamedWant[i])
				}
				if got := display(originalView, originalRelation, original, pretty); got != want[i] {
					t.Fatal("replacement changed retained predecessor", got, want[i])
				}
			}
			if original.Presentation().Query().Root != originalRoot || !reflect.DeepEqual(original.Expression(), originalExpression) || stored.PolicyOf(originalRelation.Security.Policies[0].Using) != original {
				t.Fatal("render/replacement changed actual immutable payload authority")
			}
		})
	}
}

func TestPGPolicyCaptureCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{"CREATE TABLE cancel_policy(id INT)", "CREATE POLICY a ON cancel_policy USING(id>0)", "CREATE POLICY b ON cancel_policy USING(id>1)"} {
		enginetest.SessionSQL(t, s, q)
	}
	in := policyUnitInput(t, f, f.e.CatalogRead())
	produce := registeredCatalogProducer(3256)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bad := in
	bad.Context = ctx
	if rows, err := core.CaptureCatalogSourceRows(bad, produce); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture prefix", rows, err)
	}
	if rows, err := core.CaptureCatalogSourceRows(in, produce); err != nil || rows == nil || len(rows.Anonymous) != 2 {
		t.Fatal("healthy capture after cancel", rows, err)
	}
	// Cancellation after an admitted first payload must still discard all rows.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	bad = in
	bad.Context = ctx
	bad.Runtime = cancelPolicyRuntime{Runtime: in.Runtime, cancel: cancel}
	if rows, err := core.CaptureCatalogSourceRows(bad, produce); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("mid-capture cancellation prefix", rows, err)
	}
	if rows, err := produce(in); err != nil || len(rows) != 2 {
		t.Fatal("retry after mid-capture cancellation", rows, err)
	}
}

type cancelPolicyRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
}

func (r cancelPolicyRuntime) PolicyTree(ctx context.Context, view catalog.Read, relation *catalog.Relation, typ *sqltype.Descriptor, payload catalog.PolicyPayload) (sqltype.Value, error) {
	value, err := r.Runtime.PolicyTree(ctx, view, relation, typ, payload)
	r.cancel()
	return value, err
}
