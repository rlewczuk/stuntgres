package storage

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestStatisticsDependenciesSnapshot(t *testing.T) {
	items := []DependencySnapshot{{Attributes: []int16{1, 3}, Degree: .25}, {Attributes: []int16{3, 1}, Degree: .5}}
	record := ExtendedStatisticsSnapshot{Schema: "public", Name: "s", RelationSchema: "public", RelationName: "t", Keys: []int32{1, 3}, Kinds: "f", Usages: []NDistinctUsageRecord{{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}, {TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}}, Subsets: [][]int16{}, Counts: []uint64{}, Dependencies: &items}
	encoded, err := EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(encoded, "-- "))
	if err != nil || !recognized || (*decoded.Dependencies)[0].Degree != .25 || (*decoded.Dependencies)[1].Degree != .5 {
		t.Fatal(decoded, recognized, err)
	}
	_, payload, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(encoded, "-- ")), " ")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		strings.Replace(string(raw), `,"dependencies":[{"attributes":[1,3],"degree":0.25},{"attributes":[3,1],"degree":0.5}]`, "", 1),
		strings.Replace(string(raw), `"dependencies":[`, `"dependencies":null,"extra":[`, 1),
		strings.Replace(string(raw), `"degree":0.25`, `"degree":0`, 1),
		strings.Replace(string(raw), `"degree":0.25`, `"degree":1.1`, 1),
		strings.Replace(string(raw), `"degree":0.25`, `"degree":null`, 1),
		strings.Replace(string(raw), `"degree":0.25`, `"degree":0.25,"degree":0.25`, 1),
		strings.Replace(string(raw), `"degree":0.25`, `"unknown":0.25`, 1),
		strings.Replace(string(raw), `"attributes":[1,3]`, `"attributes":[1,2]`, 1),
		strings.Replace(string(raw), `"attributes":[3,1]`, `"attributes":[1,3]`, 1),
		strings.Replace(string(raw), `"kinds":"f"`, `"kinds":"m"`, 1),
	} {
		if _, ok, err := DecodeExtendedStatisticsSnapshot(NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(text))); !ok || err == nil {
			t.Fatal("accepted corrupt dependencies", text)
		}
	}
	empty := []DependencySnapshot{}
	record.Dependencies = &empty
	encoded, err = EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err = DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(encoded, "-- "))
	if err != nil || decoded.Dependencies == nil || len(*decoded.Dependencies) != 0 {
		t.Fatal("analyzed NULL lost", decoded, err)
	}
}
