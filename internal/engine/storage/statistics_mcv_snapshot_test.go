package storage

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func mcvSnapshotRecord() ExtendedStatisticsSnapshot {
	dictionary := []json.RawMessage{json.RawMessage(`{"kind":"scalar","scalar":"1"}`), json.RawMessage(`{"kind":"scalar","scalar":"2"}`)}
	return ExtendedStatisticsSnapshot{Schema: "public", Name: "s", RelationSchema: "public", RelationName: "t", Keys: []int32{1, 3}, Kinds: "m", Usages: []NDistinctUsageRecord{{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}, {TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}}, Subsets: [][]int16{}, Counts: []uint64{}, MCV: &ExtendedMCVSnapshot{Rows: 5, Dictionaries: [][]json.RawMessage{dictionary, dictionary}, Items: []ExtendedMCVSnapshotItem{
		{Nulls: []bool{false, false}, Indexes: []uint16{0, 0}, Count: 2, MarginalCounts: []uint64{2, 2}},
		{Nulls: []bool{false, false}, Indexes: []uint16{1, 1}, Count: 2, MarginalCounts: []uint64{2, 2}},
		{Nulls: []bool{true, true}, Indexes: []uint16{0, 0}, Count: 1, MarginalCounts: []uint64{1, 1}},
	}}}
}

func TestStatisticsMCVSnapshot(t *testing.T) {
	record := mcvSnapshotRecord()
	encoded, err := EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, found, err := DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(encoded, "-- "))
	if err != nil || !found || decoded.MCV.Rows != 5 || len(decoded.MCV.Items) != 3 {
		t.Fatal(decoded, found, err)
	}
	again, err := EncodeExtendedStatisticsSnapshot(decoded)
	if err != nil || again != encoded {
		t.Fatal("canonical construction changed", err)
	}
	// Presence means built state, not just a requested m kind.
	record.MCV = nil
	absent, err := EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	legacy, found, err := DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(absent, "-- "))
	if err != nil || !found || legacy.MCV != nil || strings.Contains(absent, `"mcv"`) {
		t.Fatal("legacy absent state", legacy, err)
	}
	_, payload, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(encoded, "-- ")), " ")
	bytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(bytes)
	for _, bad := range []string{
		strings.Replace(raw, `"rows":5`, `"rows":0`, 1),
		strings.Replace(raw, `"rows":5`, `"rows":2147483648`, 1),
		strings.Replace(raw, `"rows":5`, `"rows":5,"rows":5`, 1),
		strings.Replace(raw, `"rows":5,`, "", 1),
		strings.Replace(raw, `"rows":5`, `"other":5`, 1),
		strings.Replace(raw, `"rows":5`, `"rows":null`, 1),
		strings.Replace(raw, `"nulls":[false,false]`, `"nulls":[false]`, 1),
		strings.Replace(raw, `"nulls":[false,false]`, `"nulls":[false,false,false]`, 1),
		strings.Replace(raw, `"nulls":[false,false]`, `"nulls":[0,0]`, 1),
		strings.Replace(raw, `"nulls":[false,false],`, "", 1),
		strings.Replace(raw, `"indexes":[0,0]`, `"indexes":[2,0]`, 1),
		strings.Replace(raw, `"indexes":[1,1]`, `"indexes":[0,0]`, 1),
		strings.Replace(raw, `"nulls":[true,true],"indexes":[0,0]`, `"nulls":[true,true],"indexes":[1,0]`, 1),
		strings.Replace(raw, `"count":2`, `"count":0`, 1),
		strings.Replace(raw, `"count":2`, `"count":3`, 1),
		strings.Replace(raw, `"count":1`, `"count":2`, 1),
		strings.Replace(raw, `"marginal_counts":[2,2]`, `"marginal_counts":[1,2]`, 1),
		strings.Replace(raw, `"marginal_counts":[2,2]`, `"marginal_counts":[6,2]`, 1),
		strings.Replace(raw, `"marginal_counts":[2,2]`, `"marginal_counts":[2]`, 1),
		strings.Replace(raw, `"dictionaries":[[`, `"dictionaries":[[null,`, 1),
		strings.Replace(raw, `"kinds":"m"`, `"kinds":"e"`, 1),
	} {
		if bad == raw {
			t.Fatal("ineffective corrupt fixture")
		}
		_, found, err := DecodeExtendedStatisticsSnapshot(NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(bad)))
		if !found || err == nil {
			t.Fatal("accepted corrupt MCV snapshot", bad)
		}
	}
	// Non-MCV bytes retain the historical schema and field order.
	old := `{"schema":"public","name":"s","relation_schema":"public","relation_name":"t","keys":[1,3],"kinds":"d","inherited":false,"usages":[{"type_schema":"pg_catalog","type_name":"int4","typmod":-1,"collation_schema":"","collation_name":""},{"type_schema":"pg_catalog","type_name":"int4","typmod":-1,"collation_schema":"","collation_name":""}],"subsets":[[1,3]],"counts":[2]}`
	oldRecord, _, err := DecodeExtendedStatisticsSnapshot(NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(old)))
	if err != nil {
		t.Fatal(err)
	}
	oldEncoded, err := EncodeExtendedStatisticsSnapshot(oldRecord)
	if err != nil || oldEncoded != "-- "+NDistinctSnapshotMarker+" "+base64.StdEncoding.EncodeToString([]byte(old))+"\n" {
		t.Fatal("old snapshot bytes changed", err)
	}
}
