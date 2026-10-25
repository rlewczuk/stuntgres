package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTimezoneSourceDescriptors(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		oid   ids.OID
		names []string
		types []sqltype.ID
	}{
		{12005, []string{"name", "abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Interval, sqltype.Bool}},
		{12004, []string{"abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Interval, sqltype.Bool}},
	} {
		d := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tc.oid})
		if !d.VirtualView || d.View != nil || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || len(d.Columns) != len(tc.names) {
			t.Fatal(d)
		}
		for i, c := range d.Columns {
			owner, ok := e.CatalogRead().LookupType(tc.types[i])
			if !ok || c.Name != tc.names[i] || c.Usage.Type != owner || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(tc.oid) || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) {
				t.Fatal(i, c)
			}
		}
		if err := (core.RelationProvider).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal(d.Security.ACL)
		}
		invalid := *d
		invalid.RelationKind = catalog.TableRelation
		if err := (core.RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("virtual table accepted")
		}
		invalid = *d
		invalid.Columns = append([]catalog.Column(nil), d.Columns...)
		foreign, ok := engine.New().CatalogRead().LookupType(tc.types[0])
		if !ok {
			t.Fatal("foreign type missing")
		}
		invalid.Columns[0].Usage.Type = foreign
		if err := (core.RelationProvider).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("foreign owner type accepted")
		}
	}
}

func TestTimezoneSourceCapturedInstantAndOwnership(t *testing.T) {
	e := engine.New()
	d := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12005})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	boundary := time.Date(2024, 3, 10, 7, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		instant time.Time
		offset  int64
		dst     bool
	}{
		{boundary.Add(-time.Microsecond), -18000000000, false},
		{boundary, -14400000000, true},
	} {
		input := sqltype.InputEnvironment{TransactionStart: tc.instant}
		rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Input: input})}, captureTimezoneSource)
		if err != nil {
			t.Fatal(err)
		}
		input.TransactionStart = boundary.Add(24 * time.Hour)
		if rows.Ref != ref {
			t.Fatal("lost source identity")
		}
		found := false
		for _, row := range rows.Anonymous {
			for i, value := range row {
				if value.Type() != d.Columns[i].Usage.Type || value.IsNull() {
					t.Fatal("unowned/null fact", i, value)
				}
			}
			name, _ := row[0].Text()
			if name != "America/New_York" {
				continue
			}
			months, days, micros, err := row[2].Interval()
			dst, dstErr := row[3].Boolean()
			if err != nil || dstErr != nil || months != 0 || days != 0 || micros != tc.offset || dst != tc.dst {
				t.Fatal(months, days, micros, dst, err, dstErr)
			}
			found = true
		}
		if !found {
			t.Fatal("missing NY fact")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Ref: ref, Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Input: sqltype.InputEnvironment{}})}, captureTimezoneSource); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(source, err)
	}
}

func TestTimezoneSourceScopeRetainsClockAndSettings(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, session)
	defer op.Done()
	op.CommandStart = time.Date(2024, 3, 10, 6, 59, 59, 0, time.UTC)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	op.CommandStart = op.CommandStart.Add(time.Second)
	session.Settings.Mu.Lock()
	session.Settings.Typed["timezone_abbreviations"] = settings.Value{Text: "India", TimezoneAbbreviations: sqltype.TimezoneAbbreviationsIndia}
	session.Settings.Mu.Unlock()
	for range 2 {
		for _, tc := range []struct {
			oid    ids.OID
			label  string
			offset int64
		}{
			{12005, "America/New_York", -18000000000},
			{12004, "IST", 7200000000},
		} {
			d := e.Catalog.State.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tc.oid})
			ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
			e.Mu.Lock()
			rows, err := core.EngineOpenRelationSourceLocked(e, scope, ref, d.Owner, plan.RowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows.Anonymous {
				label, _ := row[0].Text()
				if label != tc.label {
					continue
				}
				months, days, micros, err := row[len(row)-2].Interval()
				if err != nil || months != 0 || days != 0 || micros != tc.offset {
					t.Fatal(label, months, days, micros, err)
				}
				found = true
			}
			if !found {
				t.Fatal("captured source missing label", tc.label)
			}
		}
	}
}
