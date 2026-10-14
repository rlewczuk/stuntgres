package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitStringFunctionsMeasurements(t *testing.T) {
	bitStringFunctionsScenario(t, bitStringFunctionsMeasurements)
}

func bitStringFunctionsMeasurements(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	bitStringFunctionsBoth(t, p, b, c, bitStringFunctionsManualStep())
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE bsf_measure(id INTEGER,f BIT(3),v VARBIT,u "bit")`, 0))
	run(fixture.Exec("INSERT INTO bsf_measure VALUES(1,B'101',B'',B''),(2,B'000',B'1',B'1'),(3,B'111',B'0000000',B'0000000'),(4,B'001',X'0f',X'0f'),(5,B'100',B'100000001',B'100000001'),(6,B'010',B'1111111111111111',B'1111111111111111'),(7,B'011',B'10000000000000001',B'10000000000000001'),(8,NULL,NULL,NULL)", 8))
	want := [][]any{
		{int64(1), int64(2), int64(3), int64(0), int64(0), int64(0), int64(0)},
		{int64(2), int64(0), int64(3), int64(1), int64(1), int64(1), int64(1)},
		{int64(3), int64(3), int64(3), int64(0), int64(7), int64(7), int64(1)},
		{int64(4), int64(1), int64(3), int64(4), int64(8), int64(8), int64(1)},
		{int64(5), int64(1), int64(3), int64(2), int64(9), int64(9), int64(2)},
		{int64(6), int64(1), int64(3), int64(16), int64(16), int64(16), int64(2)},
		{int64(7), int64(2), int64(3), int64(2), int64(17), int64(17), int64(3)},
		{int64(8), nil, nil, nil, nil, nil, nil},
	}
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT id,bit_count(f) AS fc,length(f) AS fl,bit_count(v) AS vc,bit_length(v) AS vb,length(u) AS ul,octet_length(u) AS uo FROM bsf_measure ORDER BY id", []string{"id", "fc", "fl", "vc", "vb", "ul", "uo"}, []string{"INT4", "INT8", "INT4", "INT8", "INT4", "INT4", "INT4"}, want))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT bit_count(overlay(v PLACING $1 FROM 1 FOR 0)) AS c FROM bsf_measure WHERE id=5", []string{"c"}, []string{"INT8"}, [][]any{{int64(4)}}, "11"))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT v,u FROM bsf_measure WHERE id=5", []string{"v", "u"}, []string{"VARBIT", "BIT"}, [][]any{{"100000001", "100000001"}}))
}
