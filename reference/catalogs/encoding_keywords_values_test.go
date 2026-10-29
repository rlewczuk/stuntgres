package reference_test

import (
 "fmt"
 "strconv"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

// Independently transcribed from REL_18_6 encnames.c and pg_wchar.h:
// https://github.com/postgres/postgres/blob/REL_18_6/src/common/encnames.c
const encodingCanonical = "SQL_ASCII EUC_JP EUC_CN EUC_KR EUC_TW EUC_JIS_2004 UTF8 MULE_INTERNAL LATIN1 LATIN2 LATIN3 LATIN4 LATIN5 LATIN6 LATIN7 LATIN8 LATIN9 LATIN10 WIN1256 WIN1258 WIN866 WIN874 KOI8R WIN1251 WIN1252 ISO_8859_5 ISO_8859_6 ISO_8859_7 ISO_8859_8 WIN1250 WIN1253 WIN1254 WIN1255 WIN1257 KOI8U SJIS BIG5 GBK UHC GB18030 JOHAB SHIFT_JIS_2004"
const encodingAliases = `abc 19
alt 20
big5 36
euccn 2
eucjis2004 5
eucjp 1
euckr 3
euctw 4
gb18030 39
gbk 37
iso88591 8
iso885910 13
iso885913 14
iso885914 15
iso885915 16
iso885916 17
iso88592 9
iso88593 10
iso88594 11
iso88595 25
iso88596 26
iso88597 27
iso88598 28
iso88599 12
johab 40
koi8 22
koi8r 22
koi8u 34
latin1 8
latin10 17
latin2 9
latin3 10
latin4 11
latin5 12
latin6 13
latin7 14
latin8 15
latin9 16
mskanji 35
muleinternal 7
shiftjis 35
shiftjis2004 41
sjis 35
sqlascii 0
tcvn 19
tcvn5712 19
uhc 38
unicode 6
utf8 6
vscii 19
win 23
win1250 29
win1251 23
win1252 24
win1253 30
win1254 31
win1255 32
win1256 18
win1257 33
win1258 19
win866 20
win874 21
win932 35
win936 37
win949 38
win950 36
windows1250 29
windows1251 23
windows1252 24
windows1253 30
windows1254 31
windows1255 32
windows1256 18
windows1257 33
windows1258 19
windows866 20
windows874 21
windows932 35
windows936 37
windows949 38
windows950 36`

func TestReferenceEncodingKeywordsValues(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, prepared := range []bool{false,true} {
  var tuples []string
  var want [][]any
  for id, name := range strings.Fields(encodingCanonical) {
   tuples = append(tuples,fmt.Sprintf("(%d,'%s'::name)",id,name))
   want = append(want,[]any{int64(id),name,int64(id),name})
  }
  s := fixture.Select("SELECT id,encoding,pg_char_to_encoding(encoding) AS actual_id,pg_encoding_to_char(id) AS actual_name FROM (VALUES "+strings.Join(tuples,",")+") AS e(id,encoding) ORDER BY id", []string{"id","encoding","actual_id","actual_name"}, []string{"INT4","NAME","INT4","NAME"},want)
  s.Prepared = prepared
  p.Step(t,s)
  tuples, want = nil,nil
  for _, line := range strings.Split(encodingAliases,"\n") {
   fields := strings.Fields(line)
   id,err := strconv.Atoi(fields[1]); if err != nil { t.Fatal(err) }
   tuples = append(tuples,"('"+fields[0]+"'::name)")
   want = append(want,[]any{fields[0],int64(id)})
  }
  s = fixture.Select("SELECT encoding,pg_char_to_encoding(encoding) AS id FROM (VALUES "+strings.Join(tuples,",")+") AS e(encoding)", []string{"encoding","id"}, []string{"NAME","INT4"},want)
  s.Prepared,s.Bag = prepared,true
  p.Step(t,s)
  for _, c := range []struct { input string; id int64 }{
   {"",-1},{"not-an-encoding",-1},{"---",-1},{"UTF-8",6},{"u.t_f-8",6},{"Unicode",6},
   {"UTF8"+strings.Repeat("-",60),6},{strings.Repeat("-",64)+"UTF8",-1},
   // 59 ASCII bytes plus a 3-byte rune: the next rune crosses byte63.
   {"UTF8"+strings.Repeat("-",55)+"界界",6},
   // NAME keeps59 hyphens, one three-byte rune and U:63 bytes. Raw
   // encoding lookup would incorrectly retain the entire UTF8 suffix.
   {strings.Repeat("-",59)+"界UTF8",-1},
  } {
   s = fixture.Select("SELECT pg_char_to_encoding($1) AS id",[]string{"id"},[]string{"INT4"},[][]any{{c.id}},c.input)
   s.Prepared = prepared; p.Step(t,s)
  }
  s = fixture.Select("SELECT $1::text::name AS clipped,pg_char_to_encoding($1::text) AS id",[]string{"clipped","id"},[]string{"NAME","INT4"},[][]any{{strings.Repeat("-",59)+"界U",int64(-1)}},strings.Repeat("-",59)+"界UTF8")
  s.Prepared = prepared; p.Step(t,s)
  s = fixture.Select("SELECT pg_char_to_encoding('UTF8'::text) AS text_id,pg_char_to_encoding('UTF8'::varchar) AS varchar_id,pg_char_to_encoding('UTF8'::name) AS name_id,pg_encoding_to_char(6::smallint) AS smallint_name",[]string{"text_id","varchar_id","name_id","smallint_name"},[]string{"INT4","INT4","INT4","NAME"},[][]any{{int64(6),int64(6),int64(6),"UTF8"}})
  s.Prepared = prepared; p.Step(t,s)
  for _, id := range []int64{-1,42,-2147483648,2147483647} {
   s = fixture.Select("SELECT pg_encoding_to_char($1) AS encoding",[]string{"encoding"},[]string{"NAME"},[][]any{{""}},id)
   s.Prepared = prepared; p.Step(t,s)
  }
  s = fixture.Select("SELECT pg_char_to_encoding(NULL) AS id,pg_encoding_to_char(NULL) AS encoding,pg_encoding_to_char(pg_char_to_encoding('UTF8')) AS roundtrip",[]string{"id","encoding","roundtrip"},[]string{"INT4","NAME","NAME"},[][]any{{nil,nil,"UTF8"}})
  s.Prepared = prepared; p.Step(t,s)
 }
}
