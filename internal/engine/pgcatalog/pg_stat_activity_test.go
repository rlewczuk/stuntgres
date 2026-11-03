package pgcatalog

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestPGStatActivityDescriptorAndMask(t *testing.T) {
	names := []string{"datid", "datname", "pid", "leader_pid", "usesysid", "usename", "application_name", "client_addr", "client_hostname", "client_port", "backend_start", "xact_start", "query_start", "state_change", "wait_event_type", "wait_event", "state", "backend_xid", "backend_xmin", "query_id", "query", "backend_type"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Int4, sqltype.Int4, sqltype.Oid, sqltype.Name, sqltype.Text, sqltype.Inet, sqltype.Text, sqltype.Int4, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Timestamptz, sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Xid, sqltype.Xid, sqltype.Int8, sqltype.Text, sqltype.Text}
	count := 0
	for _, spec := range Specs {
		if spec.Name != "pg_stat_activity" {
			continue
		}
		count++
		if spec.OID != StatActivityOID || !spec.Virtual || spec.NoPublic || len(spec.Columns) != 22 {
			t.Fatal("activity spec", spec)
		}
		for i, c := range spec.Columns {
			if c.Name != names[i] || c.Type != types[i] || !c.Nullable {
				t.Fatal("activity column", i, c)
			}
		}
	}
	if count != 1 {
		t.Fatal("activity registration count", count)
	}
	if p, ok := ProducerFor(StatActivityOID); !ok || p == nil {
		t.Fatal("missing common producer")
	}
	descriptors := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		descriptors[d.ID()] = d
	}
	row := make([]sqltype.Value, 22)
	for i, id := range types {
		text := "1"
		switch id {
		case sqltype.Name, sqltype.Text:
			text = "present"
		case sqltype.Inet:
			text = "192.0.2.1"
		case sqltype.Timestamptz:
			text = "2026-01-01 00:00:00+00"
		}
		v, err := descriptors[id].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(i, err)
		}
		row[i] = v
	}
	before := append([]sqltype.Value(nil), row...)
	redactActivity(row)
	public := map[int]bool{0: true, 1: true, 2: true, 4: true, 5: true, 6: true, 17: true, 18: true}
	for i, v := range row {
		if public[i] {
			if !reflect.DeepEqual(v, before[i]) {
				t.Fatal("redacted public field", names[i])
			}
		} else if i == 20 {
			if text, err := v.Text(); err != nil || text != "<insufficient privilege>" {
				t.Fatal("query mask", text, err)
			}
		} else if !v.IsNull() || v.Type() != before[i].Type() {
			t.Fatal("protected field leaked or lost type", names[i], v)
		}
	}
	if BuiltinViewEvents(StatActivityOID, nil) != 0 {
		t.Fatal("activity view is writable")
	}
	// Published clocks are time.Time facts, not already-encoded PostgreSQL
	// microseconds. Exercise the actual row codec with an independent epoch value.
	source := &catalog.Relation{Columns: make([]catalog.Column, len(types))}
	for i, id := range types {
		source.Columns[i].Usage = descriptors[id].Usage()
	}
	stamp := time.Date(2026, time.November, 3, 14, 15, 16, 123456000, time.FixedZone("activity", 2*60*60))
	fact := statistics.Activity{Principal: (&authorization.Domain{}).Principal(10), BackendStart: stamp, TransactionStart: stamp, Start: stamp, StateChange: stamp, QueryAvailable: true}
	fact.QueryID = -9223372036854775807 // High bit set; never narrow or decimal-rehash.
	input := Input{Source: source}
	token := (&ids.Domain{}).Session(1)
	rendered, err := activityRow(input, token, fact, true, "current_login", true)
	if err != nil {
		t.Fatal("activity timestamp codec", err)
	}
	if id, err := rendered[19].Integer(); err != nil || id != fact.QueryID {
		t.Fatal("signed activity query ID", id, err)
	}
	if name, err := rendered[5].Text(); err != nil || name != "current_login" {
		t.Fatal("current joined role name", name, err)
	}
	for _, i := range []int{10, 11, 12, 13} {
		value, err := rendered[i].Timestamp()
		if err != nil || value != stamp.UnixMicro()-946684800000000 {
			t.Fatal("timestamp epoch/UTC conversion", names[i], value, err)
		}
	}
	empty, err := activityRow(input, token, statistics.Activity{}, true, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !empty[5].IsNull() || empty[5].Type().ID() != sqltype.Name {
		t.Fatal("absent joined role name must be typed NULL", empty[5])
	}
	for _, i := range []int{10, 11, 12, 13} {
		if !empty[i].IsNull() {
			t.Fatal("absent clock fabricated", names[i], empty[i])
		}
	}
	if !empty[19].IsNull() {
		t.Fatal("zero query ID must remain NULL", empty[19])
	}
}
