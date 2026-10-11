package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestTimezoneSourceDescriptors(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		oid   OID
		names []string
		types []sqltype.ID
	}{
		{12005, []string{"name", "abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Interval, sqltype.Bool}},
		{12004, []string{"abbrev", "utc_offset", "is_dst"}, []sqltype.ID{sqltype.Text, sqltype.Interval, sqltype.Bool}},
	} {
		d := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: tc.oid}].(*relationDescriptor)
		if !d.virtualView || d.view != nil || d.relationKind != catalogRelation || d.namespace.ObjectID != 11 || len(d.columns) != len(tc.names) {
			t.Fatal(d)
		}
		for i, c := range d.columns {
			owner, ok := e.CatalogRead().LookupType(tc.types[i])
			if !ok || c.Name != tc.names[i] || c.Usage.Type != owner || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(tc.oid) || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) {
				t.Fatal(i, c)
			}
		}
		if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		if len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Privileges != SelectPrivilege {
			t.Fatal(d.acl)
		}
		invalid := *d
		invalid.relationKind = tableRelation
		if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("virtual table accepted")
		}
		invalid = *d
		invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
		foreign, ok := New().CatalogRead().LookupType(tc.types[0])
		if !ok {
			t.Fatal("foreign type missing")
		}
		invalid.columns[0].Usage.Type = foreign
		if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
			t.Fatal("foreign owner type accepted")
		}
	}
}

func TestTimezoneSourceCapturedInstantAndOwnership(t *testing.T) {
	e := New()
	d := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 12005}].(*relationDescriptor)
	ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: d.address}
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
		rows, err := captureTimezoneSource(context.Background(), ref, d, input)
		if err != nil {
			t.Fatal(err)
		}
		input.TransactionStart = boundary.Add(24 * time.Hour)
		if rows.ref != ref {
			t.Fatal("lost source identity")
		}
		found := false
		for _, row := range rows.anonymous {
			for i, value := range row {
				if value.Type() != d.columns[i].Usage.Type || value.IsNull() {
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
	if source, err := captureTimezoneSource(ctx, ref, d, sqltype.InputEnvironment{}); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(source, err)
	}
}

func TestTimezoneSourceScopeRetainsClockAndSettings(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	op := transactionOperation(t, session)
	defer op.Done()
	op.commandStart = time.Date(2024, 3, 10, 6, 59, 59, 0, time.UTC)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Select{}); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	op.commandStart = op.commandStart.Add(time.Second)
	session.settings.mu.Lock()
	session.settings.typed["timezone_abbreviations"] = settingValue{text: "India", timezoneAbbreviations: sqltype.TimezoneAbbreviationsIndia}
	session.settings.mu.Unlock()
	for range 2 {
		for _, tc := range []struct {
			oid    OID
			label  string
			offset int64
		}{
			{12005, "America/New_York", -18000000000},
			{12004, "IST", 7200000000},
		} {
			d := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: tc.oid}].(*relationDescriptor)
			ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: d.address}
			e.mu.Lock()
			rows, err := e.openRelationSourceLocked(scope, ref, d.owner, queryRowMark{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows.anonymous {
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
