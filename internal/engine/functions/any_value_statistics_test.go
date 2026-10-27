package functions

import (
	"encoding/binary"
	"math"
	"testing"

	"stuntgres/internal/sqltype"
)

func anyValueStatisticsDatum(t *testing.T, d *sqltype.Descriptor) sqltype.Value {
	t.Helper()
	var value sqltype.Value
	var err error
	switch d.ID() {
	case sqltype.PgNDistinct, sqltype.PgDependencies:
		countOrDegree := float64(3)
		if d.ID() == sqltype.PgDependencies {
			countOrDegree = 0.5
		}
		attributes := []int16{1, 2}
		value, err = sqltype.StatisticsValue(d, []sqltype.StatisticsItem{{Attributes: attributes, Value: countOrDegree}})
		attributes[0] = 7
	case sqltype.PgMCVList:
		// Released linux/amd64 BASIC serialization: two INT4 dimensions,
		// one distinct value 1 in each, one non-NULL frequency-1 item.
		payload := make([]byte, 92)
		binary.LittleEndian.PutUint32(payload, 0xE1A651C2)
		binary.LittleEndian.PutUint32(payload[4:], 1)
		binary.LittleEndian.PutUint32(payload[8:], 1)
		binary.LittleEndian.PutUint16(payload[12:], 2)
		binary.LittleEndian.PutUint32(payload[14:], 23)
		binary.LittleEndian.PutUint32(payload[18:], 23)
		for _, offset := range []int{22, 42} {
			binary.LittleEndian.PutUint32(payload[offset:], 1)
			binary.LittleEndian.PutUint32(payload[offset+4:], 4)
			binary.LittleEndian.PutUint32(payload[offset+8:], 0) // by-value deserialization allocates nothing
			binary.LittleEndian.PutUint32(payload[offset+12:], 4)
			payload[offset+16] = 1
		}
		binary.LittleEndian.PutUint32(payload[62:], 1)
		binary.LittleEndian.PutUint32(payload[66:], 1)
		binary.LittleEndian.PutUint64(payload[72:], math.Float64bits(1))
		binary.LittleEndian.PutUint64(payload[80:], math.Float64bits(1))
		value, err = sqltype.StatisticsMCVValue(d, payload)
		payload[0] = 0
	default:
		t.Fatal("unexpected statistics datum type", d.Name())
	}
	if err != nil || value.IsNull() || value.Type() != d {
		t.Fatal("genuine statistics carrier construction", value, err)
	}
	return value
}
