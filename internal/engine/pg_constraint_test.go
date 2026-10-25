package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgConstraintUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2606}
	source, ok := e.Catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing pg_constraint")
	}
	return source, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}
func pgConstraintUnitCapture(t *testing.T, e *Engine, view catalog.Read) map[string][]sqltype.Value {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2606}
	source := view.MustRelation(address)
	ref := e.Catalog.Domain.Ref(view.Epoch(), address)
	got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGConstraint)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("missing constraint capture")
	}
	out := make(map[string][]sqltype.Value, len(got.Anonymous))
	for _, row := range got.Anonymous {
		name, err := row[1].Text()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := out[name]; exists {
			t.Fatal("duplicate fixture constraint", name)
		}
		out[name] = row
	}
	pgConstraintUnitBootstrap(t, view, out["yes_or_no_check"])
	return out
}

func pgConstraintUnitBootstrap(t *testing.T, view catalog.Read, row []sqltype.Value) {
	t.Helper()
	domain := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13256})
	if len(row) != 28 || len(domain.DomainChecks) != 1 {
		t.Fatal("missing real bootstrap domain CHECK", row)
	}
	id, err := row[0].OID()
	if err != nil || id == 0 || ids.OID(id) != domain.DomainChecks[0].OID {
		t.Fatal("bootstrap CHECK identity detached from recipe", id, err)
	}
	for i, value := range row {
		if value.Type().ID() != pgcatalog.ConstraintTypeIDs[i] || i < 20 && value.IsNull() {
			t.Fatal("bootstrap typed scalar", i, value)
		}
	}
	for i, want := range map[int]string{
		1: "yes_or_no_check", 2: "13229", 3: "c", 4: "f", 5: "f",
		6: "t", 7: "t", 8: "0", 9: "13256", 10: "0", 11: "0", 12: "0",
		13: " ", 14: " ", 15: " ", 16: "t", 17: "0", 18: "f", 19: "f",
	} {
		if got := pgConstraintUnitText(t, row[i]); got != want {
			t.Fatal("bootstrap constraint field", i, got, want)
		}
	}
	for i := 20; i < 27; i++ {
		if !row[i].IsNull() {
			t.Fatal("bootstrap domain CHECK has relation keys", i, row[i])
		}
	}
	if row[27].IsNull() {
		t.Fatal("bootstrap CHECK lost actual expression tree")
	}
}

// Exercise the real private CREATE binder and publication boundary, not SQL
// execution. Public SQL routes are covered independently in reference/catalogs.
func pgConstraintUnitTableStage(t *testing.T, e *Engine, op *session.Operation, q string) *CatalogStage {
	t.Helper()
	syntax := statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = SchemaTableLocked(e, op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}
func pgConstraintUnitFixture(t *testing.T) *Engine {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TABLE pc_unit_parent(id INT CONSTRAINT pc_unit_pk PRIMARY KEY)",
		"CREATE TABLE pc_unit(a BIGINT,b INT CONSTRAINT pc_unit_nn NOT NULL,dead TEXT,d INT,CONSTRAINT pc_unit_ck CHECK(b>d AND b<>0),CONSTRAINT pc_unit_const CHECK(TRUE),CONSTRAINT pc_unit_private CHECK(d>0) NO INHERIT,CONSTRAINT pc_unit_u UNIQUE(d,b),CONSTRAINT pc_unit_fk FOREIGN KEY(a) REFERENCES pc_unit_parent(id),CONSTRAINT pc_unit_x EXCLUDE USING btree((b+1) WITH =) DEFERRABLE INITIALLY DEFERRED)",
	} {
		domainUnitPublish(t, e, pgConstraintUnitTableStage(t, e, op, q))
	}
	return e
}

func TestPGConstraintShapeAndOwnership(t *testing.T) {
	e := New()
	source, ref := pgConstraintUnitSource(t, e)
	view := e.CatalogRead()
	names := []string{"oid", "conname", "connamespace", "contype", "condeferrable", "condeferred", "conenforced", "convalidated", "conrelid", "contypid", "conindid", "conparentid", "confrelid", "confupdtype", "confdeltype", "confmatchtype", "conislocal", "coninhcount", "connoinherit", "conperiod", "conkey", "confkey", "conpfeqop", "conppeqop", "conffeqop", "confdelsetcols", "conexclop", "conbin"}
	typeIDs := []sqltype.ID{26, 19, 26, 18, 16, 16, 16, 16, 26, 26, 26, 26, 26, 18, 18, 18, 16, 21, 16, 16, 1005, 1005, 1028, 1028, 1028, 1005, 1028, 194}
	if source.RowType != 10003 || source.ColumnMeta.HighWater != 28 || len(source.Columns) != 28 || source.Namespace().ObjectID != 11 {
		t.Fatal("source shape", source)
	}
	for i, column := range source.Columns {
		owned, ok := view.LookupType(typeIDs[i])
		if !ok || column.Name != names[i] || column.Usage.Type != owned || column.Usage.Typmod != -1 || column.Nullable != (i >= 20) || !column.NullabilityKnown || column.OriginTable != 2606 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || source.Index[column.Name] != i {
			t.Fatal("descriptor", i, column)
		}
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10003})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10002})
	if row.OwningRelation != source.Address() || array.OwningRelation != source.Address() || row.Array != 10002 {
		t.Fatal("row/array ownership", row, array)
	}
	pgConstraintUnitCapture(t, e, view)
	invalid := *source
	invalid.Columns = slices.Clone(source.Columns[:9])
	if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: &invalid, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGConstraint); got != nil || err == nil {
		t.Fatal("old shape admitted", got, err)
	}
	foreign := New()
	foreignSource, _ := pgConstraintUnitSource(t, foreign)
	if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Ref: ref, Source: foreignSource, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGConstraint); got != nil || err == nil {
		t.Fatal("foreign generation admitted", got, err)
	}
}

func pgConstraintUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	text, err := value.Type().Output(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	return text
}
func TestPGConstraintProjection(t *testing.T) {
	e := pgConstraintUnitFixture(t)
	rows := pgConstraintUnitCapture(t, e, e.CatalogRead())
	for _, tc := range []struct {
		name, kind, key                string
		noinherit, deferred, validated bool
		index                          bool
	}{
		{"pc_unit_nn", "n", "{2}", false, false, true, false},
		{"pc_unit_ck", "c", "{2,4}", false, false, true, false},
		{"pc_unit_const", "c", "", false, false, true, false},
		{"pc_unit_private", "c", "{4}", true, false, true, false},
		{"pc_unit_u", "u", "{4,2}", true, false, true, true},
		{"pc_unit_pk", "p", "{1}", true, false, true, true},
		{"pc_unit_fk", "f", "{1}", true, false, true, true},
		{"pc_unit_x", "x", "{0}", true, true, true, true},
	} {
		row, ok := rows[tc.name]
		if !ok || len(row) != 28 {
			t.Fatal("missing projection", tc.name, row)
		}
		for i, value := range row {
			if value.Type().ID() != pgcatalog.ConstraintTypeIDs[i] || i < 20 && value.IsNull() {
				t.Fatal("typed scalar", tc.name, i, value)
			}
			if !value.IsNull() && value.Type().IsArray() {
				if value.ArrayRank() != 1 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: int32(value.ArrayLen())}) {
					t.Fatal("array dimensions", value)
				}
				for j := range value.ArrayLen() {
					if value.ArrayElement(j).IsNull() || value.ArrayElement(j).Type() != value.Type().Element() {
						t.Fatal("array generation", j, value)
					}
				}
			}
		}
		if pgConstraintUnitText(t, row[3]) != tc.kind || pgConstraintUnitText(t, row[2]) != "2200" || pgConstraintUnitText(t, row[9]) != "0" || pgConstraintUnitText(t, row[11]) != "0" || pgConstraintUnitText(t, row[17]) != "0" {
			t.Fatal("fixed fields", tc.name, row)
		}
		for i, want := range map[int]bool{4: tc.deferred, 5: tc.deferred, 6: true, 7: tc.validated, 16: true, 18: tc.noinherit, 19: false} {
			got, err := row[i].Boolean()
			if err != nil || got != want {
				t.Fatal("boolean", tc.name, i, got, want, err)
			}
		}
		id, _ := row[0].OID()
		owner, _ := row[8].OID()
		index, _ := row[10].OID()
		if id == 0 || owner == 0 || id == owner || (index != 0) != tc.index || index != 0 && (index == id || index == owner) {
			t.Fatal("identity", tc.name, id, owner, index)
		}
		if tc.key == "" {
			if !row[20].IsNull() {
				t.Fatal("constant CHECK key not NULL")
			}
		} else if got := pgConstraintUnitText(t, row[20]); got != tc.key {
			t.Fatal("key", tc.name, got, tc.key)
		}
		if row[27].IsNull() != (tc.kind != "c") {
			t.Fatal("tree applicability", tc.name)
		}
		if tc.kind != "f" {
			for _, i := range []int{12, 13, 14, 15} {
				want := " "
				if i == 12 {
					want = "0"
				}
				if pgConstraintUnitText(t, row[i]) != want {
					t.Fatal("non-FK scalar", tc.name, i, row[i])
				}
			}
			for _, i := range []int{21, 22, 23, 24, 25} {
				if !row[i].IsNull() {
					t.Fatal("non-FK vector", tc.name, i)
				}
			}
		}
		if tc.kind != "x" && !row[26].IsNull() {
			t.Fatal("non-exclusion operator", tc.name)
		}
	}
	fk := rows["pc_unit_fk"]
	for i, want := range map[int]string{13: "a", 14: "a", 15: "s", 21: "{1}", 22: "{15}", 23: "{96}", 24: "{410}"} {
		if got := pgConstraintUnitText(t, fk[i]); got != want {
			t.Fatal("FK metadata", i, got, want)
		}
	}
	pk := rows["pc_unit_pk"]
	if !reflect.DeepEqual(fk[10], pk[10]) || !reflect.DeepEqual(fk[12], pk[8]) {
		t.Fatal("actual FK provider", fk, pk)
	}
	if got := pgConstraintUnitText(t, rows["pc_unit_x"][26]); got != "{96}" {
		t.Fatal("exclusion operators", got)
	}
	if got := pgConstraintUnitText(t, rows["pc_unit_const"][27]); got != `{type 16 typmod -1 collation 0 derivation 0 node Const value "t"}` {
		t.Fatal("native structural tree", got)
	}
}

func TestPGConstraintCapture(t *testing.T) {
	e := pgConstraintUnitFixture(t)
	source, ref := pgConstraintUnitSource(t, e)
	view := e.CatalogRead()
	before := pgConstraintUnitCapture(t, e, view)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{canceled, &pgEnumCancelDuringCapture{Context: context.Background(), after: view.DescriptorCount() + 2}} {
		if got, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: source, Owner: (view).Owner(), Runtime: EngineCatalogRuntime{}}, capturePGConstraint); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation published partial capture", got, err)
		}
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for name, row := range pgConstraintUnitCapture(t, e, e.CatalogRead()) {
		if owner := pgConstraintUnitText(t, row[8]); owner != "0" {
			t.Fatal("reset retained relation constraint", name, owner)
		}
	}
	if got := pgConstraintUnitCapture(t, e, view); !reflect.DeepEqual(got, before) {
		t.Fatal("reset mutated captured generation", got, before)
	}
	if got := pgConstraintUnitText(t, before["pc_unit_ck"][20]); got != "{2,4}" {
		t.Fatal("retained keys", got)
	}
	if before["pc_unit_ck"][27].IsNull() {
		t.Fatal("retained tree")
	}
	if id, err := pgcatalog.ConstraintOperator(view, "=", sqltype.Int4, sqltype.Int8); err != nil || id != 15 {
		t.Fatal(id, err)
	}
	if id, err := pgcatalog.ConstraintOperator(view, "missing", sqltype.Int4, sqltype.Int4); err == nil || id != 0 {
		t.Fatal("missing operator fabricated", id, err)
	}
}

func TestPGConstraintPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	base := e.CatalogRead()
	baseline := pgConstraintUnitCapture(t, e, base)
	stage := pgConstraintUnitTableStage(t, e, op, "CREATE TABLE pc_private(a INT CONSTRAINT pc_private_u UNIQUE)")
	private := e.StageRead(stage)
	if got := pgConstraintUnitCapture(t, e, base); !reflect.DeepEqual(got, baseline) {
		t.Fatal("private creation leaked", got)
	}
	candidate := pgConstraintUnitCapture(t, e, private)
	if len(candidate) != len(baseline)+1 || candidate["pc_private_u"] == nil {
		t.Fatal("private view omitted creation", candidate)
	}
	for name, row := range baseline {
		if !reflect.DeepEqual(candidate[name], row) {
			t.Fatal("private creation changed baseline constraint", name, candidate[name], row)
		}
	}
	e.DiscardCatalogStage(stage)
	if got := pgConstraintUnitCapture(t, e, e.CatalogRead()); !reflect.DeepEqual(got, baseline) {
		t.Fatal("discard leaked creation", got)
	}
	stage = pgConstraintUnitTableStage(t, e, op, "CREATE TABLE pc_private(a INT CONSTRAINT pc_private_u UNIQUE)")
	candidate = pgConstraintUnitCapture(t, e, e.StageRead(stage))
	domainUnitPublish(t, e, stage)
	if got := pgConstraintUnitCapture(t, e, e.CatalogRead()); !reflect.DeepEqual(got, candidate) {
		t.Fatal("publication changed candidate metadata", got, candidate)
	}
	if got := pgConstraintUnitCapture(t, e, base); !reflect.DeepEqual(got, baseline) {
		t.Fatal("publication mutated retained base", got)
	}
}

func TestPGConstraintOperatorMetadata(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		parent, child sqltype.ID
		want          [3]ids.OID
	}{
		{sqltype.Int4, sqltype.Int4, [3]ids.OID{96, 96, 96}},
		{sqltype.Int4, sqltype.Int8, [3]ids.OID{15, 96, 410}},
		{sqltype.Int8, sqltype.Int4, [3]ids.OID{416, 410, 96}},
		{sqltype.Int2, sqltype.Int4, [3]ids.OID{532, 94, 96}},
		{sqltype.Int4, sqltype.Int2, [3]ids.OID{533, 96, 94}},
		{sqltype.VarChar, sqltype.VarChar, [3]ids.OID{98, 98, 98}},
	} {
		parent, _ := view.LookupType(tc.parent)
		child, _ := view.LookupType(tc.child)
		class, ok := sqltype.DefaultIndexClass(parent)
		if !ok {
			t.Fatal("missing class", tc)
		}
		bound, err := routine.BindIndexForeignKey(view, parent.Usage(), child.Usage(), class)
		if err != nil {
			t.Fatal(err)
		}
		inputs, err := bound.EqualityInputs()
		if err != nil {
			t.Fatal(err)
		}
		for i, pair := range inputs {
			id, err := pgcatalog.ConstraintOperator(view, "=", pair[0], pair[1])
			if err != nil || id != tc.want[i] {
				t.Fatal("selected operator", tc, i, id, err)
			}
		}
	}
	if _, err := (sqltype.IndexForeignKeyComparison{}).EqualityInputs(); err == nil {
		t.Fatal("unbound operator metadata accepted")
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE pc_unit_enum AS ENUM ('z','a')"))
	view = e.CatalogRead()
	enum := domainUnitType(t, view, "pc_unit_enum").SQLType
	class, _, defaultErr := view.DefaultIndexClass(403, enum)
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	bound, err := routine.BindIndexForeignKey(view, enum.Usage(), enum.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := bound.EqualityInputs()
	if err != nil || inputs != ([3][2]sqltype.ID{{3500, 3500}, {3500, 3500}, {3500, 3500}}) {
		t.Fatal("enum normalized signature", inputs, err)
	}
	for _, pair := range inputs {
		id, err := pgcatalog.ConstraintOperator(view, "=", pair[0], pair[1])
		if err != nil || id != 3516 {
			t.Fatal("enum operator", id, err)
		}
	}
}
