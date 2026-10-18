package engine

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

// Independent literal expectations transcribed from REL_18_0 pg_conversion.dat.
const visibilityExpectedConversions = `4402 koi8_r_to_mic
4403 mic_to_koi8_r
4404 iso_8859_5_to_mic
4405 mic_to_iso_8859_5
4406 windows_1251_to_mic
4407 mic_to_windows_1251
4408 windows_866_to_mic
4409 mic_to_windows_866
4410 koi8_r_to_windows_1251
4411 windows_1251_to_koi8_r
4412 koi8_r_to_windows_866
4413 windows_866_to_koi8_r
4414 windows_866_to_windows_1251
4415 windows_1251_to_windows_866
4416 iso_8859_5_to_koi8_r
4417 koi8_r_to_iso_8859_5
4418 iso_8859_5_to_windows_1251
4419 windows_1251_to_iso_8859_5
4420 iso_8859_5_to_windows_866
4421 windows_866_to_iso_8859_5
4422 euc_cn_to_mic
4423 mic_to_euc_cn
4424 euc_jp_to_sjis
4425 sjis_to_euc_jp
4426 euc_jp_to_mic
4427 sjis_to_mic
4428 mic_to_euc_jp
4429 mic_to_sjis
4430 euc_kr_to_mic
4431 mic_to_euc_kr
4432 euc_tw_to_big5
4433 big5_to_euc_tw
4434 euc_tw_to_mic
4435 big5_to_mic
4436 mic_to_euc_tw
4437 mic_to_big5
4438 iso_8859_2_to_mic
4439 mic_to_iso_8859_2
4440 windows_1250_to_mic
4441 mic_to_windows_1250
4442 iso_8859_2_to_windows_1250
4443 windows_1250_to_iso_8859_2
4444 iso_8859_1_to_mic
4445 mic_to_iso_8859_1
4446 iso_8859_3_to_mic
4447 mic_to_iso_8859_3
4448 iso_8859_4_to_mic
4449 mic_to_iso_8859_4
4452 big5_to_utf8
4453 utf8_to_big5
4454 utf8_to_koi8_r
4455 koi8_r_to_utf8
4456 utf8_to_koi8_u
4457 koi8_u_to_utf8
4458 utf8_to_windows_866
4459 windows_866_to_utf8
4460 utf8_to_windows_874
4461 windows_874_to_utf8
4462 utf8_to_windows_1250
4463 windows_1250_to_utf8
4464 utf8_to_windows_1251
4465 windows_1251_to_utf8
4466 utf8_to_windows_1252
4467 windows_1252_to_utf8
4468 utf8_to_windows_1253
4469 windows_1253_to_utf8
4470 utf8_to_windows_1254
4471 windows_1254_to_utf8
4472 utf8_to_windows_1255
4473 windows_1255_to_utf8
4474 utf8_to_windows_1256
4475 windows_1256_to_utf8
4476 utf8_to_windows_1257
4477 windows_1257_to_utf8
4478 utf8_to_windows_1258
4479 windows_1258_to_utf8
4480 euc_cn_to_utf8
4481 utf8_to_euc_cn
4482 euc_jp_to_utf8
4483 utf8_to_euc_jp
4484 euc_kr_to_utf8
4485 utf8_to_euc_kr
4486 euc_tw_to_utf8
4487 utf8_to_euc_tw
4488 gb18030_to_utf8
4489 utf8_to_gb18030
4490 gbk_to_utf8
4491 utf8_to_gbk
4492 utf8_to_iso_8859_2
4493 iso_8859_2_to_utf8
4494 utf8_to_iso_8859_3
4495 iso_8859_3_to_utf8
4496 utf8_to_iso_8859_4
4497 iso_8859_4_to_utf8
4498 utf8_to_iso_8859_9
4499 iso_8859_9_to_utf8
4500 utf8_to_iso_8859_10
4501 iso_8859_10_to_utf8
4502 utf8_to_iso_8859_13
4503 iso_8859_13_to_utf8
4504 utf8_to_iso_8859_14
4505 iso_8859_14_to_utf8
4506 utf8_to_iso_8859_15
4507 iso_8859_15_to_utf8
4508 utf8_to_iso_8859_16
4509 iso_8859_16_to_utf8
4510 utf8_to_iso_8859_5
4511 iso_8859_5_to_utf8
4512 utf8_to_iso_8859_6
4513 iso_8859_6_to_utf8
4514 utf8_to_iso_8859_7
4515 iso_8859_7_to_utf8
4516 utf8_to_iso_8859_8
4517 iso_8859_8_to_utf8
4518 iso_8859_1_to_utf8
4519 utf8_to_iso_8859_1
4520 johab_to_utf8
4521 utf8_to_johab
4522 sjis_to_utf8
4523 utf8_to_sjis
4524 uhc_to_utf8
4525 utf8_to_uhc
4526 euc_jis_2004_to_utf8
4527 utf8_to_euc_jis_2004
4528 shift_jis_2004_to_utf8
4529 utf8_to_shift_jis_2004
4530 euc_jis_2004_to_shift_jis_2004
4531 shift_jis_2004_to_euc_jis_2004`

func TestVisibilityInquiryReleaseNames(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, entry := range strings.Split(visibilityExpectedConversions, "\n") {
		fields := strings.Fields(entry)
		id, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		address := ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: ids.OID(id)}
		d, ok := view.Descriptor(address)
		if !ok || d.Name() != fields[1] || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.Revision() != 1 || d.Kind() != ids.ConversionObject {
			t.Fatal(entry, d, ok)
		}
	}
	for _, entry := range []struct {
		id       ids.OID
		name     string
		encoding int32
	}{{100, "default", -1}, {950, "C", -1}, {951, "POSIX", -1}, {962, "ucs_basic", 6}, {963, "unicode", -1}, {811, "pg_c_utf8", 6}, {6411, "pg_unicode_fast", 6}} {
		d, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: entry.id})
		if !ok || d.name != entry.name || d.encoding != entry.encoding {
			t.Fatal(entry, d, ok)
		}
	}
}
