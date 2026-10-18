package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var builtinConversionRoutines = [...]struct {
	oid  ids.OID
	name string
}{
	{4302, "koi8r_to_mic"},
	{4303, "mic_to_koi8r"},
	{4304, "iso_to_mic"},
	{4305, "mic_to_iso"},
	{4306, "win1251_to_mic"},
	{4307, "mic_to_win1251"},
	{4308, "win866_to_mic"},
	{4309, "mic_to_win866"},
	{4310, "koi8r_to_win1251"},
	{4311, "win1251_to_koi8r"},
	{4312, "koi8r_to_win866"},
	{4313, "win866_to_koi8r"},
	{4314, "win866_to_win1251"},
	{4315, "win1251_to_win866"},
	{4316, "iso_to_koi8r"},
	{4317, "koi8r_to_iso"},
	{4318, "iso_to_win1251"},
	{4319, "win1251_to_iso"},
	{4320, "iso_to_win866"},
	{4321, "win866_to_iso"},
	{4322, "euc_cn_to_mic"},
	{4323, "mic_to_euc_cn"},
	{4324, "euc_jp_to_sjis"},
	{4325, "sjis_to_euc_jp"},
	{4326, "euc_jp_to_mic"},
	{4327, "sjis_to_mic"},
	{4328, "mic_to_euc_jp"},
	{4329, "mic_to_sjis"},
	{4330, "euc_kr_to_mic"},
	{4331, "mic_to_euc_kr"},
	{4332, "euc_tw_to_big5"},
	{4333, "big5_to_euc_tw"},
	{4334, "euc_tw_to_mic"},
	{4335, "big5_to_mic"},
	{4336, "mic_to_euc_tw"},
	{4337, "mic_to_big5"},
	{4338, "latin2_to_mic"},
	{4339, "mic_to_latin2"},
	{4340, "win1250_to_mic"},
	{4341, "mic_to_win1250"},
	{4342, "latin2_to_win1250"},
	{4343, "win1250_to_latin2"},
	{4344, "latin1_to_mic"},
	{4345, "mic_to_latin1"},
	{4346, "latin3_to_mic"},
	{4347, "mic_to_latin3"},
	{4348, "latin4_to_mic"},
	{4349, "mic_to_latin4"},
	{4352, "big5_to_utf8"},
	{4353, "utf8_to_big5"},
	{4354, "utf8_to_koi8r"},
	{4355, "koi8r_to_utf8"},
	{4356, "utf8_to_koi8u"},
	{4357, "koi8u_to_utf8"},
	{4358, "utf8_to_win"},
	{4359, "win_to_utf8"},
	{4360, "euc_cn_to_utf8"},
	{4361, "utf8_to_euc_cn"},
	{4362, "euc_jp_to_utf8"},
	{4363, "utf8_to_euc_jp"},
	{4364, "euc_kr_to_utf8"},
	{4365, "utf8_to_euc_kr"},
	{4366, "euc_tw_to_utf8"},
	{4367, "utf8_to_euc_tw"},
	{4368, "gb18030_to_utf8"},
	{4369, "utf8_to_gb18030"},
	{4370, "gbk_to_utf8"},
	{4371, "utf8_to_gbk"},
	{4372, "utf8_to_iso8859"},
	{4373, "iso8859_to_utf8"},
	{4374, "iso8859_1_to_utf8"},
	{4375, "utf8_to_iso8859_1"},
	{4376, "johab_to_utf8"},
	{4377, "utf8_to_johab"},
	{4378, "sjis_to_utf8"},
	{4379, "utf8_to_sjis"},
	{4380, "uhc_to_utf8"},
	{4381, "utf8_to_uhc"},
	{4382, "euc_jis_2004_to_utf8"},
	{4383, "utf8_to_euc_jis_2004"},
	{4384, "shift_jis_2004_to_utf8"},
	{4385, "utf8_to_shift_jis_2004"},
	{4386, "euc_jis_2004_to_shift_jis_2004"},
	{4387, "shift_jis_2004_to_euc_jis_2004"},
}

func conversionRoutineName(oid ids.OID) string {
	if oid < 4302 || oid > 4387 || oid == 4350 || oid == 4351 {
		return ""
	}
	index := int(oid - 4302)
	if oid >= 4352 {
		index -= 2
	}
	return builtinConversionRoutines[index].name
}

func (e *Engine) initializeConversionRoutines() {
	for _, routine := range builtinConversionRoutines {
		provider, ok := sqltype.RegisteredEncodingConversion(routine.name)
		if !ok {
			panic("stuntgres: missing released encoding conversion provider")
		}
		d := &procedureDescriptor{address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: routine.oid}, namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, revision: 1, name: routine.name, Signature: conversionSignature, RoutineKind: functionRoutine, Owner: e.authorizationDomain.Principal(10), Result: 23, Volatility: expr.Immutable, Strict: true, Conversion: provider}
		e.catalog.state.setDescriptor(d.Address(), d)
		e.catalog.state.bindName(descriptorNameKey(d), d.Address())
	}
}
