package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/hbarules"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func hbaInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.HBAFileRulesOID}
	return pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(address), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{}), HBARules: e.HBARules.Capture()}
}
func TestPGHBAFileRulesCapture(t *testing.T) {
	e := engine.New()
	e.HBARules = new(hbarules.Source)
	e.HBARules.Set([]hbarules.Rule{
		{RuleNumber: new(int32(1)), FileName: new("label"), LineNumber: new(int32(2)), Type: new("local"), Database: []string{"db1", "db2"}, UserName: []string{"all"}, AuthMethod: new("peer"), Options: []string{"map=secret"}},
		{FileName: new("warning label"), Type: new("hostssl"), Error: new("warning")},
		{RuleNumber: new(int32(2))},
		{FileName: new("error label"), LineNumber: new(int32(7)), Error: new("diagnostic")},
	})
	in := hbaInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.HBAFileRulesOID)
	if !ok {
		t.Fatal("producer missing")
	}
	rows, err := produce(in)
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	if n, err := rows[0][0].Integer(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if !rows[1][0].IsNull() || rows[1][3].IsNull() || rows[1][10].IsNull() {
		t.Fatal("warning fields lost", rows[1])
	}
	if n, err := rows[2][0].Integer(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	for _, j := range []int{3, 4, 5, 6, 7, 8, 9, 10} {
		if !rows[2][j].IsNull() {
			t.Fatal("artifact", j, rows[2])
		}
	}
	for _, j := range []int{0, 3, 4, 5, 6, 7, 8, 9} {
		if !rows[3][j].IsNull() {
			t.Fatal("fatal", j, rows[3])
		}
	}
	for _, j := range []int{4, 5, 9} {
		value := rows[0][j]
		typ := in.Source.Columns[j].Usage.Type
		if value.Type() != typ || value.ArrayRank() != 1 || value.ArrayDimension(0).Lower != 1 || value.ArrayDimension(0).Length != int32(value.ArrayLen()) {
			t.Fatal("array ownership/rank", j)
		}
		for k := range value.ArrayLen() {
			element := value.ArrayElement(k)
			if element.IsNull() || element.Type() != typ.Element() {
				t.Fatal("foreign/NULL element", j, k)
			}
		}
	}
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil || captured.Ref != in.Ref || !reflect.DeepEqual(captured.Anonymous, rows) {
		t.Fatal(captured, err)
	}
	old := rows[0][4]
	e.HBARules.Set([]hbarules.Rule{{Database: []string{"replacement"}}})
	fresh := hbaInput(e)
	again, err := produce(fresh)
	if err != nil || len(again) != 1 || reflect.DeepEqual(old, again[0][4]) || !reflect.DeepEqual(old, rows[0][4]) {
		t.Fatal("retained arrays changed", again, err)
	}
	rows[0][4] = sqltype.Null(in.Source.Columns[4].Usage.Type)
	if captured.Anonymous[0][4].IsNull() {
		t.Fatal("captures alias flat cells")
	}
	reject := func(input pgcatalog.Input) {
		t.Helper()
		if got, err := produce(input); got != nil || err == nil {
			t.Fatal("forged input admitted", got, err)
		}
		if got, err := catalogrt.CaptureRows(input, produce); got != nil || err == nil {
			t.Fatal("forged input published", got, err)
		}
	}
	foreign := engine.New()
	for _, change := range []func(*pgcatalog.Input){func(i *pgcatalog.Input) { i.Source = nil }, func(i *pgcatalog.Input) { i.Owner = nil }, func(i *pgcatalog.Input) { i.Owner = foreign.CatalogRead().Owner() }, func(i *pgcatalog.Input) { i.Catalog = foreign.CatalogRead() }, func(i *pgcatalog.Input) { i.Runtime = nil }, func(i *pgcatalog.Input) { i.Ref = ids.ObjectRef{} }, func(i *pgcatalog.Input) { i.Ref.Epoch++ }, func(i *pgcatalog.Input) { r := *i.Source; i.Source = &r }} {
		bad := in
		change(&bad)
		reject(bad)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:10] }, func(r *catalog.Relation) { r.Columns[0].Name = "forged" }, func(r *catalog.Relation) { r.Columns[4].Usage.Type, _ = foreign.CatalogRead().LookupType(1009) }, func(r *catalog.Relation) { r.Columns[4].Usage.Typmod = 0 }, func(r *catalog.Relation) { r.Columns[4].Usage.Collation.ID = 950 }, func(r *catalog.Relation) { r.Columns[4].DeclaredDimensions = 1 }, func(r *catalog.Relation) { r.Columns[4].Nullable = false }, func(r *catalog.Relation) { r.Columns[4].NullabilityKnown = false },
	} {
		state := *in.Catalog.State()
		r := *in.Source
		r.Columns = append([]catalog.Column(nil), r.Columns...)
		change(&r)
		state.SetDescriptor(r.Address(), &r)
		bad := in
		bad.Catalog, bad.Source = e.Catalog.Read(&state), &r
		reject(bad)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bad := in
	bad.Context = ctx
	bad.Source = nil
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	in.HBARules = nil
	if rows, err := produce(in); len(rows) != 0 || err != nil {
		t.Fatal(rows, err)
	}
}

func TestPGHBAFileRulesNullableFacts(t *testing.T) {
	e := engine.New()
	e.HBARules = new(hbarules.Source)
	e.HBARules.Set([]hbarules.Rule{
		{},
		{RuleNumber: new(int32(0)), FileName: new(""), LineNumber: new(int32(0)), Type: new(""), Database: []string{}, UserName: []string{""}, Address: new(""), Netmask: new(""), AuthMethod: new(""), Options: []string{}, Error: new("")},
		{RuleNumber: new(int32(-8)), FileName: new("label"), LineNumber: new(int32(-4)), Type: new("kind"), Database: []string{"a", "b"}, UserName: []string{"u"}, Address: new("addr"), Netmask: new("mask"), AuthMethod: new("method"), Options: []string{"secret"}, Error: new("warning")},
	})
	in := hbaInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.HBAFileRulesOID)
	if !ok {
		t.Fatal("producer missing")
	}
	rows, err := produce(in)
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	for i, row := range rows {
		if len(row) != 11 {
			t.Fatal("wrong width", i, len(row))
		}
		for j, cell := range row {
			if cell.Type() != in.Source.Columns[j].Usage.Type || cell.IsNull() != (i == 0) {
				t.Fatal("NULL or current descriptor lost", i, j, cell)
			}
		}
	}
	for _, scalar := range []struct {
		index int
		want  int64
	}{{0, -8}, {2, -4}} {
		zero, err := rows[1][scalar.index].Integer()
		if err != nil || zero != 0 {
			t.Fatal("zero became NULL", scalar.index, zero, err)
		}
		value, err := rows[2][scalar.index].Integer()
		if err != nil || value != scalar.want {
			t.Fatal("number normalized", scalar.index, value, err)
		}
	}
	for _, scalar := range []struct {
		index int
		want  string
	}{{1, "label"}, {3, "kind"}, {6, "addr"}, {7, "mask"}, {8, "method"}, {10, "warning"}} {
		empty, err := rows[1][scalar.index].Text()
		if err != nil || empty != "" {
			t.Fatal("empty text became NULL", scalar.index, empty, err)
		}
		value, err := rows[2][scalar.index].Text()
		if err != nil || value != scalar.want {
			t.Fatal("scalar changed", scalar.index, value, err)
		}
	}
	for _, array := range []struct {
		index int
		want  []string
	}{{4, []string{"a", "b"}}, {5, []string{"u"}}, {9, []string{"secret"}}} {
		value := rows[2][array.index]
		if value.ArrayRank() != 1 || value.ArrayDimension(0).Lower != 1 || value.ArrayDimension(0).Length != int32(len(array.want)) || value.ArrayLen() != len(array.want) {
			t.Fatal("array dimensions", array.index, value)
		}
		for k, want := range array.want {
			element := value.ArrayElement(k)
			got, err := element.Text()
			if err != nil || got != want || element.Type() != value.Type().Element() {
				t.Fatal("array element", array.index, k, got, err)
			}
		}
	}
	for _, index := range []int{4, 9} {
		if rows[1][index].ArrayRank() != 0 || rows[1][index].ArrayLen() != 0 {
			t.Fatal("present empty array not rank zero", index, rows[1][index])
		}
	}
	oneEmpty := rows[1][5]
	if oneEmpty.ArrayRank() != 1 || oneEmpty.ArrayLen() != 1 {
		t.Fatal("one empty element collapsed", oneEmpty)
	}
	if text, err := oneEmpty.ArrayElement(0).Text(); err != nil || text != "" {
		t.Fatal(text, err)
	}
	// Each invocation allocates its typed cells, not a cached producer result.
	again, err := produce(in)
	if err != nil || &again[0][0] == &rows[0][0] || !reflect.DeepEqual(again, rows) {
		t.Fatal("invocations borrowed cells", again, err)
	}
	// Cancellation at admission, during enumeration, and finalization must
	// reject publication rather than returning a materialized prefix.
	for remaining := 1; remaining <= len(rows)+2; remaining++ {
		cancelled := in
		cancelled.Context = &dtpCancelContext{Context: t.Context(), remaining: remaining}
		if got, err := produce(cancelled); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled producer published rows", remaining, got, err)
		}
		cancelled.Context = &dtpCancelContext{Context: t.Context(), remaining: remaining}
		if got, err := catalogrt.CaptureRows(cancelled, produce); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled capture published rows", remaining, got, err)
		}
	}
	// Facts carry no type authority: after catalog replacement they are
	// materialized with the new generation, while old typed cells stay owned.
	oldType := rows[2][4].Type()
	oldSnapshot := in.HBARules
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	current := hbaInput(e)
	if current.HBARules != oldSnapshot {
		t.Fatal("Reset cloned or lost facts")
	}
	fresh, err := produce(current)
	if err != nil || len(fresh) != 3 || fresh[2][4].Type() == oldType || fresh[2][4].Type() != current.Source.Columns[4].Usage.Type || rows[2][4].Type() != oldType {
		t.Fatal("catalog generation ownership", fresh, err)
	}
	e.HBARules.Set(nil)
	if got, err := produce(hbaInput(e)); err != nil || len(got) != 0 {
		t.Fatal("clear", got, err)
	}
	if got, err := produce(in); err != nil || !reflect.DeepEqual(got, rows) {
		t.Fatal("old snapshot/generation changed", got, err)
	}
}
func TestPGHBAFileRulesOwnership(t *testing.T) {
	e := engine.New()
	in := hbaInput(e)
	r := in.Source
	if r.RowType != 12116 || !r.VirtualView || !r.CatalogView || len(r.Columns) != 11 || r.Owner.ID() != authorization.BootstrapPrincipalID {
		t.Fatal(r)
	}
	names := []string{"rule_number", "file_name", "line_number", "type", "database", "user_name", "address", "netmask", "auth_method", "options", "error"}
	types := []sqltype.ID{23, 25, 23, 25, 1009, 1009, 25, 25, 25, 1009, 25}
	proc, ok := in.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3401})
	if !ok || proc.Builtin != routine.BuiltinRoutine(3401) || proc.Builtin.Providers != 0 || proc.Builtin.Parallel != 's' || !proc.Strict || !proc.ReturnsSet() || proc.Result != 2249 || proc.Owner != r.Owner {
		t.Fatal(proc)
	}
	if err := (stored.ProcedureProvider{}).Validate(in.Catalog, proc); err != nil {
		t.Fatal(err)
	}
	outputs := proc.OutputDeclaration()
	if outputs == nil || outputs.Len() != 11 || outputs.Inputs() != 0 {
		t.Fatal(outputs)
	}
	for i, c := range r.Columns {
		owned, ok := in.Catalog.LookupType(types[i])
		collation := uint32(100)
		if types[i] == 23 {
			collation = 0
		}
		if !ok || c.Usage.Type != owned || c.Name != names[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12114 || c.OriginAttribute != int16(i+1) || outputs.Name(i) != names[i] || outputs.Type(i) != ids.OID(types[i]) {
			t.Fatal(i, c)
		}
	}
	for _, item := range []struct {
		address   ids.ObjectAddress
		acl       authorization.ACLValue
		privilege authorization.Privileges
	}{{r.Address(), r.Security.ACL, authorization.TableGrantPrivileges}, {proc.Address(), proc.ACL, authorization.ExecutePrivilege}} {
		if !item.acl.Explicit || len(item.acl.Entries) != 1 || item.acl.Entries[0].Grantee.Role != r.Owner || item.acl.Entries[0].Privileges != item.privilege {
			t.Fatal(item)
		}
		initial := in.Catalog.State().InitialPrivilege(item.address)
		if initial == nil || !reflect.DeepEqual(initial.ACL, item.acl) {
			t.Fatal(initial, item.acl)
		}
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12116})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12115})
	if row.Array != 12115 || row.OwningRelation != r.Address() || array.OwningRelation != r.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal(row, array)
	}
}
