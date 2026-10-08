//go:build reference

package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var timestampBadInputs = []struct{ input, code, message, hint string }{
	{"1999 1.5 8", "22007", "invalid input syntax for type timestamp: \"1999 1.5 8\"", ""},
	{"1999 1.1 8", "22007", "invalid input syntax for type timestamp: \"1999 1.1 8\"", ""},
	{"4714-11-23 000000 BC", "22008", "timestamp out of range: \"4714-11-23 000000 BC\"", ""},
	{"J .1 04:05:06", "22007", "invalid input syntax for type timestamp: \"J .1 04:05:06\"", ""},
	{"J . 04:05:06", "22007", "invalid input syntax for type timestamp: \"J . 04:05:06\"", ""},
	{"1999-01-08 250000 PM", "22008", "date/time field value out of range: \"1999-01-08 250000 PM\"", ""},
	{"EDT 1999.008", "22007", "invalid input syntax for type timestamp: \"EDT 1999.008\"", ""},
	{"PST dst 1999.008", "22007", "invalid input syntax for type timestamp: \"PST dst 1999.008\"", ""},
	{"1999-01-08 UTC America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 UTC America/New_York\"", ""},
	{"1999-01-08 UTC America/Bogus", "22023", "time zone \"america/bogus\" not recognized", ""},
	{"1999 1 0.5", "22008", "date/time field value out of range: \"1999 1 0.5\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"1999 1 32.5", "22008", "date/time field value out of range: \"1999 1 32.5\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"294276-12-31 250000", "22008", "timestamp out of range: \"294276-12-31 250000\"", ""},
	{"1999-01-08 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 25:00:00\"", ""},
	{"1999-01-08 04:99:00", "22008", "date/time field value out of range: \"1999-01-08 04:99:00\"", ""},
	{"1999-01-08 04:05:99", "22008", "date/time field value out of range: \"1999-01-08 04:05:99\"", ""},
	{"1999-01-08 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 T 2147483648\"", ""},
	{"1999-01-08 04:05:06 T 2147483648", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 2147483648\"", ""},
	{"1999-01-08 04:05:06 T 25:00:00", "22008", "date/time field value out of range: \"1999-01-08 04:05:06 T 25:00:00\"", ""},
	{"J -2147483648", "22009", "time zone displacement out of range: \"J -2147483648\"", ""},
	{"J on", "22007", "invalid input syntax for type timestamp: \"J on\"", ""},
	{"J J2451187", "22007", "invalid input syntax for type timestamp: \"J J2451187\"", ""},
	{"J 04:05:06 2451187", "22007", "invalid input syntax for type timestamp: \"J 04:05:06 2451187\"", ""},
	{"1999-01-08 04:05:06 T 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 T 040506\"", ""},
	{"1999-01-08 T America/New_York 040506", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/New_York 040506\"", ""},
	{"1999-01-08 T America/Bogus", "22007", "invalid input syntax for type timestamp: \"1999-01-08 T America/Bogus\"", ""},
	{"1999-01-08 04:05:06 EDT dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 EDT dst\"", ""},
	{"1999-01-08 04:05:06 dst EDT", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst EDT\"", ""},
	{"1999-01-08 04:05:06 MSK dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 MSK dst\"", ""},
	{"1999-01-08 04:05:06 dst MSK", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst MSK\"", ""},
	{"1999-01-08 04:05:06 America/New_York dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 America/New_York dst\"", ""},
	{"1999-01-08 04:05:06 dst America/New_York", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst America/New_York\"", ""},
	{"1999-01-08 04:05:06 FOOBAR0 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 FOOBAR0 dst\"", ""},
	{"1999-01-08 04:05:06 dst FOOBAR0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst FOOBAR0\"", ""},
	{"1999-01-08 04:05:06 dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst\"", ""},
	{"1999-01-08 04:05:06 PST dst dst", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST dst dst\"", ""},
	{"1999-01-08 04:05:06 dst PST UTC", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 dst PST UTC\"", ""},
	{"EDT 1999-01-08", "22007", "invalid input syntax for type timestamp: \"EDT 1999-01-08\"", ""},
	{"PST dst 1999-01-08", "22007", "invalid input syntax for type timestamp: \"PST dst 1999-01-08\"", ""},
	{"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08!\"", ""},
	{"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08", "22007", "invalid input syntax for type timestamp: \"at at at at at at at at at at at at at at at at at at at at at at at at at 1999-01-08\"", ""},
	{"1999-01-08\u007f", "22007", "invalid input syntax for type timestamp: \"1999-01-08\u007f\"", ""},
	{"\u007f1999-01-08", "22007", "invalid input syntax for type timestamp: \"\u007f1999-01-08\"", ""},
	{"y 2147483648", "22008", "date/time field value out of range: \"y 2147483648\"", ""},
	{"y 1", "22007", "invalid input syntax for type timestamp: \"y 1\"", ""},
	{"y on 2147483648", "22008", "date/time field value out of range: \"y on 2147483648\"", ""},
	{"m 2147483648", "22008", "date/time field value out of range: \"m 2147483648\"", ""},
	{"m 1", "22007", "invalid input syntax for type timestamp: \"m 1\"", ""},
	{"m on 2147483648", "22008", "date/time field value out of range: \"m on 2147483648\"", ""},
	{"d 2147483648", "22008", "date/time field value out of range: \"d 2147483648\"", ""},
	{"d 1", "22007", "invalid input syntax for type timestamp: \"d 1\"", ""},
	{"d on 2147483648", "22008", "date/time field value out of range: \"d on 2147483648\"", ""},
	{"h 2147483648", "22008", "date/time field value out of range: \"h 2147483648\"", ""},
	{"h 1", "22007", "invalid input syntax for type timestamp: \"h 1\"", ""},
	{"h on 2147483648", "22008", "date/time field value out of range: \"h on 2147483648\"", ""},
	{"mm 2147483648", "22008", "date/time field value out of range: \"mm 2147483648\"", ""},
	{"mm 1", "22007", "invalid input syntax for type timestamp: \"mm 1\"", ""},
	{"mm on 2147483648", "22008", "date/time field value out of range: \"mm on 2147483648\"", ""},
	{"s 2147483648", "22008", "date/time field value out of range: \"s 2147483648\"", ""},
	{"s 1", "22007", "invalid input syntax for type timestamp: \"s 1\"", ""},
	{"s on 2147483648", "22008", "date/time field value out of range: \"s on 2147483648\"", ""},
	{"dow 2147483648", "22008", "date/time field value out of range: \"dow 2147483648\"", ""},
	{"dow 1", "22007", "invalid input syntax for type timestamp: \"dow 1\"", ""},
	{"dow on 2147483648", "22008", "date/time field value out of range: \"dow on 2147483648\"", ""},
	{"doy 2147483648", "22008", "date/time field value out of range: \"doy 2147483648\"", ""},
	{"doy 1", "22007", "invalid input syntax for type timestamp: \"doy 1\"", ""},
	{"doy on 2147483648", "22008", "date/time field value out of range: \"doy on 2147483648\"", ""},
	{"isodow 2147483648", "22008", "date/time field value out of range: \"isodow 2147483648\"", ""},
	{"isodow 1", "22007", "invalid input syntax for type timestamp: \"isodow 1\"", ""},
	{"isodow on 2147483648", "22008", "date/time field value out of range: \"isodow on 2147483648\"", ""},
	{"isoyear 2147483648", "22008", "date/time field value out of range: \"isoyear 2147483648\"", ""},
	{"isoyear 1", "22007", "invalid input syntax for type timestamp: \"isoyear 1\"", ""},
	{"isoyear on 2147483648", "22008", "date/time field value out of range: \"isoyear on 2147483648\"", ""},
	{"1999-01-08 04:05:06 + 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 + 16\"", ""},
	{"1999-01-08 04:05:06 +\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\t16\"", ""},
	{"1999-01-08 04:05:06 +\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\n16\"", ""},
	{"1999-01-08 04:05:06 +\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\r16\"", ""},
	{"1999-01-08 04:05:06 +\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\f16\"", ""},
	{"1999-01-08 04:05:06 +\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 +\u000b16\"", ""},
	{"1999-01-08 04:05:06 - 16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 - 16\"", ""},
	{"1999-01-08 04:05:06 -\t16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\t16\"", ""},
	{"1999-01-08 04:05:06 -\n16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\n16\"", ""},
	{"1999-01-08 04:05:06 -\r16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\r16\"", ""},
	{"1999-01-08 04:05:06 -\f16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\f16\"", ""},
	{"1999-01-08 04:05:06 -\u000b16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06 -\u000b16\"", ""},
	{"January 8 04:05:06 2147483648", "22008", `date/time field value out of range: "January 8 04:05:06 2147483648"`, ""},
	{"1999-01-08T2147483648", "22008", `date/time field value out of range: "1999-01-08T2147483648"`, ""},
	{"epoch 1999-01-08", "22007", `invalid input syntax for type timestamp: "epoch 1999-01-08"`, ""},
	{"1999-01-08 epoch", "22007", `invalid input syntax for type timestamp: "1999-01-08 epoch"`, ""},
	{"epoch 04:05:06", "22007", `invalid input syntax for type timestamp: "epoch 04:05:06"`, ""},
	{"04:05:06 epoch", "22007", `invalid input syntax for type timestamp: "04:05:06 epoch"`, ""},
	{"epoch UTC", "22007", `invalid input syntax for type timestamp: "epoch UTC"`, ""},
	{"UTC epoch", "22007", `invalid input syntax for type timestamp: "UTC epoch"`, ""},
	{"epoch +02", "22007", `invalid input syntax for type timestamp: "epoch +02"`, ""},
	{"+02 epoch", "22007", `invalid input syntax for type timestamp: "+02 epoch"`, ""},
	{"infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "infinity 1999-01-08"`, ""},
	{"1999-01-08 infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 infinity"`, ""},
	{"infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "infinity 04:05:06"`, ""},
	{"04:05:06 infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 infinity"`, ""},
	{"infinity UTC", "22007", `invalid input syntax for type timestamp: "infinity UTC"`, ""},
	{"UTC infinity", "22007", `invalid input syntax for type timestamp: "UTC infinity"`, ""},
	{"infinity +02", "22007", `invalid input syntax for type timestamp: "infinity +02"`, ""},
	{"+02 infinity", "22007", `invalid input syntax for type timestamp: "+02 infinity"`, ""},
	{"+infinity 1999-01-08", "22007", `invalid input syntax for type timestamp: "+infinity 1999-01-08"`, ""},
	{"1999-01-08 +infinity", "22007", `invalid input syntax for type timestamp: "1999-01-08 +infinity"`, ""},
	{"+infinity 04:05:06", "22007", `invalid input syntax for type timestamp: "+infinity 04:05:06"`, ""},
	{"04:05:06 +infinity", "22007", `invalid input syntax for type timestamp: "04:05:06 +infinity"`, ""},
	{"+infinity UTC", "22007", `invalid input syntax for type timestamp: "+infinity UTC"`, ""},
	{"UTC +infinity", "22007", `invalid input syntax for type timestamp: "UTC +infinity"`, ""},
	{"+infinity +02", "22007", `invalid input syntax for type timestamp: "+infinity +02"`, ""},
	{"+02 +infinity", "22007", `invalid input syntax for type timestamp: "+02 +infinity"`, ""},
	{"- InFiNiTy 1999-01-08", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 1999-01-08"`, ""},
	{"1999-01-08 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "1999-01-08 - InFiNiTy"`, ""},
	{"- InFiNiTy 04:05:06", "22007", `invalid input syntax for type timestamp: "- InFiNiTy 04:05:06"`, ""},
	{"04:05:06 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "04:05:06 - InFiNiTy"`, ""},
	{"- InFiNiTy UTC", "22007", `invalid input syntax for type timestamp: "- InFiNiTy UTC"`, ""},
	{"UTC - InFiNiTy", "22007", `invalid input syntax for type timestamp: "UTC - InFiNiTy"`, ""},
	{"- InFiNiTy +02", "22007", `invalid input syntax for type timestamp: "- InFiNiTy +02"`, ""},
	{"+02 - InFiNiTy", "22007", `invalid input syntax for type timestamp: "+02 - InFiNiTy"`, ""},
	{"epoch epoch", "22007", `invalid input syntax for type timestamp: "epoch epoch"`, ""},
	{"infinity -infinity", "22007", `invalid input syntax for type timestamp: "infinity -infinity"`, ""},
	{"on epoch at nonsense", "22007", `invalid input syntax for type timestamp: "on epoch at nonsense"`, ""},
	{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`, ""},
	{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`, ""},
	{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`, ""},
	{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`, ""},
	{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`, ""},
	{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`, ""},
	{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`, ""},
	{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`, ""},
	{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`, ""},
	{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`, ""},
	{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`, ""},
	{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`, ""},
	{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`, ""},
	{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`, ""},
	{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`, ""},
	{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`, ""},
	{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`, ""},
	{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`, ""},
	{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`, ""},
	{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`, ""},
	{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`, ""},
	{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`, ""},
	{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`, ""},
	{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`, ""},
	{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`, ""},
	{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`, ""},
	{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`, ""},
	{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`, ""},
	{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`, ""},
	{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`, ""},
	{"2004-10-19 10:23:54+16:00", "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`, ""},
	{"2004-10-19 10:23:54-16:00", "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`, ""},
	{"January 1999-08", "22008", `date/time field value out of range: "January 1999-08"`, `Perhaps you need a different "DateStyle" setting.`},
	{"Jan 1999/08", "22008", `date/time field value out of range: "Jan 1999/08"`, `Perhaps you need a different "DateStyle" setting.`},
	{"2147483648-01-01 00:00:00+16:00", "22008", `date/time field value out of range: "2147483648-01-01 00:00:00+16:00"`, ""},
	{"1999 13", "22008", `date/time field value out of range: "1999 13"`, `Perhaps you need a different "DateStyle" setting.`},
	{"1999.13", "22007", `invalid input syntax for type timestamp: "1999.13"`, ""},
	{"Jan 32", "22008", `date/time field value out of range: "Jan 32"`, `Perhaps you need a different "DateStyle" setting.`},
	{"Jan 31", "22007", `invalid input syntax for type timestamp: "Jan 31"`, ""},
	{"1999 000", "22008", `date/time field value out of range: "1999 000"`, `Perhaps you need a different "DateStyle" setting.`},
	{"1999 367 04:05:06", "22008", `date/time field value out of range: "1999 367 04:05:06"`, `Perhaps you need a different "DateStyle" setting.`},
	{"13", "22008", `date/time field value out of range: "13"`, `Perhaps you need a different "DateStyle" setting.`},
	{"January 32", "22008", `date/time field value out of range: "January 32"`, `Perhaps you need a different "DateStyle" setting.`},
	{"000", "22008", `date/time field value out of range: "000"`, ""},
	{"1999 12", "22007", `invalid input syntax for type timestamp: "1999 12"`, ""},
	{"04:05:06", "22007", `invalid input syntax for type timestamp: "04:05:06"`, ""},
	{"AD 0001-01-01", "22007", `invalid input syntax for type timestamp: "AD 0001-01-01"`, ""},
	{"AM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "AM 1999-01-08 04:05:06"`, ""},
	{"weds 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "weds 1999-01-08 04:05:06"`, ""},
	{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`, ""},
	{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`, ""},
	{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`, ""},
	{"Friday 1999.008 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999.008 04:05:06"`, ""},
	{"BC 0001.001", "22007", `invalid input syntax for type timestamp: "BC 0001.001"`, ""},
	{"PM 1999.008 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999.008 04:05:06"`, ""},
	{"1999 01-08-1999", "22007", `invalid input syntax for type timestamp: "1999 01-08-1999"`, ""},
	{"1999.000", "22007", `invalid input syntax for type timestamp: "1999.000"`, ""},
	{"1999.367", "22007", `invalid input syntax for type timestamp: "1999.367"`, ""},
	{"1999 367", "22008", `date/time field value out of range: "1999 367"`, `Perhaps you need a different "DateStyle" setting.`},
	{"294277.001", "22008", `timestamp out of range: "294277.001"`, ""},
	{"allballs 1999-01-08", "22007", `invalid input syntax for type timestamp: "allballs 1999-01-08"`, ""},
	{"04:05:06 1999-01-08", "22007", `invalid input syntax for type timestamp: "04:05:06 1999-01-08"`, ""},
	{"allballs 1999.008", "22007", `invalid input syntax for type timestamp: "allballs 1999.008"`, ""},
	{"04:05:06 1999.008", "22007", `invalid input syntax for type timestamp: "04:05:06 1999.008"`, ""},
	{"1999-01-08 04:05:06+00:00:60", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+00:00:60"`, ""},
	{"1999-01-08 04:05:06 A0:00:61", "22023", `time zone "a0:00:61" not recognized`, ""},
	{"J2451187.0 04:05:06", "22007", `invalid input syntax for type timestamp: "J2451187.0 04:05:06"`, ""},
	{"04:05:06 J2451187.0", "22007", `invalid input syntax for type timestamp: "04:05:06 J2451187.0"`, ""},
	{"J2451187.1 UTC UTC", "22007", `invalid input syntax for type timestamp: "J2451187.1 UTC UTC"`, ""},
	{"J2451187.", "22007", `invalid input syntax for type timestamp: "J2451187."`, ""},
	{"1999-01-08 040506.", "22007", `invalid input syntax for type timestamp: "1999-01-08 040506."`, ""},
	{"1999-01-08 04:05:06..1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06..1"`, ""},
	{"1999-01-08 04:05:06.nonsense", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06.nonsense"`, ""},
	{"2147483648-01-01", "22008", `date/time field value out of range: "2147483648-01-01"`, ""},
	{"1999-2147483648-08", "22008", `date/time field value out of range: "1999-2147483648-08"`, ""},
	{"1999-01-2147483648", "22008", `date/time field value out of range: "1999-01-2147483648"`, ""},
	{"J2147483648", "22008", `date/time field value out of range: "J2147483648"`, ""},
	{"1999-01-08 2147483648:00:00", "22008", `date/time field value out of range: "1999-01-08 2147483648:00:00"`, ""},
	{"1999-01-08 04:2147483648:06", "22008", `date/time field value out of range: "1999-01-08 04:2147483648:06"`, ""},
	{"1999-01-08 04:05:2147483648", "22008", `date/time field value out of range: "1999-01-08 04:05:2147483648"`, ""},
	{"1999-01-08 9223372036854775808:00:00", "22008", `date/time field value out of range: "1999-01-08 9223372036854775808:00:00"`, ""},
	{"1999-01-08 2147483648:00:00..1", "22007", `invalid input syntax for type timestamp: "1999-01-08 2147483648:00:00..1"`, ""},
	{"1999-01-08 04:05:2147483648..1", "22008", `date/time field value out of range: "1999-01-08 04:05:2147483648..1"`, ""},
	{"1999-01-08 04:05:06+2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+2147483648"`, ""},
	{"1999-01-08 04:05:06+02:2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+02:2147483648"`, ""},
	{"1999-01-08 04:05:06+02:30:2147483648", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+02:30:2147483648"`, ""},
	{"1999-01-08 04:05:06+16.0", "22009", `time zone displacement out of range: "1999-01-08 04:05:06+16.0"`, ""},
	{"1999-01-08 04:05:06+02.0", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06+02.0"`, ""},
	{"now UTC", "22007", `invalid input syntax for type timestamp: "now UTC"`, ""},
	{"UTC now", "22007", `invalid input syntax for type timestamp: "UTC now"`, ""},
	{"now +02", "22007", `invalid input syntax for type timestamp: "now +02"`, ""},
	{"+02 now", "22007", `invalid input syntax for type timestamp: "+02 now"`, ""},
	{"now GMT", "22007", `invalid input syntax for type timestamp: "now GMT"`, ""},
	{"now America/New_York", "22007", `invalid input syntax for type timestamp: "now America/New_York"`, ""},
	{"America/New_York now", "22007", `invalid input syntax for type timestamp: "America/New_York now"`, ""},
	{"1999-01-08 allballs UTC", "22007", `invalid input syntax for type timestamp: "1999-01-08 allballs UTC"`, ""},
	{"1999-01-08 UTC allballs", "22007", `invalid input syntax for type timestamp: "1999-01-08 UTC allballs"`, ""},
	{"1999-01-08 allballs +02", "22007", `invalid input syntax for type timestamp: "1999-01-08 allballs +02"`, ""},
	{"1999-01-08 +02 allballs", "22007", `invalid input syntax for type timestamp: "1999-01-08 +02 allballs"`, ""},
	{"allballs UTC 1999-01-08", "22007", `invalid input syntax for type timestamp: "allballs UTC 1999-01-08"`, ""},
	{"UTC allballs 1999-01-08", "22007", `invalid input syntax for type timestamp: "UTC allballs 1999-01-08"`, ""},
	{"January 8 040506-8 1999", "22007", `invalid input syntax for type timestamp: "January 8 040506-8 1999"`, ""},
	{"y2001m2d4", "22007", `invalid input syntax for type timestamp: "y2001m2d4"`, ""},
	{"2001-02-04 m2", "22007", `invalid input syntax for type timestamp: "2001-02-04 m2"`, ""},
	{"2001-02-04 d4", "22007", `invalid input syntax for type timestamp: "2001-02-04 d4"`, ""},
	{"2001-02-04 h4", "22007", `invalid input syntax for type timestamp: "2001-02-04 h4"`, ""},
	{"2001-02-04 mm5", "22007", `invalid input syntax for type timestamp: "2001-02-04 mm5"`, ""},
	{"2001-02-04 s6", "22007", `invalid input syntax for type timestamp: "2001-02-04 s6"`, ""},
	{"nonsense", "22007", "invalid input syntax for type timestamp: \"nonsense\"", ""},
	{"2023-02-29 12:00:00", "22008", "date/time field value out of range: \"2023-02-29 12:00:00\"", ""},
	{"1900-02-29", "22008", "date/time field value out of range: \"1900-02-29\"", ""},
	{"2024-13-01 12:00:00", "22008", "date/time field value out of range: \"2024-13-01 12:00:00\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"2024-01-01 25:00:00", "22008", "date/time field value out of range: \"2024-01-01 25:00:00\"", ""},
	{"2024-01-01 12:60:00", "22008", "date/time field value out of range: \"2024-01-01 12:60:00\"", ""},
	{"2024-01-01 24:00:00.000001", "22008", "date/time field value out of range: \"2024-01-01 24:00:00.000001\"", ""},
	{"0000-01-01", "22008", "date/time field value out of range: \"0000-01-01\"", ""},
	{"294277-01-01 00:00:00", "22008", "timestamp out of range: \"294277-01-01 00:00:00\"", ""},
	{"4714-11-23 BC", "22008", "timestamp out of range: \"4714-11-23 BC\"", ""},
	{"99-Jan-08", "22008", "date/time field value out of range: \"99-Jan-08\"", "Perhaps you need a different \"DateStyle\" setting."},
	{"allballs", "22007", "invalid input syntax for type timestamp: \"allballs\"", ""},
	{"1999-01-08 13:00 PM", "22008", "date/time field value out of range: \"1999-01-08 13:00 PM\"", ""},
	{"1999-01-08 04:05:06+16", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06+16\"", ""},
	{"1999-01-08 04:05:06+02:60", "22009", "time zone displacement out of range: \"1999-01-08 04:05:06+02:60\"", ""},
	{"1999-01-08 04:05:06 nonsense", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 nonsense\"", ""},
	{"1999-01-08 04:05:06 America/No_Such_Zone", "22023", "time zone \"america/no_such_zone\" not recognized", ""},
	{"1999-01-08 04:05:06 PST UTC", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST UTC\"", ""},
	{"1999-01-08 04:05:06 PST8PDT,M3.2.0,M11.1.0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 PST8PDT,M3.2.0,M11.1.0\"", ""},
	{"1999-01-08 04:05:06 <FOO>0", "22007", "invalid input syntax for type timestamp: \"1999-01-08 04:05:06 <FOO>0\"", ""},
	{"1999-01-08 04:05:06 FOOBAR999", "22023", "time zone \"foobar999\" not recognized", ""},
}

func TestReferenceTimestampDiagnosticsAndAtomicity(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP NOT NULL)", 0))
	for _, tc := range timestampBadInputs {
		q := "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t"
		position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
		step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position})
		step.selectRows = true
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	unchanged := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
	for _, tc := range timestampBadInputs {
		t.Run(tc.input, func(t *testing.T) {
			for _, q := range []string{"INSERT INTO t VALUES ('" + tc.input + "')", "/*é*/ SELECT TIMESTAMP '" + tc.input + "' FROM t", "INSERT INTO t VALUES('2000-01-01'),('" + tc.input + "')"} {
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, "'"+tc.input)]) + 1)
				step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position})
				step.selectRows = strings.Contains(q, "SELECT")
				step.errorStage = "execute"
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
				p.step(t, unchanged)
			}
			for _, q := range []string{"INSERT INTO t VALUES($1)", "INSERT INTO t VALUES('2000-01-01'),($1)"} {
				step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}, tc.input)
				pg := *step.diagnostic
				pg.Context = "unnamed portal parameter $1 = '...'"
				step.diagnostic = &pg
				step.errorStage = "execute"
				p.step(t, step)
				p.step(t, referencePrepared(step))
				p.step(t, unchanged)
			}
		})
	}
	conflict := referenceImplicitError("SELECT $1 FROM t WHERE v=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp without time zone versus text", Position: "8"})
	conflict.selectRows = true
	conflict.errorStage = "prepare"
	p.step(t, referencePrepared(conflict))
	for _, tc := range []struct{ q, code, msg, stage string }{
		{"CREATE TABLE bad (v TIMESTAMP(-1))", "42601", "syntax error at or near \"-\"", "prepare"},
		{`CREATE TABLE bad (v "timestamp"(-1))`, "22023", "TIMESTAMP(-1) precision must not be negative", "execute"},
		{"CREATE TABLE bad (v TIMESTAMP(+1))", "42601", "syntax error at or near \"+\"", "prepare"},
		{"CREATE TABLE bad (v TIMESTAMP(1.5))", "42601", "syntax error at or near \"1.5\"", "prepare"},
		{"CREATE TABLE bad (v TIMESTAMP(1e2))", "42601", "syntax error at or near \"1e2\"", "prepare"},
		{"CREATE TABLE bad (v TIMESTAMP(.5))", "42601", "syntax error at or near \".5\"", "prepare"},
	} {
		offset := strings.LastIndex(tc.q, "(") + 1
		if tc.code == "22023" {
			offset = strings.Index(tc.q, `"timestamp"`)
		}
		position := strconv.Itoa(utf8.RuneCountInString(tc.q[:offset]) + 1)
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.msg, Position: position})
		step.errorStage = tc.stage
		p.step(t, referencePrepared(step))
		step.errorStage = "execute"
		p.step(t, step)
	}
}

func TestReferenceTimestampTimeParameterRangeDiagnostics(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	unchanged := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}})
	for _, tc := range []struct {
		input         time.Time
		code, message string
	}{
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`},
		{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 7384)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:03:04"`},
		{time.Date(-4713, 11, 23, 4, 5, 6, 123456789, time.FixedZone("west", -7384)), "22008", `timestamp out of range: "4714-11-23 04:05:06.123456789-02:03:04 BC"`},
		{time.Date(294277, 1, 1, 4, 5, 6, 123456789, time.FixedZone("east", 9045)), "22008", `timestamp out of range: "294277-01-01 04:05:06.123456789+02:30:45"`},
		{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "5001-01-01 00:00:00Z BC"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00+16:00"`},
		{time.Date(-5000, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "5001-01-01 00:00:00-16:00 BC"`},
		{time.Date(2147483648, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00+16:00"`},
		{time.Date(-2147483647, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22008", `date/time field value out of range: "2147483648-01-01 00:00:00-16:00 BC"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789+16:00"`},
		{time.Date(2004, 10, 19, 10, 23, 54, 123456789, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54.123456789-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "294277-01-01 00:00:00-16:00"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00+15:59:59"`},
		{time.Date(294277, 1, 1, 0, 0, 0, 0, time.FixedZone("west", -57599)), "22008", `timestamp out of range: "294277-01-01 00:00:00-15:59:59"`},
	} {
		for _, q := range []string{"INSERT INTO t VALUES($1)", "INSERT INTO t VALUES('2000-01-01'),($1)"} {
			step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.input)
			pg := *step.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.diagnostic = &pg
			step.errorStage = "execute"
			p.step(t, step)
			p.step(t, referencePrepared(step))
			p.step(t, unchanged)
		}
	}
}

func TestReferenceTimestampNewInputErrorsAbortTransaction(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('epoch')", 1))
	for index := range 2 {
		db, recorder := p.memory, p.memoryRecord
		if index == 1 {
			db, recorder = p.postgres, p.postgresRecord
		}
		for _, tc := range []struct {
			input               any
			code, message, hint string
		}{
			{"2147483648-on-08", "22008", `date/time field value out of range: "2147483648-on-08"`, ""},
			{"1999-at-08", "22007", `invalid input syntax for type timestamp: "1999-at-08"`, ""},
			{"1999-on-08", "22007", `invalid input syntax for type timestamp: "1999-on-08"`, ""},
			{"1999-01-08--", "22007", `invalid input syntax for type timestamp: "1999-01-08--"`, ""},
			{"1999/01/08//", "22007", `invalid input syntax for type timestamp: "1999/01/08//"`, ""},
			{"99 January 1999", "22007", `invalid input syntax for type timestamp: "99 January 1999"`, ""},
			{"1999-01-08 04:05:06 now1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now1"`, ""},
			{"1999-01-08 04:05:06 now+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 now+2"`, ""},
			{"1999.01.08..", "22007", `invalid input syntax for type timestamp: "1999.01.08.."`, ""},
			{"1999-01-08-1", "22007", `invalid input syntax for type timestamp: "1999-01-08-1"`, ""},
			{"1999-01-08-2147483648", "22008", `date/time field value out of range: "1999-01-08-2147483648"`, ""},
			{"2147483648-xxx-08", "22007", `invalid input syntax for type timestamp: "2147483648-xxx-08"`, ""},
			{"2147483648-at-08", "22008", `date/time field value out of range: "2147483648-at-08"`, ""},
			{"1999 0 January", "22007", `invalid input syntax for type timestamp: "1999 0 January"`, ""},
			{"1999 32 January", "22007", `invalid input syntax for type timestamp: "1999 32 January"`, ""},
			{"0 January 1999", "22007", `invalid input syntax for type timestamp: "0 January 1999"`, ""},
			{"32 January 1999", "22007", `invalid input syntax for type timestamp: "32 January 1999"`, ""},
			{"1999 31 February", "22008", `date/time field value out of range: "1999 31 February"`, ""},
			{"1999-01-08 04:05:06 today1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today1"`, ""},
			{"1999-01-08 04:05:06 today+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 today+2"`, ""},
			{"1999-01-08 04:05:06 tomorrow1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow1"`, ""},
			{"1999-01-08 04:05:06 tomorrow+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 tomorrow+2"`, ""},
			{"1999-01-08 04:05:06 yesterday1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday1"`, ""},
			{"1999-01-08 04:05:06 yesterday+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 yesterday+2"`, ""},
			{"1999-01-08 04:05:06 epoch1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch1"`, ""},
			{"1999-01-08 04:05:06 epoch+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 epoch+2"`, ""},
			{"1999-01-08 04:05:06 infinity1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity1"`, ""},
			{"1999-01-08 04:05:06 infinity+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 infinity+2"`, ""},
			{"1999-01-08 04:05:06 allballs1", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs1"`, ""},
			{"1999-01-08 04:05:06 allballs+2", "22007", `invalid input syntax for type timestamp: "1999-01-08 04:05:06 allballs+2"`, ""},
			{"Friday 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "Friday 1999-01-08 04:05:06"`, ""},
			{"BC 0001-01-01", "22007", `invalid input syntax for type timestamp: "BC 0001-01-01"`, ""},
			{"PM 1999-01-08 04:05:06", "22007", `invalid input syntax for type timestamp: "PM 1999-01-08 04:05:06"`, ""},
			{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), "22008", `timestamp out of range: "294277-01-01 00:00:00Z"`, ""},
			{"January 1999-08", "22008", `date/time field value out of range: "January 1999-08"`, `Perhaps you need a different "DateStyle" setting.`},
			{"Jan 1999/08", "22008", `date/time field value out of range: "Jan 1999/08"`, `Perhaps you need a different "DateStyle" setting.`},
			{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("east", 57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54+16:00"`, ""},
			{time.Date(2004, 10, 19, 10, 23, 54, 0, time.FixedZone("west", -57600)), "22009", `time zone displacement out of range: "2004-10-19 10:23:54-16:00"`, ""},
		} {
			tx, err := db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
			step := referenceTransactionError(false, "INSERT INTO t VALUES('2000-01-01'),($1)", want)
			step.args = []any{tc.input}
			pg := want
			pg.Context = "unnamed portal parameter $1 = '...'"
			step.diagnostic = &pg
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, tx, recorder, step)
			referenceRunStep(t, p.ctx, tx, recorder, referenceTransactionError(false, "SELECT v FROM t", referenceTransactionAborted()))
			referenceRunStep(t, p.ctx, tx, recorder, referenceTransactionError(false, "INSERT INTO t VALUES('epoch')", referenceTransactionAborted()))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}))
		}
	}
}

func TestReferenceTimestampTimeDisplacementBounds(t *testing.T) {
	p := newTimestampReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v TIMESTAMP)", 0))
	for _, offset := range []int{57599, -57599} {
		input := time.Date(2004, 10, 19, 10, 23, 54, 123456600, time.FixedZone("civil", offset))
		step := referenceExec("INSERT INTO t VALUES($1)", 1, input)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	want := time.Date(2004, 10, 19, 10, 23, 54, 123457000, time.UTC)
	p.step(t, referenceSelect("SELECT v FROM t", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{want}, {want}, {want}, {want}}))
}
