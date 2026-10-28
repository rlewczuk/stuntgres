package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"stuntgres"
)

// Assert the physical words independently, including vector ArrayType fields.
// These are native datum images, not the text accepted by replay constructors.
func mcvCapabilityValue(t *testing.T, typ string, payload []byte) string {
	t.Helper()
	if typ == "xid" || typ == "cid" {
		if len(payload) != 4 {
			t.Fatalf("%s word extent %d, want 4", typ, len(payload))
		}
		switch binary.LittleEndian.Uint32(payload) {
		case 4294967294:
			return "A"
		case 7:
			return "B"
		default:
			t.Fatalf("%s native word %x", typ, payload)
		}
	}
	width, elementOID := 4, uint32(26)
	if typ == "int2vector" {
		width, elementOID = 2, 21
	}
	if len(payload) != 20+2*width {
		t.Fatalf("%s datum extent %d, want %d", typ, len(payload), 20+2*width)
	}
	if binary.LittleEndian.Uint32(payload) != 1 ||
		binary.LittleEndian.Uint32(payload[4:]) != 0 ||
		binary.LittleEndian.Uint32(payload[8:]) != elementOID ||
		binary.LittleEndian.Uint32(payload[12:]) != 2 ||
		binary.LittleEndian.Uint32(payload[16:]) != 0 {
		t.Fatalf("%s native rank/dataoffset/element/dimension/lower-bound: %x", typ, payload)
	}
	var first, second uint32
	if width == 4 {
		first = binary.LittleEndian.Uint32(payload[20:])
		second = binary.LittleEndian.Uint32(payload[24:])
	} else {
		first = uint32(binary.LittleEndian.Uint16(payload[20:]))
		second = uint32(binary.LittleEndian.Uint16(payload[22:]))
	}
	switch {
	case first == 1 && second == 2:
		return "A"
	case first == 3 && second == 4:
		return "B"
	default:
		t.Fatalf("%s native vector elements %d %d", typ, first, second)
	}
	return "" // Fatalf terminates the test.
}

func mcvCapabilityImage(t *testing.T, typ string, image mcvImage) {
	t.Helper()
	typeOID := map[string]uint32{"xid": 28, "cid": 29, "oidvector": 30, "int2vector": 22}[typ]
	if typeOID == 0 || len(image.dimensions) != 2 || len(image.items) != 4 {
		t.Fatal("capability MCV dimensions/items", typ, image)
	}
	first, second := image.dimensions[0], image.dimensions[1]
	vector := typ == "oidvector" || typ == "int2vector"
	length := 4
	if vector {
		length = -1
	}
	if first.oid != typeOID || first.length != length || first.byval == vector ||
		second.oid != 23 || second.length != 4 || !second.byval ||
		len(first.values) != 2 || len(second.values) != 2 {
		t.Fatal("capability logical/physical dimension facts", image.dimensions)
	}
	// Both dictionaries are inspected, not merely the most common item.
	for _, payload := range first.values {
		mcvCapabilityValue(t, typ, payload)
	}
	want := map[string][2]float64{
		"A,1":       {.4, .24},
		"A,2":       {.2, .24},
		"B,2":       {.2, .08},
		"NULL,NULL": {.2, .04},
	}
	seen := make(map[string]bool, len(want))
	for _, item := range image.items {
		if len(item.nulls) != 2 || len(item.values) != 2 {
			t.Fatal("capability item width", item)
		}
		key := "NULL,NULL"
		if item.nulls[0] || item.nulls[1] {
			if !item.nulls[0] || !item.nulls[1] || item.values[0] != nil || item.values[1] != nil {
				t.Fatal("true SQL NULL pair markers", item)
			}
		} else {
			if len(item.values[1]) != 4 {
				t.Fatal("INT4 dictionary word extent", item.values[1])
			}
			key = fmt.Sprintf("%s,%d", mcvCapabilityValue(t, typ, item.values[0]), int32(binary.LittleEndian.Uint32(item.values[1])))
		}
		expected, ok := want[key]
		if !ok || seen[key] || math.Abs(item.frequency-expected[0]) > 1e-14 || math.Abs(item.baseline-expected[1]) > 1e-14 {
			t.Fatal("retained full-population capability distribution", key, item, expected)
		}
		seen[key] = true
	}
}

func mcvCapabilityRestore(t *testing.T, s ndistinctScenario, typ string) {
	t.Helper()
	var saved bytes.Buffer
	if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
		t.Fatal(err)
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name() + "-stale-capability-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(connector)
	defer destination.Close()
	if _, err := destination.ExecContext(s.pair.Ctx, "CREATE TABLE collision(a INT,b INT); CREATE STATISTICS collision_s ON a,b FROM collision"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(s.pair.Ctx, destination, bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	conn, err := destination.Conn(s.pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	target := s
	target.conn = conn
	_, retained := readMCV(t, target, "mcv_cs", false)
	mcvCapabilityImage(t, typ, retained)
	// Only A,1 twice and the SQL NULL pair remain. The retained four-item
	// .4/.2/.2/.2 population cannot have been regenerated from these rows.
	rows, err := conn.QueryContext(s.pair.Ctx, "SELECT b FROM mcv_custom ORDER BY b")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i := range 3 {
		if !rows.Next() {
			t.Fatal("restored live row missing", i, rows.Err())
		}
		var value sql.NullInt64
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if i < 2 && (!value.Valid || value.Int64 != 1) || i == 2 && value.Valid {
			t.Fatal("restored live substantive rows", i, value)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("restored live row extent", rows.Err())
	}
	rows.Close()
	if _, err := conn.ExecContext(s.pair.Ctx, "ANALYZE mcv_custom"); err != nil {
		t.Fatal(err)
	}
	_, refreshed := readMCV(t, target, "mcv_cs", false)
	if len(refreshed.items) != 2 {
		t.Fatal("actual remeasurement must differ from retained four-group state", refreshed.items)
	}
	nullGroups := 0
	for _, item := range refreshed.items {
		frequency, baseline := 2.0/3, 4.0/9
		if item.nulls[0] {
			nullGroups++
			if !item.nulls[1] {
				t.Fatal("refreshed NULL markers", item)
			}
			frequency, baseline = 1.0/3, 1.0/9
		} else if item.nulls[1] || mcvCapabilityValue(t, typ, item.values[0]) != "A" || len(item.values[1]) != 4 || binary.LittleEndian.Uint32(item.values[1]) != 1 {
			t.Fatal("refreshed actual tuple", item)
		}
		if math.Abs(item.frequency-frequency) > 1e-14 || math.Abs(item.baseline-baseline) > 1e-14 {
			t.Fatal("refreshed live-population frequencies", item)
		}
	}
	if nullGroups != 1 {
		t.Fatal("refreshed population must contain exactly one NULL group", nullGroups)
	}
}
