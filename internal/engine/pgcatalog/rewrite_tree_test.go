package pgcatalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteMetadata(t *testing.T) {
	var spec *Spec
	for i := range Specs {
		if Specs[i].OID == 2618 {
			spec = &Specs[i]
			break
		}
	}
	if spec == nil || len(spec.Columns) != 8 {
		t.Fatal(spec)
	}
	names := []string{"oid", "rulename", "ev_class", "ev_type", "ev_enabled", "is_instead", "ev_qual", "ev_action"}
	types := []sqltype.ID{26, 19, 26, 18, 18, 16, 194, 194}
	for i, c := range spec.Columns {
		coll := uint32(0)
		if i == 1 || i >= 6 {
			coll = 950
		}
		if c.Name != names[i] || c.Type != types[i] || c.Nullable || c.Collation != coll || c.Typmod != -1 || c.Dimensions != 0 {
			t.Fatal(i, c)
		}
	}
}

func TestPGRewriteBuiltinDeclarations(t *testing.T) {
	state := catalog.NewState(1)
	owner := &catalog.Owner{}
	read := catalog.NewRead(owner, state)
	typ, ok := builtinType(sqltype.PgNodeTree)
	if !ok {
		t.Fatal("PG_NODE_TREE")
	}
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 194}, &catalog.Type{SQLType: typ})
	seen := 0
	var previous string
	for _, record := range builtinViews {
		if record.returnRule == 0 {
			if record.declaration != nil {
				t.Fatal("fabricated declaration")
			}
			continue
		}
		seen++
		if record.declarationErr != nil || record.declaration == nil {
			t.Fatal(record.oid, record.declarationErr)
		}
		parsed, err := parser.ParseRoutineDeclarationBody(record.canonical)
		if err != nil {
			t.Fatal(record.oid, err)
		}
		if _, ok := parsed[0].(*parser.Select); !ok {
			t.Fatal("not SELECT")
		}
		if !reflect.DeepEqual(parsed[0], record.declaration.query) {
			t.Fatal("declaration is not canonical parsed owner")
		}
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: record.oid}, RelationKind: catalog.CatalogRelation, CatalogView: true, CatalogReturnRule: record.returnRule}
		state.SetDescriptor(relation.Address(), relation)
		fact, err := builtinRewriteFact(t.Context(), read, relation, typ)
		if err != nil {
			t.Fatal(err)
		}
		if fact.OID != record.returnRule || fact.Name != "_RETURN" || fact.Event != "1" || !fact.Instead || fact.Mode.String() != "O" || fact.Qual.IsNull() || fact.Action.IsNull() {
			t.Fatal(fact)
		}
		action, _ := fact.Action.Tree()
		if action != record.declaration {
			t.Fatal("copied/detached declaration")
		}
		qual, _ := fact.Qual.Type().Output(t.Context(), fact.Qual)
		if qual != "<>" {
			t.Fatal(qual)
		}
		text, err := fact.Action.Type().Output(t.Context(), fact.Action)
		if err != nil || text == previous || !strings.HasPrefix(text, "{BuiltinRewriteDeclaration {parser.Select") || !strings.Contains(text, " references [") {
			t.Fatal(text, err)
		}
		previous = text
		copy := *relation
		if _, err := builtinRewriteFact(t.Context(), read, &copy, typ); err == nil {
			t.Fatal("copied relation admitted")
		}
		copy.CatalogReturnRule++
		state.SetDescriptor(copy.Address(), &copy)
		if _, err := builtinRewriteFact(t.Context(), read, &copy, typ); err == nil {
			t.Fatal("wrong return identity admitted")
		}
		state.SetDescriptor(relation.Address(), relation)
		if relation.View != nil {
			t.Fatal("declaration gained stored-view authority")
		}
	}
	if seen != 2 {
		t.Fatal("update explicit inventory expectations", seen)
	}
}

type rewriteCancelRuntime struct {
	Runtime
	cancel context.CancelFunc
}

func (r rewriteCancelRuntime) VisitRewriteCatalog(ctx context.Context, _ catalog.Read, _ *catalog.Relation, _ *sqltype.Descriptor, _ func(RewriteCatalogFact) error) error {
	r.cancel()
	return ctx.Err()
}
func TestPGRewriteCaptureCancellation(t *testing.T) {
	state := catalog.NewState(1)
	read := catalog.NewRead(&catalog.Owner{}, state)
	for _, typ := range sqltype.Builtins() {
		state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}, &catalog.Type{SQLType: typ})
	}
	var source catalog.Relation
	for _, spec := range Specs {
		if spec.OID == 2618 {
			for _, column := range spec.Columns {
				typ, ok := read.LookupType(column.Type)
				if !ok {
					t.Fatal(column.Type)
				}
				source.Columns = append(source.Columns, catalog.Column{Name: column.Name, Usage: typ.Usage()})
			}
		}
	}
	for _, record := range builtinViews {
		if record.returnRule != 0 {
			relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: record.oid}, RelationKind: catalog.CatalogRelation, CatalogView: true, CatalogReturnRule: record.returnRule}
			state.SetDescriptor(relation.Address(), relation)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rows, err := capturePGRewrite(Input{Context: ctx, Catalog: read, Source: &source, Runtime: rewriteCancelRuntime{cancel: cancel}})
	if !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled capture returned a partial relation", rows, err)
	}
}
