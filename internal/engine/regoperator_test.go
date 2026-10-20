package engine

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
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
	expected := make(map[ids.OID]bool)
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
		address := ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(oid)}
		d, ok := view.Operator(address)
		if !ok || d.Name() != fields[2] || d.Namespace().ObjectID != 11 || d.Left != ids.OID(left) || d.Right != ids.OID(right) || expected[ids.OID(oid)] {
			t.Fatalf("operator identity %v differs from independent inventory", fields)
		}
		if err := (routine.OperatorProvider{}).Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if !pgDependPinned(view, nil, address) {
			t.Fatal("builtin operator not pinned", address)
		}
		expected[ids.OID(oid)] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	actual := 0
	for a := range view.Descriptors() {
		if a.ClassID == ids.OperatorClassID {
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
	namespaces := make([]ids.ObjectAddress, 4)
	for i, name := range []string{"Reg Op", "earlier", "later", `Reg" Op`} {
		ns := *view.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
		ns.ObjectAddress = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(90000 + i)}
		ns.ObjectName = name
		namespaces[i] = ns.Address()
		if err := stage.Create(&ns); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct {
		ns          int
		left, right ids.OID
	}{{0, 23, 23}, {1, 23, 23}, {2, 23, 23}, {2, 20, 20}, {3, 23, 23}} {
		d := &catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(91000 + i)}, ObjectNamespace: namespaces[row.ns], ObjectRevision: 1, ObjectName: "#=#", Left: row.left, Right: row.right, Owner: e.roles.domain.Principal(10)}
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	collationUnitPublish(t, e, stage)
	view = e.CatalogRead()
	groups := collectOperatorGroups(view)
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: 11}}
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
	for _, alias := range []sqltype.ID{sqltype.Regoper, sqltype.Regoperator} {
		typ, _ := view.LookupType(alias)
		for _, id := range []uint32{551, 0, 4294967295} {
			value, err := sqltype.OperatorValue(typ, id)
			if err != nil {
				t.Fatal(err)
			}
			var refs []ids.ObjectAddress
			if err := walkOperatorReferences(view, value, func(a ids.ObjectAddress) error { refs = append(refs, a); return nil }); err != nil {
				t.Fatal(err)
			}
			if id == 551 {
				if len(refs) != 1 || refs[0] != (ids.ObjectAddress{ClassID: 2617, ObjectID: 551}) {
					t.Fatal(refs)
				}
			} else if len(refs) != 0 {
				t.Fatal(refs)
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
			next := catalog.CloneState(view.State())
			mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
			for i, domain := range []*catalog.Type{direct, chain} {
				if !domain.SQLType.Concrete() || !domain.SQLType.Usage().Valid() || !sqltype.IsOperatorAlias(domain.SQLType.BaseType().ID()) {
					t.Fatal("ordinary scalar domain invalidated", domain)
				}
				array, ok := view.LookupArray(domain.SQLType)
				if !ok || array.Concrete() || array.Usage().Valid() || array.Element() != domain.SQLType || array.ID() != domain.Array {
					t.Fatal("lookup-only domain companion", domain, array, ok)
				}
				if _, err := sqltype.Array(61000, "_forged", domain.SQLType); err == nil {
					t.Fatal("recursive operator array admitted")
				}
				mapping[domain.Address()] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(60001 + i)}
				mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(60003 + i)}
			}
			if changed, err := reownDomainTypes(next, mapping, false); err != nil || !changed {
				t.Fatal("domain reownership", changed, err)
			}
			for i, original := range []*catalog.Type{direct, chain} {
				domain := next.MustType(original.Address())
				array := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.Array)})
				if !domain.SQLType.Concrete() || domain.SQLType.ID() != sqltype.ID(60001+i) || array.SQLType.Concrete() || array.SQLType.Element() != domain.SQLType || array.SQLType.ID() != sqltype.ID(60003+i) || domain.Array != array.SQLType.ID() {
					t.Fatal("reownership changed companion admission", domain, array)
				}
			}
			if chain.SQLType.DomainBase().Type != direct.SQLType || !chain.SQLType.Concrete() {
				t.Fatal("source domain authority changed")
			}
		})
	}
}
