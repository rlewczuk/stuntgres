package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func policiesUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PoliciesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth}}
}

func TestPGPoliciesMetadata(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := policiesUnitInput(t, f, f.e.CatalogRead())
	names := []string{"schemaname", "tablename", "policyname", "permissive", "roles", "cmd", "qual", "with_check"}
	types := []sqltype.ID{19, 19, 19, 25, 1003, 25, 25, 25}
	for i, column := range in.Source.Columns {
		collation := uint32(100)
		if i < 3 || i == 4 {
			collation = 950
		}
		dimensions := int16(0)
		if i == 4 {
			dimensions = 1
		}
		// View attributes report attndims=0 independently of the descriptor's
		// declared array dimension and the genuine NAME[] datum shape.
		fact := in.Source.ColumnMeta.CatalogAttributes[i]
		if column.Name != names[i] || column.Usage.Type.ID() != types[i] || column.DeclaredDimensions != dimensions || column.Usage.Collation.ID != collation || column.OriginTable != 12018 || column.OriginAttribute != int16(i+1) || !column.NullabilityKnown || !column.Nullable || fact.NotNull || fact.Collation != collation || fact.Dimensions != 0 {
			t.Fatal("policies descriptor", i, column, fact)
		}
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12020})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12019})
	if pgcatalog.ClassKind(in.Catalog, in.Source) != "v" || in.Source.RowType != 12020 || row.Array != 12019 || row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("policies relation/row/array ownership")
	}
	rows, err := core.CaptureCatalogSourceRows(in, registeredCatalogProducer(pgcatalog.PoliciesOID))
	if err != nil || rows == nil || rows.Anonymous == nil || len(rows.Anonymous) != 0 || rows.Ref != in.Ref {
		t.Fatal("owned empty capture", rows, err)
	}
}

func TestPGPoliciesProjectionLifetime(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{"CREATE ROLE policies_z", "CREATE ROLE policies_a", "CREATE TABLE policies_owner(id INT)", "CREATE POLICY p ON policies_owner AS RESTRICTIVE FOR UPDATE TO policies_z,policies_a,policies_a USING(id>0)"} {
		enginetest.SessionSQL(t, s, q)
	}
	before := f.e.CatalogRead()
	in := policiesUnitInput(t, f, before)
	address := enginetest.CatalogRef(t, f.e, before, "policies_owner").Address
	relation := before.MustRelation(address)
	payload := stored.PolicyOf(relation.Security.Policies[0].Using)
	presentation := payload.Presentation()
	root := presentation.Query().Root
	dependencies := presentation.Dependencies()
	roleRefs := append([]catalog.PolicyRole(nil), relation.Security.Policies[0].Roles...)
	produce := registeredCatalogProducer(pgcatalog.PoliciesOID)
	rows, err := produce(in)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	want := []string{"public", "policies_owner", "p", "RESTRICTIVE", "", "UPDATE", "(id > 0)"}
	for i, value := range rows[0] {
		if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
			t.Fatal("foreign row value", i, value)
		}
		if i == 4 || i == 7 {
			continue
		}
		if text, err := value.Text(); err != nil || text != want[i] {
			t.Fatal("independent projection", i, text, err)
		}
	}
	roles := rows[0][4]
	if roles.ArrayLen() != 2 || roles.ArrayRank() != 1 || roles.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 2}) || !rows[0][7].IsNull() {
		t.Fatal("array shape or independent CHECK absence", rows)
	}
	nameType, _ := before.LookupType(sqltype.Name)
	for i, want := range []string{"policies_a", "policies_z"} {
		v := roles.ArrayElement(i)
		if text, err := v.Text(); err != nil || text != want || v.Type() != nameType {
			t.Fatal("typed sorted distinct role", i, v, err)
		}
	}
	for _, q := range []string{"ALTER ROLE policies_z RENAME TO policies_0", "ALTER POLICY p ON policies_owner USING(id>9)", "ALTER POLICY p ON policies_owner RENAME TO replacement"} {
		enginetest.SessionSQL(t, s, q)
	}
	current := policiesUnitInput(t, f, f.e.CatalogRead())
	fresh, err := produce(current)
	if err != nil || len(fresh) != 1 {
		t.Fatal(fresh, err)
	}
	if text, _ := fresh[0][4].ArrayElement(0).Text(); text != "policies_0" {
		t.Fatal("new role root not captured", text)
	}
	if text, _ := fresh[0][6].Text(); text != "(id > 9)" {
		t.Fatal("new policy payload not captured", text)
	}
	retained, err := produce(in)
	if err != nil || !reflect.DeepEqual(retained, rows) || current.Roles == in.Roles || current.Catalog.State() == in.Catalog.State() {
		t.Fatal("old captured authority adopted replacement", retained, err)
	}
	if payload.Presentation() != presentation || presentation.Query().Root != root || !reflect.DeepEqual(relation.Security.Policies[0].Roles, roleRefs) || len(roleRefs) != 3 || len(dependencies) == 0 || &presentation.Dependencies()[0] != &dependencies[0] {
		t.Fatal("projection mutated frozen policy backing")
	}
	clone := *relation
	for _, bad := range []struct{ view catalog.Read; relation *catalog.Relation }{{before, &clone}, {current.Catalog, relation}, {engine.New().CatalogRead(), relation}} {
		if _, err := in.Runtime.PolicyDisplay(t.Context(), bad.view, bad.relation, payload); err == nil {
			t.Fatal("display accepted foreign/retired owner")
		}
	}
	bad := in
	bad.Roles = current.Roles
	if rejected, err := produce(bad); rejected != nil || err == nil {
		t.Fatal("mismatched retained principal root published rows", rejected, err)
	}
}

func TestPGPoliciesProducerAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	produce := registeredCatalogProducer(pgcatalog.PoliciesOID)
	for _, populated := range []bool{false, true} {
		if populated {
			enginetest.SessionSQL(t, s, "CREATE TABLE policies_authority(id INT)")
			enginetest.SessionSQL(t, s, "CREATE POLICY p ON policies_authority USING(id>0)")
		}
		in := policiesUnitInput(t, f, f.e.CatalogRead())
		reject := func(label string, bad pgcatalog.Input) {
			t.Helper()
			if rows, err := produce(bad); rows != nil || err == nil {
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
		bad.Catalog = foreign
		reject("catalog", bad)
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
		reject("source", bad)
		bad = in
		bad.Roles = authorization.Read{}
		reject("roles", bad)
		bad = in
		bad.Runtime = core.EngineCatalogRuntime{}
		reject("runtime", bad)
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
				clone.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Name)
			case "nullability":
				clone.Columns[7].Nullable = false
			}
			state.SetDescriptor(clone.Address(), &clone)
			reject(shape, policiesUnitInput(t, f, f.e.Catalog.Read(state)))
		}
		if !populated {
			continue
		}
		address := enginetest.CatalogRef(t, f.e, in.Catalog, "policies_authority").Address
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
			reject(malformed, policiesUnitInput(t, f, f.e.Catalog.Read(state)))
		}
	}
}

// The wrapper cancels in traversal or in the second real rendering after a
// genuine earlier row; it never supplies rows or replaces the stored renderer.
type policiesCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
	duringDisplay bool
	rendered *bool
}

func (r policiesCancelRuntime) ValidateACL(roles authorization.Read, d catalog.Descriptor) error {
	err := r.Runtime.ValidateACL(roles, d)
	if err == nil && !r.duringDisplay {
		r.cancel()
	}
	return err
}

func (r policiesCancelRuntime) PolicyDisplay(ctx context.Context, view catalog.Read, relation *catalog.Relation, payload catalog.PolicyPayload) (string, error) {
	if r.duringDisplay && *r.rendered {
		r.cancel()
	}
	text, err := r.Runtime.PolicyDisplay(ctx, view, relation, payload)
	if err == nil {
		*r.rendered = true
	}
	return text, err
}

func TestPGPoliciesCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{"CREATE TABLE policies_cancel(id INT)", "CREATE POLICY p ON policies_cancel USING(id>0)", "CREATE POLICY q ON policies_cancel USING(id>1)"} {
		enginetest.SessionSQL(t, s, q)
	}
	for _, phase := range []string{"before", "traversal", "display"} {
		t.Run(phase, func(t *testing.T) {
			in := policiesUnitInput(t, f, f.e.CatalogRead())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			in.Context = ctx
			if phase == "before" {
				cancel()
			} else {
				rendered := false
				in.Runtime = policiesCancelRuntime{Runtime: in.Runtime, cancel: cancel, duringDisplay: phase == "display", rendered: &rendered}
			}
			rows, err := registeredCatalogProducer(pgcatalog.PoliciesOID)(in)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation published a prefix", rows, err)
			}
		})
	}
}
