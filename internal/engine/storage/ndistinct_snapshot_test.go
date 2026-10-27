package storage

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestStatisticsNDistinctImportCodec(t *testing.T) {
	record := ExtendedStatisticsSnapshot{Schema: "public", Name: "s", RelationSchema: "public", RelationName: "t", Keys: []int32{1, 3}, Kinds: "d", Usages: []NDistinctUsageRecord{{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}, {TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}}, Subsets: [][]int16{{1, 3}}, Counts: []uint64{2}}
	encoded, err := EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(encoded, "-- "))
	if err != nil || !recognized || decoded.Counts[0] != 2 || decoded.Keys[1] != 3 {
		t.Fatal(decoded, recognized, err)
	}
	_, payload, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(encoded, "-- ")), " ")
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(text string) string {
		return NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(text))
	}
	for _, text := range []string{
		strings.Replace(string(raw), `"counts":[2]`, `"counts":null`, 1),
		strings.Replace(string(raw), `"counts":[2]`, `"counts":[0]`, 1),
		strings.Replace(string(raw), `"counts":[2]`, `"counts":[1.5]`, 1),
		strings.Replace(string(raw), `"counts":[2]`, `"counts":[2147483648]`, 1),
		strings.Replace(string(raw), `"counts":[2]`, `"counts":[2],"counts":[3]`, 1),
		strings.Replace(string(raw), `"subsets":[[1,3]]`, `"subsets":[[3,1]]`, 1),
		strings.Replace(string(raw), `"inherited":false,`, "", 1),
		strings.Replace(string(raw), `"typmod":-1`, `"typmod":-1,"typmod":-1`, 1),
		strings.Replace(string(raw), `"type_name":"int4"`, `"type_name":null`, 1),
		strings.Replace(string(raw), `"typmod":-1`, `"unknown":-1`, 1),
		string(raw) + ` {}`,
	} {
		if _, recognized, err := DecodeExtendedStatisticsSnapshot(mutate(text)); !recognized || err == nil {
			t.Fatal("accepted corrupt snapshot", text, recognized, err)
		}
	}
	for _, text := range []string{"stuntgres-ndistinct-v2 e30=", NDistinctSnapshotMarker + " !!!", NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte{255})} {
		if _, recognized, err := DecodeExtendedStatisticsSnapshot(text); !recognized || err == nil {
			t.Fatal(text, recognized, err)
		}
	}
	if _, recognized, err := DecodeExtendedStatisticsSnapshot("ordinary comment"); recognized || err != nil {
		t.Fatal("ordinary comment gained authority")
	}
}
