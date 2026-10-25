package core_test

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
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func pgDescriptionUnitSource(t *testing.T, e *core.Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2609}
	d, ok := e.Catalog.State.Relation(address)
	if !ok {
		t.Fatal("missing catalog descriptor")
	}
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}

func pgDescriptionUnitBag(t *testing.T, source *exec.Source) map[ids.ObjectAddress]string {
	t.Helper()
	bag := make(map[ids.ObjectAddress]string)
	for _, row := range source.Anonymous {
		if len(row) != 4 {
			t.Fatal("description width", row)
		}
		object, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		class, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		sub, err := row[2].Integer()
		if err != nil {
			t.Fatal(err)
		}
		text, err := row[3].Text()
		if err != nil || text == "" {
			t.Fatal("description payload", text, err)
		}
		key := ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(sub)}
		if _, exists := bag[key]; exists {
			t.Fatal("duplicate address", key)
		}
		bag[key] = text
	}
	return bag
}

func TestPGDescriptionShape(t *testing.T) {
	e := engine.New()
	d, _ := pgDescriptionUnitSource(t, e)
	if d.Name() != "pg_description" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 4 || len(d.Columns) != 4 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name      string
		typ       sqltype.ID
		collation uint32
	}{{"objoid", sqltype.Oid, 0}, {"classoid", sqltype.Oid, 0}, {"objsubid", sqltype.Int4, 0}, {"description", sqltype.Text, 950}} {
		c := d.Columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != tc.collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2609 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) {
			t.Fatal("exact descriptor", i, c)
		}
	}
	if _, exists := d.Index["oid"]; exists {
		t.Fatal("fabricated row oid")
	}
	if err := (core.RelationProvider).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal("virtual catalog metadata must validate without physical constraints", err)
	}
	physical := *d
	physical.RelationKind = catalog.TableRelation
	if err := (core.RelationProvider).Validate(e.CatalogRead(), &physical); err == nil {
		t.Fatal("physical table NOT NULL metadata requires constraint identities")
	}
	foreignType, _ := engine.New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		name       string
		invalidate func(*catalog.Relation)
	}{
		{"identity", func(r *catalog.Relation) { r.ObjectAddress.ObjectID = 0 }},
		{"type", func(r *catalog.Relation) { r.Columns[0].Usage.Type = foreignType }},
		{"live attribute", func(r *catalog.Relation) { r.Attributes[0] = 0 }},
		{"constraint record", func(r *catalog.Relation) { r.Constraints.NotNull = []catalog.NotNullConstraint{{Attribute: 1}} }},
		{"column comment", func(r *catalog.Relation) { r.ColumnMeta.Comments = map[int32]string{99: "dead attribute"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *d
			invalid.Columns = append([]catalog.Column(nil), d.Columns...)
			invalid.Attributes = append([]int32(nil), d.Attributes...)
			tc.invalidate(&invalid)
			if err := (core.RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
				t.Fatal("virtual-source exemption bypassed descriptor validation")
			}
		})
	}
	class := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := captureCatalogSourceLocked(e, context.Background(), e.CatalogRead(), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.Anonymous {
		oid, _ := row[0].OID()
		if oid == 2609 {
			name, _ := row[1].Text()
			namespace, _ := row[2].OID()
			kind, _ := row[class.Index["relkind"]].Text()
			natts, _ := row[class.Index["relnatts"]].Integer()
			if name != "pg_description" || namespace != 11 || kind != "r" || natts != 4 {
				t.Fatal(row)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("pg_class did not discover catalog relation")
	}
}

func TestPGDescriptionStableKeysAndSnapshots(t *testing.T) {
	e := engine.New()
	source, ref := pgDescriptionUnitSource(t, e)
	oldState := *e.Catalog.State
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}
	relation := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	d := &catalog.Relation{ObjectAddress: relation, Attributes: []int32{1, 3}, Comment: "whole", ColumnMeta: catalog.ColumnMetadata{Comments: map[int32]string{1: "", 2: "stale dropped", 3: "third"}}}
	oldState.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, Comment: "namespace"})
	oldState.SetDescriptor(relation, d)
	old := e.Catalog.Read(&oldState)
	captured, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: old, Ref: ref, Source: source, Owner: (old).Owner(), Runtime: core.EngineCatalogRuntime{}}, capturePGDescription)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	want := map[ids.ObjectAddress]string{namespace: "namespace", relation: "whole", {ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 3}: "third"}
	for key, text := range want {
		if bag[key] != text {
			t.Fatal(key, bag[key], text)
		}
	}
	if _, ok := bag[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 2}]; ok {
		t.Fatal("stale map entry projected")
	}
	if _, ok := bag[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 1}]; ok {
		t.Fatal("empty row projected")
	}
	if !reflect.DeepEqual(d.Attributes, []int32{1, 3}) || d.ColumnMeta.Comments[2] != "stale dropped" {
		t.Fatal("capture mutated descriptor")
	}
	newState := oldState
	updated := *d
	updated.Comment = "replacement"
	updated.ColumnMeta.Comments = map[int32]string{3: "replacement third"}
	newState.SetDescriptor(relation, &updated)
	newRows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.Catalog.Read(&newState), Ref: ref, Source: source, Owner: (e.Catalog.Read(&newState)).Owner(), Runtime: core.EngineCatalogRuntime{}}, capturePGDescription)
	if err != nil || pgDescriptionUnitBag(t, newRows)[relation] != "replacement" {
		t.Fatal(err)
	}
	newState.RemoveDescriptor(relation)
	if pgDescriptionUnitBag(t, captured)[relation] != "whole" || pgcatalog.DescriptorComment(oldState.Get(relation), 3) != "third" {
		t.Fatal("retained snapshot changed")
	}
	// Simulate a nested provider relocking after publication: opening the virtual
	// source must use the statement frontier rather than the newly sampled state.
	e.Catalog.PublishLocked(&newState)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope := &core.StatementScope{Op: op, Owner: op.Session.Transactions, Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: old})}
	e.Mu.Lock()
	frozen, err := core.EngineOpenRelationSourceLocked(e, scope, ref, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), plan.RowMark{}, nil, nil)
	if err != nil || pgDescriptionUnitBag(t, frozen)[relation] != "whole" {
		t.Fatal("nested frontier", err)
	}
}

func TestPGDescriptionCanceledCapture(t *testing.T) {
	e := engine.New()
	source, ref := pgDescriptionUnitSource(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, state := range []*catalog.State{e.Catalog.State, catalog.NewState(0)} {
		captured, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: e.Catalog.Read(state), Ref: ref, Source: source, Owner: (e.Catalog.Read(state)).Owner(), Runtime: core.EngineCatalogRuntime{}}, capturePGDescription)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", captured, err)
		}
	}
}

func TestPGDescriptionBuiltinSeeds(t *testing.T) {
	e := engine.New()
	source, ref := pgDescriptionUnitSource(t, e)
	captured, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: source, Owner: (e.CatalogRead()).Owner(), Runtime: core.EngineCatalogRuntime{}}, capturePGDescription)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	expected := map[ids.ObjectAddress]string{
		{ClassID: ids.NamespaceClassID, ObjectID: 11}:   "system catalog schema",
		{ClassID: ids.NamespaceClassID, ObjectID: 99}:   "reserved schema for TOAST tables",
		{ClassID: ids.NamespaceClassID, ObjectID: 2200}: "standard public schema",
	}
	for _, group := range []struct {
		class ids.OID
		seeds []pgDescriptionExpectedSeed
	}{
		{ids.TypeClassID, pgDescriptionExpectedTypes},
		{ids.CollationClassID, pgDescriptionExpectedCollations},
		{ids.ConversionClassID, pgDescriptionExpectedConversions},
	} {
		for _, seed := range group.seeds {
			expected[ids.ObjectAddress{ClassID: group.class, ObjectID: seed.oid}] = seed.text
		}
	}
	if len(bag) != 166 || !reflect.DeepEqual(bag, expected) {
		t.Fatal("complete independent baseline", bag, expected)
	}
	for _, oid := range []ids.OID{4401, 4450, 4451, 4532} {
		if pgcatalog.ConversionDescription(oid) != "" {
			t.Fatal("conversion hole or boundary", oid)
		}
	}
	for _, descriptor := range e.Catalog.State.Descriptors() {
		switch descriptor.(type) {
		case *catalog.Collation, *catalog.Conversion:
			if pgcatalog.DescriptorComment(descriptor, 0) != "" || pgcatalog.Description(descriptor, 1) != "" {
				t.Fatal("projection widened inquiry/subobject boundary", descriptor)
			}
		}
	}
	if _, exists := bag[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1003}]; exists {
		t.Fatal("array inherited description")
	}
}

// Independent release expectations shared with the public reference scenarios,
// never constructed from production inventories or native output.
type pgDescriptionExpectedSeed struct {
	oid  ids.OID
	text string
}

var pgDescriptionExpectedTypes = []pgDescriptionExpectedSeed{
	{16, "boolean, format 't'/'f'"},
	{17, "variable-length string, binary values escaped"},
	{18, "single character"},
	{19, "63-byte type for storing system identifiers"},
	{20, "~18 digit integer, 8-byte storage"},
	{21, "-32 thousand to 32 thousand, 2-byte storage"},
	{23, "-2 billion to 2 billion integer, 4-byte storage"},
	{25, "variable-length string, no limit specified"},
	{26, "object identifier(oid), maximum 4 billion"},
	{114, "JSON stored as text"},
	{194, "string representing an internal node tree"},
	{700, "single-precision floating point number, 4-byte storage"},
	{701, "double-precision floating point number, 8-byte storage"},
	{705, "pseudo-type representing an undetermined type"},
	{790, "monetary amounts, $d,ddd.cc"},
	{1042, "'char(length)' blank-padded string, fixed storage length"},
	{1043, "'varchar(length)' non-blank-padded string, variable storage length"},
	{1082, "date"},
	{1083, "time of day"},
	{1114, "date and time"},
	{1184, "date and time with time zone"},
	{1186, "time interval, format 'number units ...'"},
	{1266, "time of day with time zone"},
	{1700, "'numeric(precision, scale)' arbitrary precision number"},
	{2205, "registered class"},
	{2278, "pseudo-type for the result of a function with no real result"},
	{2950, "UUID"},
	{3802, "Binary JSON"},
}

var pgDescriptionExpectedCollations = []pgDescriptionExpectedSeed{
	{100, "database's default collation"},
	{950, "standard C collation"},
	{951, "standard POSIX collation"},
	{962, "sorts by Unicode code point, C character semantics"},
	{963, "sorts using the Unicode Collation Algorithm with default settings"},
	{811, "sorts by Unicode code point; Unicode and POSIX character semantics"},
	{6411, "sorts by Unicode code point; Unicode character semantics"},
}

// REL_18_0 pg_conversion.dat descr fields.
var pgDescriptionExpectedConversions = []pgDescriptionExpectedSeed{
	{4402, "conversion for KOI8R to MULE_INTERNAL"},
	{4403, "conversion for MULE_INTERNAL to KOI8R"},
	{4404, "conversion for ISO-8859-5 to MULE_INTERNAL"},
	{4405, "conversion for MULE_INTERNAL to ISO-8859-5"},
	{4406, "conversion for WIN1251 to MULE_INTERNAL"},
	{4407, "conversion for MULE_INTERNAL to WIN1251"},
	{4408, "conversion for WIN866 to MULE_INTERNAL"},
	{4409, "conversion for MULE_INTERNAL to WIN866"},
	{4410, "conversion for KOI8R to WIN1251"},
	{4411, "conversion for WIN1251 to KOI8R"},
	{4412, "conversion for KOI8R to WIN866"},
	{4413, "conversion for WIN866 to KOI8R"},
	{4414, "conversion for WIN866 to WIN1251"},
	{4415, "conversion for WIN1251 to WIN866"},
	{4416, "conversion for ISO-8859-5 to KOI8R"},
	{4417, "conversion for KOI8R to ISO-8859-5"},
	{4418, "conversion for ISO-8859-5 to WIN1251"},
	{4419, "conversion for WIN1251 to ISO-8859-5"},
	{4420, "conversion for ISO-8859-5 to WIN866"},
	{4421, "conversion for WIN866 to ISO-8859-5"},
	{4422, "conversion for EUC_CN to MULE_INTERNAL"},
	{4423, "conversion for MULE_INTERNAL to EUC_CN"},
	{4424, "conversion for EUC_JP to SJIS"},
	{4425, "conversion for SJIS to EUC_JP"},
	{4426, "conversion for EUC_JP to MULE_INTERNAL"},
	{4427, "conversion for SJIS to MULE_INTERNAL"},
	{4428, "conversion for MULE_INTERNAL to EUC_JP"},
	{4429, "conversion for MULE_INTERNAL to SJIS"},
	{4430, "conversion for EUC_KR to MULE_INTERNAL"},
	{4431, "conversion for MULE_INTERNAL to EUC_KR"},
	{4432, "conversion for EUC_TW to BIG5"},
	{4433, "conversion for BIG5 to EUC_TW"},
	{4434, "conversion for EUC_TW to MULE_INTERNAL"},
	{4435, "conversion for BIG5 to MULE_INTERNAL"},
	{4436, "conversion for MULE_INTERNAL to EUC_TW"},
	{4437, "conversion for MULE_INTERNAL to BIG5"},
	{4438, "conversion for LATIN2 to MULE_INTERNAL"},
	{4439, "conversion for MULE_INTERNAL to LATIN2"},
	{4440, "conversion for WIN1250 to MULE_INTERNAL"},
	{4441, "conversion for MULE_INTERNAL to WIN1250"},
	{4442, "conversion for LATIN2 to WIN1250"},
	{4443, "conversion for WIN1250 to LATIN2"},
	{4444, "conversion for LATIN1 to MULE_INTERNAL"},
	{4445, "conversion for MULE_INTERNAL to LATIN1"},
	{4446, "conversion for LATIN3 to MULE_INTERNAL"},
	{4447, "conversion for MULE_INTERNAL to LATIN3"},
	{4448, "conversion for LATIN4 to MULE_INTERNAL"},
	{4449, "conversion for MULE_INTERNAL to LATIN4"},
	{4452, "conversion for BIG5 to UTF8"},
	{4453, "conversion for UTF8 to BIG5"},
	{4454, "conversion for UTF8 to KOI8R"},
	{4455, "conversion for KOI8R to UTF8"},
	{4456, "conversion for UTF8 to KOI8U"},
	{4457, "conversion for KOI8U to UTF8"},
	{4458, "conversion for UTF8 to WIN866"},
	{4459, "conversion for WIN866 to UTF8"},
	{4460, "conversion for UTF8 to WIN874"},
	{4461, "conversion for WIN874 to UTF8"},
	{4462, "conversion for UTF8 to WIN1250"},
	{4463, "conversion for WIN1250 to UTF8"},
	{4464, "conversion for UTF8 to WIN1251"},
	{4465, "conversion for WIN1251 to UTF8"},
	{4466, "conversion for UTF8 to WIN1252"},
	{4467, "conversion for WIN1252 to UTF8"},
	{4468, "conversion for UTF8 to WIN1253"},
	{4469, "conversion for WIN1253 to UTF8"},
	{4470, "conversion for UTF8 to WIN1254"},
	{4471, "conversion for WIN1254 to UTF8"},
	{4472, "conversion for UTF8 to WIN1255"},
	{4473, "conversion for WIN1255 to UTF8"},
	{4474, "conversion for UTF8 to WIN1256"},
	{4475, "conversion for WIN1256 to UTF8"},
	{4476, "conversion for UTF8 to WIN1257"},
	{4477, "conversion for WIN1257 to UTF8"},
	{4478, "conversion for UTF8 to WIN1258"},
	{4479, "conversion for WIN1258 to UTF8"},
	{4480, "conversion for EUC_CN to UTF8"},
	{4481, "conversion for UTF8 to EUC_CN"},
	{4482, "conversion for EUC_JP to UTF8"},
	{4483, "conversion for UTF8 to EUC_JP"},
	{4484, "conversion for EUC_KR to UTF8"},
	{4485, "conversion for UTF8 to EUC_KR"},
	{4486, "conversion for EUC_TW to UTF8"},
	{4487, "conversion for UTF8 to EUC_TW"},
	{4488, "conversion for GB18030 to UTF8"},
	{4489, "conversion for UTF8 to GB18030"},
	{4490, "conversion for GBK to UTF8"},
	{4491, "conversion for UTF8 to GBK"},
	{4492, "conversion for UTF8 to LATIN2"},
	{4493, "conversion for LATIN2 to UTF8"},
	{4494, "conversion for UTF8 to LATIN3"},
	{4495, "conversion for LATIN3 to UTF8"},
	{4496, "conversion for UTF8 to LATIN4"},
	{4497, "conversion for LATIN4 to UTF8"},
	{4498, "conversion for UTF8 to LATIN5"},
	{4499, "conversion for LATIN5 to UTF8"},
	{4500, "conversion for UTF8 to LATIN6"},
	{4501, "conversion for LATIN6 to UTF8"},
	{4502, "conversion for UTF8 to LATIN7"},
	{4503, "conversion for LATIN7 to UTF8"},
	{4504, "conversion for UTF8 to LATIN8"},
	{4505, "conversion for LATIN8 to UTF8"},
	{4506, "conversion for UTF8 to LATIN9"},
	{4507, "conversion for LATIN9 to UTF8"},
	{4508, "conversion for UTF8 to LATIN10"},
	{4509, "conversion for LATIN10 to UTF8"},
	{4510, "conversion for UTF8 to ISO-8859-5"},
	{4511, "conversion for ISO-8859-5 to UTF8"},
	{4512, "conversion for UTF8 to ISO-8859-6"},
	{4513, "conversion for ISO-8859-6 to UTF8"},
	{4514, "conversion for UTF8 to ISO-8859-7"},
	{4515, "conversion for ISO-8859-7 to UTF8"},
	{4516, "conversion for UTF8 to ISO-8859-8"},
	{4517, "conversion for ISO-8859-8 to UTF8"},
	{4518, "conversion for LATIN1 to UTF8"},
	{4519, "conversion for UTF8 to LATIN1"},
	{4520, "conversion for JOHAB to UTF8"},
	{4521, "conversion for UTF8 to JOHAB"},
	{4522, "conversion for SJIS to UTF8"},
	{4523, "conversion for UTF8 to SJIS"},
	{4524, "conversion for UHC to UTF8"},
	{4525, "conversion for UTF8 to UHC"},
	{4526, "conversion for EUC_JIS_2004 to UTF8"},
	{4527, "conversion for UTF8 to EUC_JIS_2004"},
	{4528, "conversion for SHIFT_JIS_2004 to UTF8"},
	{4529, "conversion for UTF8 to SHIFT_JIS_2004"},
	{4530, "conversion for EUC_JIS_2004 to SHIFT_JIS_2004"},
	{4531, "conversion for SHIFT_JIS_2004 to EUC_JIS_2004"},
}
