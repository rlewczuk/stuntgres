package stored

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func defaultAttributeFixture(t *testing.T) (catalog.Read, *catalog.Relation, *catalog.Type, plan.FieldExpr) {
	t.Helper()
	read, old, integer, _ := routineCaptureFixture(t)
	typ, err := sqltype.Composite(95000, "row_default", "row_default", 0, []sqltype.CompositeMember{{Attribute: 3, Name: "v", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	relation := *old
	relation.ObjectRevision, relation.RowType, relation.Attributes = 7, typ.ID(), []int32{3}
	d := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 5, OwningRelation: relation.Address()}
	read.State().SetDescriptor(relation.Address(), &relation)
	read.State().SetDescriptor(d.Address(), d)
	node := plan.FieldExpr{Info: expr.Info{Usage: integer.Usage()}, Base: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}, Attribute: 3}
	return read, &relation, d, node
}

func defaultAttributePayload(t *testing.T, read catalog.Read, node expr.Expr) *RoutineDefaults {
	t.Helper()
	p, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{node})
	if err != nil {
		t.Fatal("lawful composite attribute capture", err)
	}
	return p
}

func defaultAttributeCheck(t *testing.T, read catalog.Read, p *RoutineDefaults, relation *catalog.Relation, typ *catalog.Type, revision ids.DefinitionRevision) {
	t.Helper()
	address := relation.Address()
	address.SubID = 3
	want := []catalog.ExpressionDependency{
		{Ref: read.Owner().Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}), Revision: 1},
		{Ref: read.Owner().Domain.Ref(read.Epoch(), typ.Address()), Revision: 5},
		{Ref: read.Owner().Domain.Ref(read.Epoch(), address), Revision: revision},
	}
	if !slices.Equal(p.Dependencies(), want) {
		t.Fatalf("exact attribute manifest got=%#v want=%#v", p.Dependencies(), want)
	}
}

func TestRoutineDefaultAttributeCapture(t *testing.T) {
	read, relation, typ, node := defaultAttributeFixture(t)
	entries := []expr.Expr{node, node}
	p, err := NewRoutineDefaults(context.Background(), read, nil, entries)
	if err != nil {
		t.Fatal(err)
	}
	defaultAttributeCheck(t, read, p, relation, typ, 7)
	entries[0] = nil
	if _, ok := p.Default(0); !ok {
		t.Fatal("caller entries retained mutably")
	}
	if err := p.Validate(read); err != nil {
		t.Fatal(err)
	}
	text, err := p.DefaultSQL(context.Background(), read, 0)
	if err != nil || !strings.Contains(text, ".v") {
		t.Fatal(text, err)
	}
	if text, err := p.DefaultSQL(context.Background(), read, 99); err == nil || text != "" {
		t.Fatal("missing slot rendered", text, err)
	}
}

func TestRoutineDefaultAttributeAuthority(t *testing.T) {
	for _, kind := range []string{"missing-owner", "missing-type", "missing-field", "retired-field", "wrong-backing-owner", "wrong-rowtype", "wrong-usage"} {
		t.Run(kind, func(t *testing.T) {
			read, relation, typ, node := defaultAttributeFixture(t)
			switch kind {
			case "missing-owner":
				read.State().RemoveDescriptor(relation.Address())
			case "missing-type":
				read.State().RemoveDescriptor(typ.Address())
			case "missing-field":
				relation.Attributes = []int32{4}
			case "retired-field":
				relation.Attributes, relation.Columns = nil, nil
			case "wrong-backing-owner":
				typ.OwningRelation.ObjectID++
			case "wrong-rowtype":
				relation.RowType++
			case "wrong-usage":
				relation.Columns = []catalog.Column{{Name: "v", Usage: typ.SQLType.Usage()}}
			}
			if p, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{node}); err == nil || p != nil {
				t.Fatal("invalid provider admitted", p, err)
			}
		})
	}
	for _, kind := range []string{"foreign-domain", "nil-domain", "zero-epoch", "old-epoch", "future-epoch", "wrong-attribute", "missing-check", "extra-check", "payload-domain", "payload-epoch"} {
		t.Run(kind, func(t *testing.T) {
			read, relation, _, node := defaultAttributeFixture(t)
			p := defaultAttributePayload(t, read, node)
			attribute := -1
			for i, d := range p.dependencies {
				if d.Ref.Address.ClassID == ids.RelationClassID {
					attribute = i
				}
			}
			if attribute < 0 {
				t.Fatal("fixture missing real attribute capture")
			}
			d := &p.dependencies[attribute]
			switch kind {
			case "foreign-domain":
				d.Ref = new(ids.Domain).Ref(read.Epoch(), d.Ref.Address)
			case "nil-domain":
				d.Ref = ids.ObjectRef{Address: d.Ref.Address, Epoch: read.Epoch()}
			case "zero-epoch":
				d.Ref.Epoch = 0
			case "old-epoch":
				d.Ref.Epoch = read.Epoch() - 1
			case "future-epoch":
				d.Ref.Epoch = read.Epoch() + 1
			case "wrong-attribute":
				d.Ref.Address.SubID = 4
			case "missing-check":
				p.dependencies = slices.Delete(p.dependencies, attribute, attribute+1)
			case "extra-check":
				extra := *d
				extra.Ref.Address.SubID = 4
				p.dependencies = append(p.dependencies, extra)
			case "payload-domain":
				p.domain = new(ids.Domain)
			case "payload-epoch":
				p.epoch++
			}
			before := slices.Clone(p.dependencies)
			provenance := kind == "foreign-domain" || kind == "nil-domain" || kind == "zero-epoch" || kind == "old-epoch" || kind == "future-epoch" || kind == "payload-domain" || kind == "payload-epoch"
			if err := p.Validate(read); err == nil || provenance && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Errorf("Validate admitted corrupt authority or lost replacement classification: %v", err)
			}
			text, err := p.DefaultSQL(context.Background(), read, 0)
			if err == nil || text != "" || provenance && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Errorf("DefaultSQL authority: text=%q err=%v", text, err)
			}
			mapped, err := p.Rebind(read, nil, NewRoutineRemap(read, nil, nil, nil, false))
			if err == nil || mapped != nil || provenance && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Errorf("Rebind authority: mapped=%v err=%v", mapped, err)
			}
			if !slices.Equal(before, p.dependencies) || relation.Revision() != 7 {
				t.Fatal("rejected authority mutated source")
			}
		})
	}
}

func TestRoutineDefaultAttributeStaging(t *testing.T) {
	read, relation, typ, node := defaultAttributeFixture(t)
	read.State().RemoveDescriptor(relation.Address())
	read.State().RemoveDescriptor(typ.Address())
	candidate := catalog.NewCandidate(read)
	if err := candidate.Create(relation); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Create(typ); err != nil {
		t.Fatal(err)
	}
	p, err := NewRoutineDefaults(context.Background(), read, candidate.Definitions(), []expr.Expr{node})
	if err != nil {
		t.Fatal("staged-only capture", err)
	}
	defaultAttributeCheck(t, read, p, relation, typ, 7)
	var refs []ids.ObjectRef
	text, err := (ScalarRenderer{Definition: candidate.Definitions(), RoutineDefault: true, Dependencies: &refs, CapturedChecks: p.dependencies}).SQL(context.Background(), read, node)
	if err != nil || !strings.Contains(text, ".v") {
		t.Fatal(text, err)
	}
	if _, err := NewRoutineDefaults(context.Background(), read, nil, []expr.Expr{node}); err == nil {
		t.Fatal("unpublished provider leaked into base")
	}
	published := catalog.NewRead(read.Owner(), candidate.State())
	if err := p.Validate(published); err != nil {
		t.Fatal("published capture", err)
	}
	if _, err := p.DefaultSQL(context.Background(), published, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Rebind(published, nil, NewRoutineRemap(published, nil, nil, nil, false)); err != nil {
		t.Fatal(err)
	}
	// A staged replacement and then tombstone must beat the tempting base.
	override := catalog.NewCandidate(published)
	bad := *relation
	bad.Attributes = []int32{4}
	bad.ObjectRevision = 11
	if err := override.Replace(&bad); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoutineDefaults(context.Background(), published, override.Definitions(), []expr.Expr{node}); err == nil {
		t.Fatal("base resurrected staged-invalid field")
	}
	removed := catalog.NewCandidate(published)
	removed.RecordRemoval(published.Owner().Domain.Ref(published.Epoch(), relation.Address()), relation)
	if _, err := NewRoutineDefaults(context.Background(), published, removed.Definitions(), []expr.Expr{node}); err == nil {
		t.Fatal("base resurrected staged removal")
	}
	// Valid current labels/revision still preserve the original immutable stamp.
	renamed := *relation
	renamed.ObjectName, renamed.ObjectRevision = "renamed", 11
	valid := catalog.NewCandidate(published)
	if err := valid.Replace(&renamed); err != nil {
		t.Fatal(err)
	}
	next, err := routineDefaultsWithChecks(context.Background(), published, valid.Definitions(), p.entries, p.dependencies)
	if err != nil {
		t.Fatal(err)
	}
	defaultAttributeCheck(t, published, next, &renamed, typ, 7)
}

func defaultAttributeDestination(t *testing.T, source catalog.Read, relation *catalog.Relation, typ *catalog.Type) (catalog.Read, *catalog.Relation, *catalog.Type, *catalog.Type, map[ids.ObjectAddress]ids.ObjectAddress, func(*sqltype.Descriptor) *sqltype.Descriptor) {
	t.Helper()
	dest, _, integer, _ := routineCaptureFixture(t)
	makeType := func(id sqltype.ID, name string) *catalog.Type {
		d, err := sqltype.Composite(id, name, name, 0, []sqltype.CompositeMember{{Attribute: 3, Name: "v", Usage: integer.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		return &catalog.Type{SQLType: d, ObjectNamespace: relation.Namespace(), ObjectRevision: 11}
	}
	b, c := makeType(95001, "mapped"), makeType(95002, "twice")
	br := *relation
	br.Columns = []catalog.Column{{Name: "v", Usage: integer.Usage()}}
	br.ObjectAddress.ObjectID = 902
	br.RowType = b.SQLType.ID()
	br.ObjectRevision = 11
	cr := br
	cr.ObjectAddress.ObjectID = 903
	cr.RowType = c.SQLType.ID()
	b.OwningRelation, c.OwningRelation = br.Address(), cr.Address()
	for _, d := range []catalog.Descriptor{&br, &cr, b, c} {
		dest.State().SetDescriptor(d.Address(), d)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{relation.Address(): br.Address(), br.Address(): cr.Address(), typ.Address(): b.Address(), b.Address(): c.Address()}
	typeMap := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == typ.SQLType {
			return b.SQLType
		}
		if old == b.SQLType {
			return c.SQLType
		}
		mapped, _ := dest.LookupType(old.ID())
		return mapped
	}
	return dest, &br, b, c, mapping, typeMap
}

func TestRoutineDefaultAttributeRebind(t *testing.T) {
	read, relation, typ, node := defaultAttributeFixture(t)
	p := defaultAttributePayload(t, read, node)
	before := slices.Clone(p.dependencies)
	dest, br, b, _, mapping, typeMap := defaultAttributeDestination(t, read, relation, typ)
	mapped, err := p.Rebind(dest, mapping, NewRoutineRemap(dest, mapping, typeMap, nil, false))
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*RoutineDefaults)
	defaultAttributeCheck(t, dest, next, br, b, 7)
	field, _ := next.Default(0)
	if field.(plan.FieldExpr).Base.ResultUsage().Type != b.SQLType {
		t.Fatal("type mapped twice or left foreign")
	}
	if err := next.Validate(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := next.DefaultSQL(context.Background(), dest, 0); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, p.dependencies) || node.Base.ResultUsage().Type != typ.SQLType {
		t.Fatal("source payload changed")
	}
}

func TestRoutineDefaultAttributeDefaultOnlyRemap(t *testing.T) {
	read, relation, typ, node := defaultAttributeFixture(t)
	p := defaultAttributePayload(t, read, node)
	definition, err := proc.New("SELECT $1", parser.Position{}, []sqltype.Usage{node.ResultUsage()}, node.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = "default_only"
	if err := definition.SetFormals([]proc.Formal{{Mode: "i", Name: "x", Usage: node.ResultUsage(), HasDefault: true}}); err != nil {
		t.Fatal(err)
	}
	original := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 96000}, ObjectNamespace: relation.Namespace(), ObjectName: "default_only", ObjectRevision: 3, RoutineKind: catalog.FunctionRoutine, Signature: "23", Result: 23, Owner: read.Owner().Principals.Principal(10), SQL: definition, SQLDefaults: p}
	dest, br, b, _, mapping, typeMap := defaultAttributeDestination(t, read, relation, typ)
	dest.State().SetDescriptor(original.Address(), original)
	mapPrincipal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if ref != read.Owner().Principals.Principal(ref.ID()) || ref.ID() == 0 {
			return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
		}
		return dest.Owner().Principals.Principal(ref.ID()), nil
	}
	principal := catalog.NewProcedureRemapper(catalog.PrincipalRemap{
		Principal: mapPrincipal,
		SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
			return mapPrincipal(ref)
		},
	})
	remap := NewRoutineRemap(dest, mapping, typeMap, principal, false)
	if err := routine.RebindSQLDefinitions(dest.State(), remap); err != nil {
		t.Fatal(err)
	}
	if err := remap.Finish(); err != nil {
		t.Fatal(err)
	}
	mapped, ok := dest.Procedure(original.Address())
	if !ok || mapped == original || mapped.SQLBody != nil || mapped.SQLDefaults == p || mapped.SQL == definition {
		t.Fatal("default-only payload/declaration not remapped", mapped)
	}
	defaultAttributeCheck(t, dest, mapped.SQLDefaults.(*RoutineDefaults), br, b, 7)
	field, _ := mapped.SQLDefaults.Default(0)
	if field.(plan.FieldExpr).Base.ResultUsage().Type != b.SQLType {
		t.Fatal("default-only type ownership not mapped once")
	}
	if original.SQL != definition || original.SQLDefaults != p || original.SQLBody != nil {
		t.Fatal("default-only source mutated")
	}
}
