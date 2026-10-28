package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A foreign nonnil carrier delegates real metadata but is not a stored payload.
type foreignTriggerPayload struct{ catalog.TriggersPayload }

type cancelTriggerCapture struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
}

func (r cancelTriggerCapture) VisitTriggerCatalog(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, visit func(pgcatalog.TriggerCatalogFact) error) error {
	return r.EngineCatalogRuntime.VisitTriggerCatalog(ctx, read, owner, typ, func(f pgcatalog.TriggerCatalogFact) error {
		if err := visit(f); err != nil {
			return err
		}
		r.cancel()
		return ctx.Err()
	})
}

func triggerCaptureInput(t *testing.T, f defaultUnitFixture, read catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, read)
	source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.TriggerClassID})
	return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Source: source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Runtime: core.EngineCatalogRuntime{Auth: auth}}
}

func TestPGTriggerCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	s := enginetest.StartSession(t, f.e, nil)
	produce := registeredCatalogProducer(ids.TriggerClassID)
	for _, populated := range []bool{false, true} {
		if populated {
			for _, q := range []string{"CREATE TABLE trigger_capture(a integer,b integer,c integer)", "CREATE FUNCTION trigger_capture_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER actual BEFORE UPDATE OF c,a ON trigger_capture FOR EACH ROW WHEN(OLD.a>0) EXECUTE FUNCTION trigger_capture_fn('x','','é')"} {
				enginetest.SessionSQL(t, s, q)
			}
		}
		in := triggerCaptureInput(t, f, f.e.CatalogRead())
		rows, err := produce(in)
		want := 0
		if populated {
			want = 1
		}
		if err != nil || rows == nil || len(rows) != want {
			t.Fatal("actual capture", rows, err)
		}
		if populated {
			row := rows[0]
			for i, v := range row {
				if v.Type() != in.Source.Columns[i].Usage.Type || v.IsNull() != (i >= 17) {
					t.Fatal("owned descriptor/null", i, v)
				}
			}
			flags, _ := row[5].Integer()
			nargs, _ := row[13].Integer()
			args, _ := row[15].Binary()
			attrs, err := row[14].Type().Output(t.Context(), row[14])
			if flags != 19 || nargs != 3 || args != "x\x00\x00é\x00" || err != nil || attrs != "3 1" {
				t.Fatal("definition facts", flags, nargs, args, attrs, err)
			}
			carrier, err := row[16].Tree()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := carrier.(stored.TriggerCarrier); !ok {
				t.Fatal("unchecked tree", carrier)
			}
		}
		reject := func(label string, bad pgcatalog.Input) {
			t.Helper()
			rows, err := produce(bad)
			if rows != nil || err == nil {
				t.Fatal(label, "accepted", rows, err)
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
		reject("read", bad)
		bad = in
		bad.Ref.Epoch++
		reject("epoch", bad)
		bad = in
		bad.Ref = foreign.Owner().Domain.Ref(in.Ref.Epoch, in.Ref.Address)
		reject("domain", bad)
		bad = in
		bad.Ref.Address.SubID = 1
		reject("subID", bad)
		bad = in
		bad.Roles = authorization.Read{}
		reject("roles", bad)
		bad = in
		clone := *in.Source
		bad.Source = &clone
		reject("source pointer", bad)
		for _, shape := range []string{"width", "name", "type", "nullable"} {
			state := catalog.CloneState(in.Catalog.State())
			clone := *in.Source
			clone.Columns = append([]catalog.Column(nil), clone.Columns...)
			switch shape {
			case "width":
				clone.Columns = clone.Columns[:18]
			case "name":
				clone.Columns[0].Name = "wrong"
			case "type":
				clone.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Oid)
			case "nullable":
				clone.Columns[16].Nullable = false
			}
			state.SetDescriptor(clone.Address(), &clone)
			reject(shape, triggerCaptureInput(t, f, in.Catalog.WithState(state)))
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		bad = in
		bad.Context = ctx
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture", rows, err)
		}
		if populated {
			during, cancel := context.WithCancel(t.Context())
			bad := in
			bad.Context = during
			bad.Runtime = cancelTriggerCapture{EngineCatalogRuntime: in.Runtime.(core.EngineCatalogRuntime), cancel: cancel}
			if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("partial capture published", rows, err)
			}
			cancel()
			address := enginetest.CatalogRef(t, f.e, in.Catalog, "trigger_capture").Address
			owner := in.Catalog.MustRelation(address)
			list := owner.Triggers.(stored.TriggerList)
			state := catalog.CloneState(in.Catalog.State())
			clone := *owner
			clone.Triggers = foreignTriggerPayload{owner.Triggers}
			state.SetDescriptor(address, &clone)
			reject("foreign payload", triggerCaptureInput(t, f, in.Catalog.WithState(state)))
			for _, kind := range []string{"zero", "duplicate"} {
				state := catalog.CloneState(in.Catalog.State())
				clone := *owner
				p := list[0].Parts()
				if kind == "zero" {
					p.OID = 0
				} else {
					p.Name = "second"
				}
				definition, err := stored.NewTrigger(p)
				if kind == "zero" {
					if err == nil {
						t.Fatal("zero identity constructor accepted")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				clone.Triggers = stored.TriggerList{list[0], definition}
				state.SetDescriptor(address, &clone)
				reject(kind, triggerCaptureInput(t, f, in.Catalog.WithState(state)))
			}
			if !reflect.DeepEqual(list[0].Parts().Arguments, []string{"x", "", "é"}) {
				t.Fatal("capture mutated arguments")
			}
		}
	}
}

// Real declarations supply relation/index/type authority; explicitly chosen
// immutable identities isolate projection and ownership from the allocator.
func TestPGTriggerForeignKeyCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	base := e.CatalogRead()
	source := base.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.TriggerClassID})
	produce, ok := pgcatalog.ProducerFor(ids.TriggerClassID)
	if !ok {
		t.Fatal("missing trigger producer")
	}
	input := triggerCaptureInput(t, f, base)
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty trigger catalog", rows, err)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE unit_parent(id integer PRIMARY KEY)")
	enginetest.SessionSQL(t, s, "CREATE TABLE unit_child(id integer CONSTRAINT unit_fk REFERENCES unit_parent)")
	base = e.CatalogRead()
	parent := enginetest.CatalogRef(t, e, base, "unit_parent").Address
	child := enginetest.CatalogRef(t, e, base, "unit_child").Address
	definition := *base.MustRelation(child)
	constraint := definition.Constraints.ForeignKeys[0]
	constraint.OID = 41003
	constraint.TriggerOIDs = [4]ids.OID{41004, 41005, 41006, 41007}
	definition.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{constraint}
	state := *base.State()
	state.SetDescriptor(child, &definition)
	retained := e.Catalog.Read(&state)
	input = triggerCaptureInput(t, f, retained)
	rows, err := produce(input)
	if err != nil || len(rows) != 4 {
		t.Fatal("four persistent triggers", rows, err)
	}
	seen := make(map[uint32]bool)
	for _, row := range rows {
		if len(row) != 19 {
			t.Fatal("trigger row width", row)
		}
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || value.IsNull() != (i >= 16) {
				t.Fatal("owned trigger cell", i, value)
			}
		}
		oid, err := row[0].OID()
		if err != nil || oid < 41004 || oid > 41007 || seen[oid] {
			t.Fatal("persistent trigger identity", oid, err)
		}
		seen[oid] = true
		wantRelation, kind := uint32(child.ObjectID), "c"
		if oid < 41006 {
			wantRelation, kind = uint32(parent.ObjectID), "a"
		}
		relation, err := row[1].OID()
		if err != nil || relation != wantRelation {
			t.Fatal("action/check relation", relation, err)
		}
		name, err := row[3].Text()
		if err != nil || name != "RI_ConstraintTrigger_"+kind+"_"+strconv.FormatUint(uint64(oid), 10) {
			t.Fatal("allocator-derived trigger name", row[3])
		}
		internal, err := row[7].Boolean()
		if err != nil || !internal {
			t.Fatal("RI internal flag", internal, err)
		}
		owner, err := row[10].OID()
		if err != nil || owner != uint32(constraint.OID) {
			t.Fatal("constraint identity", owner, err)
		}
		slot := int(oid - 41004)
		function, err := row[4].OID()
		if err != nil || function != [4]uint32{1654, 1655, 1644, 1645}[slot] {
			t.Fatal("RI function", function, err)
		}
		flags, err := row[5].Integer()
		if err != nil || flags != [4]int64{9, 17, 5, 17}[slot] {
			t.Fatal("RI event bits", flags, err)
		}
		opposite, err := row[8].OID()
		wantOpposite := uint32(parent.ObjectID)
		if slot < 2 {
			wantOpposite = uint32(child.ObjectID)
		}
		if err != nil || opposite != wantOpposite {
			t.Fatal("opposite FK relation", opposite, err)
		}
		index, err := row[9].OID()
		if err != nil || index != uint32(constraint.Index.ObjectID) {
			t.Fatal("constraint index", index, err)
		}
		for _, i := range []int{11, 12} {
			flag, err := row[i].Boolean()
			if err != nil || flag {
				t.Fatal("nondeferrable RI state", i, flag, err)
			}
		}
		nargs, err := row[13].Integer()
		if err != nil || nargs != 0 {
			t.Fatal("RI argument count", nargs, err)
		}
		attrs, err := row[14].Type().Output(t.Context(), row[14])
		if err != nil || attrs != "" {
			t.Fatal("RI UPDATE vector", attrs, err)
		}
		args, err := row[15].Binary()
		if err != nil || args != "" {
			t.Fatal("RI argument bytes", args, err)
		}
	}
	for _, action := range []parser.ForeignKeyAction{parser.ForeignKeyNoAction, parser.ForeignKeyRestrict, parser.ForeignKeyCascade, parser.ForeignKeySetNull, parser.ForeignKeySetDefault} {
		timed := constraint
		timed.Deferrable, timed.InitiallyDeferred = true, true
		timed.DeleteAction, timed.UpdateAction = action, action
		timed.TriggerModes = [4]catalog.TriggerMode{catalog.TriggerDisabled, catalog.TriggerReplica, catalog.TriggerAlways, catalog.TriggerDisabled}
		timedOwner := definition
		timedOwner.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{timed}
		timedState := catalog.CloneState(retained.State())
		timedState.SetDescriptor(child, &timedOwner)
		timedRows, err := produce(triggerCaptureInput(t, f, retained.WithState(timedState)))
		if err != nil || len(timedRows) != 4 {
			t.Fatal("timed trigger projection", timedRows, err)
		}
		for _, row := range timedRows {
			oid, err := row[0].OID()
			if err != nil || oid < 41004 || oid > 41007 {
				t.Fatal("timed trigger identity", oid, err)
			}
			slot := int(oid - 41004)
			want := slot >= 2 || action == parser.ForeignKeyNoAction
			for _, column := range []int{11, 12} {
				flag, err := row[column].Boolean()
				if err != nil || flag != want {
					t.Fatal("persistent timing depended on trigger enable mode or wrong action", action, slot, column, flag, err)
				}
			}
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), retained, core.EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	dependencies := make(map[ids.OID]int)
	for _, edge := range edges {
		if edge.Dependent.ClassID == ids.TriggerClassID {
			if edge.Referenced != (ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.OID}) || edge.DepType != 'i' {
				t.Fatal("RI constraint ownership", edge)
			}
			dependencies[edge.Dependent.ObjectID]++
		}
	}
	for _, oid := range constraint.TriggerOIDs {
		if dependencies[oid] != 1 {
			t.Fatal("one internal ownership edge", oid, dependencies)
		}
	}
	next := state
	owner := *next.MustRelation(child)
	owner.Constraints.ForeignKeys = nil
	next.SetDescriptor(child, &owner)
	input = triggerCaptureInput(t, f, e.Catalog.Read(&next))
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("removed constraint retained live triggers", rows, err)
	}
	input = triggerCaptureInput(t, f, retained)
	if rows, err := produce(input); err != nil || len(rows) != 4 {
		t.Fatal("retained snapshot lost identities", rows, err)
	}
	invalid := *source
	invalid.Columns = source.Columns[:4]
	input.Source = &invalid
	if rows, err := produce(input); err == nil || rows != nil {
		t.Fatal("invalid trigger shape admitted", rows, err)
	}
	input.Source = source
	foreign := engine.New().CatalogRead()
	invalid = *source
	invalid.Columns = append([]catalog.Column(nil), source.Columns...)
	invalid.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Oid)
	input.Source = &invalid
	if rows, err := produce(input); err == nil || rows != nil {
		t.Fatal("foreign trigger carrier admitted", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("canceled projection", rows, err)
	}
	for _, kind := range []string{"target", "index", "action", "zero", "duplicate"} {
		badConstraint := constraint
		switch kind {
		case "target":
			badConstraint.Target.ObjectID = 499999
		case "index":
			badConstraint.Index.ObjectID = 499999
		case "action":
			badConstraint.DeleteAction = 255
		case "zero":
			badConstraint.TriggerOIDs[0] = 0
		case "duplicate":
			badConstraint.TriggerOIDs[0] = badConstraint.TriggerOIDs[1]
		}
		badOwner := *retained.MustRelation(child)
		badOwner.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{badConstraint}
		badState := catalog.CloneState(retained.State())
		badState.SetDescriptor(child, &badOwner)
		if rows, err := produce(triggerCaptureInput(t, f, retained.WithState(badState))); err == nil || rows != nil {
			t.Fatal("malformed FK source accepted", kind, rows, err)
		}
	}
	enginetest.SessionSQL(t, s, "CREATE FUNCTION unit_user_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
	enginetest.SessionSQL(t, s, "CREATE TRIGGER unit_user BEFORE INSERT ON unit_parent FOR EACH ROW EXECUTE FUNCTION unit_user_fn()")
	current := e.CatalogRead()
	user := stored.TriggersOf(current.MustRelation(parent))[0]
	collision := *current.MustRelation(child)
	badConstraint := collision.Constraints.ForeignKeys[0]
	badConstraint.TriggerOIDs[0] = user.OID()
	collision.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{badConstraint}
	badState := catalog.CloneState(current.State())
	badState.SetDescriptor(child, &collision)
	if rows, err := produce(triggerCaptureInput(t, f, current.WithState(badState))); err == nil || rows != nil {
		t.Fatal("user/internal OID collision accepted", rows, err)
	}
}
