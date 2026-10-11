package reference_test

import "testing"

func TestReferenceDatetimeMakeDiagnostics(t *testing.T) {
	datetimeMakeFailures(t, []datetimeMakeFailure{
		{name: "calendar", expression: "make_date(2013,2,29)", code: "22008", message: "date field value out of range: 2013-02-29"},
		{name: "zeroYear", expression: "make_date(0,1,1)", code: "22008", message: "date field value out of range: 0-01-01"},
		{name: "BCMutated", expression: "make_date(-44,2,30)", code: "22008", message: "date field value out of range: -43-02-30"},
		{name: "century", expression: "make_date(1900,2,29)", code: "22008", message: "date field value out of range: 1900-02-29"},
		{name: "month", expression: "make_date(2000,13,1)", code: "22008", message: "date field value out of range: 2000-13-01"},
		{name: "dateLower", expression: "make_date(-4714,11,23)", code: "22008", message: "date out of range: -4713-11-23"},
		{name: "dateUpper", expression: "make_date(5874898,1,1)", code: "22008", message: "date out of range: 5874898-01-01"},
		{name: "hourNegative", expression: "make_time(-1,0,0)", code: "22008", message: "time field value out of range: -1:00:00"},
		{name: "hourHigh", expression: "make_time(25,0,0)", code: "22008", message: "time field value out of range: 25:00:00"},
		{name: "minuteNegative", expression: "make_time(0,-1,0)", code: "22008", message: "time field value out of range: 0:-1:00"},
		{name: "minuteHigh", expression: "make_time(0,60,0)", code: "22008", message: "time field value out of range: 0:60:00"},
		{name: "secNegative", expression: "make_time(0,0,-1)", code: "22008", message: "time field value out of range: 0:00:-1"},
		{name: "secHigh", expression: "make_time(0,0,61)", code: "22008", message: "time field value out of range: 0:00:61"},
		{name: "endpointOverflow", expression: "make_time(24,0,0.000001)", code: "22008", message: "time field value out of range: 24:00:1e-06"},
		{name: "clockSum", expression: "make_time(23,59,60.000001)", code: "22008", message: "time field value out of range: 23:59:60"},
		{name: "timestampBCOriginal", expression: "make_timestamp(-44,2,30,0,0,0)", code: "22008", message: "date field value out of range: -44-02-30"},
		{name: "timestampUpper", expression: "make_timestamp(294277,1,1,0,0,0)", code: "22008", message: "timestamp out of range: 294277-01-01 0:00:00"},
		{name: "wideTimestamp", expression: "make_timestamp(300000,1,1,0,0,0)", code: "22008", message: "timestamp out of range: 300000-01-01 0:00:00"},
		{name: "badZone", expression: "make_timestamptz(2000,1,1,0,0,0,'Nehwon/Lankhmar')", code: "22023", message: `time zone "Nehwon/Lankhmar" not recognized`},
		{name: "unsignedZone", expression: "make_timestamptz(2000,1,1,0,0,0,'2')", code: "22023", message: `invalid input syntax for type numeric time zone: "2"`, hint: `Numeric time zones must have "-" or "+" as first character.`},
		{name: "numericZoneOverflow", expression: "make_timestamptz(2000,1,1,0,0,0,'+16')", code: "22023", message: `numeric time zone "+16" out of range`},
		{name: "unsignedZoneQuote", expression: `make_timestamptz(2000,1,1,0,0,0,'2"x')`, code: "22023", message: `invalid input syntax for type numeric time zone: "2"x"`, hint: `Numeric time zones must have "-" or "+" as first character.`},
		{name: "unsignedZoneBackslash", expression: `make_timestamptz(2000,1,1,0,0,0,'2\x')`, code: "22023", message: `invalid input syntax for type numeric time zone: "2\x"`, hint: `Numeric time zones must have "-" or "+" as first character.`},
		{name: "numericZoneOverflowQuote", expression: `make_timestamptz(2000,1,1,0,0,0,'+16"x')`, code: "22023", message: `numeric time zone "+16"x" out of range`},
		{name: "numericZoneOverflowBackslash", expression: `make_timestamptz(2000,1,1,0,0,0,'+16\x')`, code: "22023", message: `numeric time zone "+16\x" out of range`},
		{name: "calendarBeforeZone", expression: "make_timestamptz(2013,2,29,0,0,0,'bad')", code: "22008", message: "date field value out of range: 2013-02-29"},
		{name: "postZoneLower", expression: "make_timestamptz(-4714,11,24,0,0,0,'+1')", code: "22008", message: "timestamp out of range"},
		{name: "postZoneUpper", expression: "make_timestamptz(294276,12,31,23,59,59,'-1')", code: "22008", message: "timestamp out of range"},
	})
}
