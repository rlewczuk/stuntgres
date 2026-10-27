package typedef

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// BuiltinConversionCount is the number of builtin encoding conversion routines.
const BuiltinConversionCount = len(builtinConversionRoutines)

// REL_18_6 pg_proc.dat owns both the immutable symbol and library provenance.
var builtinConversionRoutines = [...]struct {
	oid    ids.OID
	name   string
	binary string
}{
	{4302, "koi8r_to_mic", "$libdir/cyrillic_and_mic"},
	{4303, "mic_to_koi8r", "$libdir/cyrillic_and_mic"},
	{4304, "iso_to_mic", "$libdir/cyrillic_and_mic"},
	{4305, "mic_to_iso", "$libdir/cyrillic_and_mic"},
	{4306, "win1251_to_mic", "$libdir/cyrillic_and_mic"},
	{4307, "mic_to_win1251", "$libdir/cyrillic_and_mic"},
	{4308, "win866_to_mic", "$libdir/cyrillic_and_mic"},
	{4309, "mic_to_win866", "$libdir/cyrillic_and_mic"},
	{4310, "koi8r_to_win1251", "$libdir/cyrillic_and_mic"},
	{4311, "win1251_to_koi8r", "$libdir/cyrillic_and_mic"},
	{4312, "koi8r_to_win866", "$libdir/cyrillic_and_mic"},
	{4313, "win866_to_koi8r", "$libdir/cyrillic_and_mic"},
	{4314, "win866_to_win1251", "$libdir/cyrillic_and_mic"},
	{4315, "win1251_to_win866", "$libdir/cyrillic_and_mic"},
	{4316, "iso_to_koi8r", "$libdir/cyrillic_and_mic"},
	{4317, "koi8r_to_iso", "$libdir/cyrillic_and_mic"},
	{4318, "iso_to_win1251", "$libdir/cyrillic_and_mic"},
	{4319, "win1251_to_iso", "$libdir/cyrillic_and_mic"},
	{4320, "iso_to_win866", "$libdir/cyrillic_and_mic"},
	{4321, "win866_to_iso", "$libdir/cyrillic_and_mic"},
	{4322, "euc_cn_to_mic", "$libdir/euc_cn_and_mic"},
	{4323, "mic_to_euc_cn", "$libdir/euc_cn_and_mic"},
	{4324, "euc_jp_to_sjis", "$libdir/euc_jp_and_sjis"},
	{4325, "sjis_to_euc_jp", "$libdir/euc_jp_and_sjis"},
	{4326, "euc_jp_to_mic", "$libdir/euc_jp_and_sjis"},
	{4327, "sjis_to_mic", "$libdir/euc_jp_and_sjis"},
	{4328, "mic_to_euc_jp", "$libdir/euc_jp_and_sjis"},
	{4329, "mic_to_sjis", "$libdir/euc_jp_and_sjis"},
	{4330, "euc_kr_to_mic", "$libdir/euc_kr_and_mic"},
	{4331, "mic_to_euc_kr", "$libdir/euc_kr_and_mic"},
	{4332, "euc_tw_to_big5", "$libdir/euc_tw_and_big5"},
	{4333, "big5_to_euc_tw", "$libdir/euc_tw_and_big5"},
	{4334, "euc_tw_to_mic", "$libdir/euc_tw_and_big5"},
	{4335, "big5_to_mic", "$libdir/euc_tw_and_big5"},
	{4336, "mic_to_euc_tw", "$libdir/euc_tw_and_big5"},
	{4337, "mic_to_big5", "$libdir/euc_tw_and_big5"},
	{4338, "latin2_to_mic", "$libdir/latin2_and_win1250"},
	{4339, "mic_to_latin2", "$libdir/latin2_and_win1250"},
	{4340, "win1250_to_mic", "$libdir/latin2_and_win1250"},
	{4341, "mic_to_win1250", "$libdir/latin2_and_win1250"},
	{4342, "latin2_to_win1250", "$libdir/latin2_and_win1250"},
	{4343, "win1250_to_latin2", "$libdir/latin2_and_win1250"},
	{4344, "latin1_to_mic", "$libdir/latin_and_mic"},
	{4345, "mic_to_latin1", "$libdir/latin_and_mic"},
	{4346, "latin3_to_mic", "$libdir/latin_and_mic"},
	{4347, "mic_to_latin3", "$libdir/latin_and_mic"},
	{4348, "latin4_to_mic", "$libdir/latin_and_mic"},
	{4349, "mic_to_latin4", "$libdir/latin_and_mic"},
	{4352, "big5_to_utf8", "$libdir/utf8_and_big5"},
	{4353, "utf8_to_big5", "$libdir/utf8_and_big5"},
	{4354, "utf8_to_koi8r", "$libdir/utf8_and_cyrillic"},
	{4355, "koi8r_to_utf8", "$libdir/utf8_and_cyrillic"},
	{4356, "utf8_to_koi8u", "$libdir/utf8_and_cyrillic"},
	{4357, "koi8u_to_utf8", "$libdir/utf8_and_cyrillic"},
	{4358, "utf8_to_win", "$libdir/utf8_and_win"},
	{4359, "win_to_utf8", "$libdir/utf8_and_win"},
	{4360, "euc_cn_to_utf8", "$libdir/utf8_and_euc_cn"},
	{4361, "utf8_to_euc_cn", "$libdir/utf8_and_euc_cn"},
	{4362, "euc_jp_to_utf8", "$libdir/utf8_and_euc_jp"},
	{4363, "utf8_to_euc_jp", "$libdir/utf8_and_euc_jp"},
	{4364, "euc_kr_to_utf8", "$libdir/utf8_and_euc_kr"},
	{4365, "utf8_to_euc_kr", "$libdir/utf8_and_euc_kr"},
	{4366, "euc_tw_to_utf8", "$libdir/utf8_and_euc_tw"},
	{4367, "utf8_to_euc_tw", "$libdir/utf8_and_euc_tw"},
	{4368, "gb18030_to_utf8", "$libdir/utf8_and_gb18030"},
	{4369, "utf8_to_gb18030", "$libdir/utf8_and_gb18030"},
	{4370, "gbk_to_utf8", "$libdir/utf8_and_gbk"},
	{4371, "utf8_to_gbk", "$libdir/utf8_and_gbk"},
	{4372, "utf8_to_iso8859", "$libdir/utf8_and_iso8859"},
	{4373, "iso8859_to_utf8", "$libdir/utf8_and_iso8859"},
	{4374, "iso8859_1_to_utf8", "$libdir/utf8_and_iso8859_1"},
	{4375, "utf8_to_iso8859_1", "$libdir/utf8_and_iso8859_1"},
	{4376, "johab_to_utf8", "$libdir/utf8_and_johab"},
	{4377, "utf8_to_johab", "$libdir/utf8_and_johab"},
	{4378, "sjis_to_utf8", "$libdir/utf8_and_sjis"},
	{4379, "utf8_to_sjis", "$libdir/utf8_and_sjis"},
	{4380, "uhc_to_utf8", "$libdir/utf8_and_uhc"},
	{4381, "utf8_to_uhc", "$libdir/utf8_and_uhc"},
	{4382, "euc_jis_2004_to_utf8", "$libdir/utf8_and_euc2004"},
	{4383, "utf8_to_euc_jis_2004", "$libdir/utf8_and_euc2004"},
	{4384, "shift_jis_2004_to_utf8", "$libdir/utf8_and_sjis2004"},
	{4385, "utf8_to_shift_jis_2004", "$libdir/utf8_and_sjis2004"},
	{4386, "euc_jis_2004_to_shift_jis_2004", "$libdir/euc2004_sjis2004"},
	{4387, "shift_jis_2004_to_euc_jis_2004", "$libdir/euc2004_sjis2004"},
}

func ConversionRoutineName(oid ids.OID) string {
	if oid < 4302 || oid > 4387 || oid == 4350 || oid == 4351 {
		return ""
	}
	index := int(oid - 4302)
	if oid >= 4352 {
		index -= 2
	}
	return builtinConversionRoutines[index].name
}

// BuiltinConversionProcedures returns fresh descriptors for the builtin
// encoding conversion routines, owned by owner.
func BuiltinConversionProcedures(owner authorization.PrincipalRef) []catalog.Procedure {
	procedures := make([]catalog.Procedure, len(builtinConversionRoutines))
	for i, routine := range builtinConversionRoutines {
		provider, ok := sqltype.RegisteredEncodingConversion(routine.name)
		if !ok {
			panic("stuntgres: missing released encoding conversion provider")
		}
		procedures[i] = catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: routine.oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: routine.name, Signature: catalog.ConversionSignature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 23, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: routine.name, Binary: routine.binary, HasBinary: true}, Conversion: provider}
	}
	return procedures
}
