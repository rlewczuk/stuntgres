package engine

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRegOperatorInventory(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	file, err := os.Open("../../docs/design/regoperator-pg18-inventory.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	expected := make(map[OID]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 5 || fields[0] == "oid" || strings.HasPrefix(fields[0], "#") {
			continue
		}
		oid, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		left, err := strconv.ParseUint(fields[3], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		right, err := strconv.ParseUint(fields[4], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		address := ObjectAddress{ClassID: OperatorClassID, ObjectID: OID(oid)}
		d, ok := view.state.descriptors[address].(*operatorDescriptor)
		if !ok || d.name != fields[2] || d.namespace.ObjectID != 11 || d.left != OID(left) || d.right != OID(right) || expected[OID(oid)] {
			t.Fatalf("operator identity %v differs from independent inventory", fields)
		}
		if err := (operatorProvider{}).Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if !pgDependPinned(view, address) {
			t.Fatal("builtin operator not pinned", address)
		}
		expected[OID(oid)] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	actual := 0
	for a := range view.state.descriptors {
		if a.ClassID == OperatorClassID {
			actual++
			if !expected[a.ObjectID] {
				t.Fatal("extra operator", a)
			}
		}
	}
	if actual != 799 || len(expected) != 799 {
		t.Fatal("inventory cardinality", actual, len(expected))
	}
}

func TestRegOperatorMaskingQuotedOutputAndRetention(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	stage := e.BeginCatalogStage(view)
	namespaces := make([]ObjectAddress, 4)
	for i, name := range []string{"Reg Op", "earlier", "later", `Reg" Op`} {
		ns := *view.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}].(*namespaceDescriptor)
		ns.address = ObjectAddress{ClassID: NamespaceClassID, ObjectID: OID(90000 + i)}
		ns.name = name
		namespaces[i] = ns.address
		if err := stage.Create(&ns); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct {
		ns          int
		left, right OID
	}{{0, 23, 23}, {1, 23, 23}, {2, 23, 23}, {2, 20, 20}, {3, 23, 23}} {
		d := &operatorDescriptor{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: OID(91000 + i)}, namespace: namespaces[row.ns], revision: 1, name: "#=#", left: row.left, right: row.right}
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	groups := collectOperatorGroups(view)
	path := []ObjectAddress{{ClassID: NamespaceClassID, ObjectID: 11}}
	for alias, want := range map[sqltype.ID]string{sqltype.Regoper: `"Reg Op".#=#`, sqltype.Regoperator: `"Reg Op".#=#(integer,integer)`} {
		if got := operatorSpelling(view, path, groups, 91000, alias); got != want {
			t.Fatal(got, want)
		}
	}
	if got := operatorSpelling(view, path, groups, 91004, sqltype.Regoperator); got != `"Reg"" Op".#=#(integer,integer)` {
		t.Fatal(got)
	}
	candidates := visibleOperators(groups, namespaces[1:], "#=#")
	if len(candidates) != 2 {
		t.Fatal("different signatures must remain candidates", len(candidates))
	}
	if got := operatorSpelling(view, namespaces[1:], groups, 91002, sqltype.Regoperator); got != "later.#=#(integer,integer)" {
		t.Fatal(got)
	}
	if got := operatorSpelling(view, namespaces[1:], groups, 91001, sqltype.Regoper); got != "earlier.#=#" {
		t.Fatal(got)
	}
	if got := operatorSpelling(view, namespaces[:1], groups, 91000, sqltype.Regoper); got != "#=#" {
		t.Fatal(got)
	}
	rows := &finalRows{}
	for _, alias := range []sqltype.ID{sqltype.Regoper, sqltype.Regoperator} {
		typ, _ := view.LookupType(alias)
		value, err := sqltype.OperatorValue(typ, 91000)
		if err != nil {
			t.Fatal(err)
		}
		if err := captureOperatorOutputValue(rows, view, path, value); err != nil {
			t.Fatal(err)
		}
	}
	// Replacement catalog and path cannot retarget finalized row presentation.
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if text, err := rows.FormatRegoper(91000); err != nil || text != `"Reg Op".#=#` {
		t.Fatal(text, err)
	}
	if text, err := rows.FormatRegoperator(91000); err != nil || text != `"Reg Op".#=#(integer,integer)` {
		t.Fatal(text, err)
	}
}

func TestRegOperatorPersistenceReferences(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	ctx := context.Background()
	for _, alias := range []sqltype.ID{sqltype.Regoper, sqltype.Regoperator} {
		typ, _ := view.LookupType(alias)
		for _, id := range []uint32{551, 0, 4294967295} {
			value, err := sqltype.OperatorValue(typ, id)
			if err != nil {
				t.Fatal(err)
			}
			var refs []ObjectAddress
			if err := walkOperatorReferences(view, value, func(a ObjectAddress) error { refs = append(refs, a); return nil }); err != nil {
				t.Fatal(err)
			}
			if id == 551 {
				if len(refs) != 1 || refs[0] != (ObjectAddress{ClassID: 2617, ObjectID: 551}) {
					t.Fatal(refs)
				}
			} else if len(refs) != 0 {
				t.Fatal(refs)
			}
			literal := literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: value}
			text, err := catalogLiteralSQL(ctx, view, literal)
			want := "CAST('" + strconv.FormatUint(uint64(id), 10) + "' AS " + typ.SQLName() + ")"
			if err != nil || text != want {
				t.Fatal(text, want, err)
			}
		}
	}
}

func TestRegOperatorDomainCompanions(t *testing.T) {
	for _, alias := range []string{"regoper", "regoperator"} {
		t.Run(alias, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.operator_direct AS "+alias))
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.operator_chain AS public.operator_direct"))
			view := e.CatalogRead()
			direct := domainUnitType(t, view, "operator_direct")
			chain := domainUnitType(t, view, "operator_chain")
			next := cloneCatalog(view.state)
			mapping := make(map[ObjectAddress]ObjectAddress)
			for i, domain := range []*typeDescriptor{direct, chain} {
				if !domain.typ.Concrete() || !domain.typ.Usage().Valid() || !sqltype.IsOperatorAlias(domain.typ.BaseType().ID()) {
					t.Fatal("ordinary scalar domain invalidated", domain)
				}
				array, ok := view.LookupArray(domain.typ)
				if !ok || array.Concrete() || array.Usage().Valid() || array.Element() != domain.typ || array.ID() != domain.array {
					t.Fatal("lookup-only domain companion", domain, array, ok)
				}
				if _, err := sqltype.Array(61000, "_forged", domain.typ); err == nil {
					t.Fatal("recursive operator array admitted")
				}
				mapping[domain.Address()] = ObjectAddress{ClassID: TypeClassID, ObjectID: OID(60001 + i)}
				mapping[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(domain.array)}] = ObjectAddress{ClassID: TypeClassID, ObjectID: OID(60003 + i)}
			}
			if changed, err := reownDomainTypes(next, mapping); err != nil || !changed {
				t.Fatal("domain reownership", changed, err)
			}
			for i, original := range []*typeDescriptor{direct, chain} {
				domain := next.descriptors[original.Address()].(*typeDescriptor)
				array := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(original.array)}].(*typeDescriptor)
				if !domain.typ.Concrete() || domain.typ.ID() != sqltype.ID(60001+i) || array.typ.Concrete() || array.typ.Element() != domain.typ || array.typ.ID() != sqltype.ID(60003+i) || domain.array != array.typ.ID() {
					t.Fatal("reownership changed companion admission", domain, array)
				}
			}
			if chain.typ.DomainBase().Type != direct.typ || !chain.typ.Concrete() {
				t.Fatal("source domain authority changed")
			}
		})
	}
}
