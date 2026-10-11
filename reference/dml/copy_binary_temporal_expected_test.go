package reference_test

import (
	"stuntgres/reference/fixture"
	"time"
)

// Logical/diagnostic expectations grounded by pinned tests-1.md and the
// independently specified provider representation in the living plan. Inputs in copy_binary_temporal_test.go.
// Empty Diagnostic fields are asserted by the shared full-record fixture.
var copyBinaryTemporalExpected = map[string]copyBinaryProviderExpected{
	"date_epoch":                    {text: "2000-01-01\n", databaseType: "DATE", rawKind: "date", raw: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	"date_before_epoch":             {text: "1999-12-31\n", databaseType: "DATE", rawKind: "date", raw: time.Date(1999, time.December, 31, 0, 0, 0, 0, time.UTC)},
	"date_infinity":                 {text: "infinity\n", databaseType: "DATE", rawKind: "string", raw: "infinity"},
	"date_negative_infinity":        {text: "-infinity\n", databaseType: "DATE", rawKind: "string", raw: "-infinity"},
	"date_out_of_range":             {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "date out of range", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"time_midnight":                 {text: "00:00:00\n", databaseType: "TIME", rawKind: "string", raw: "00:00:00"},
	"time_24":                       {text: "24:00:00\n", databaseType: "TIME", rawKind: "string", raw: "24:00:00"},
	"time_negative":                 {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "time out of range", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"time_over_24":                  {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "time out of range", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"time_precision":                {text: "00:00:01.235\n", databaseType: "TIME", rawKind: "string", raw: "00:00:01.235"},
	"timetz":                        {text: "01:00:00+02\n", databaseType: "$timetz", rawKind: "string", raw: "01:00:00+02"},
	"timetz_bad_zone":               {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22009", Message: "time zone displacement out of range", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timestamp_epoch":               {text: "2000-01-01 00:00:00\n", databaseType: "TIMESTAMP", rawKind: "instant", raw: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	"timestamptz_epoch":             {text: "2000-01-01 00:00:00+00\n", databaseType: "TIMESTAMPTZ", rawKind: "instant", raw: time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	"timestamp_before_epoch":        {text: "1999-12-31 23:59:59.999999\n", databaseType: "TIMESTAMP", rawKind: "instant", raw: time.Date(1999, time.December, 31, 23, 59, 59, 999999000, time.UTC)},
	"timestamp_precision":           {text: "2000-01-01 00:00:01.235\n", databaseType: "TIMESTAMP", rawKind: "instant", raw: time.Date(2000, time.January, 1, 0, 0, 1, 235000000, time.UTC)},
	"timestamptz_precision":         {text: "2000-01-01 00:00:01.235+00\n", databaseType: "TIMESTAMPTZ", rawKind: "instant", raw: time.Date(2000, time.January, 1, 0, 0, 1, 235000000, time.UTC)},
	"timestamp_infinity":            {text: "infinity\n", databaseType: "TIMESTAMP", rawKind: "string", raw: "infinity"},
	"timestamptz_negative_infinity": {text: "-infinity\n", databaseType: "TIMESTAMPTZ", rawKind: "string", raw: "-infinity"},
	"timestamp_out_of_range":        {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22008", Message: "timestamp out of range", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"interval":                      {text: "3 mons 2 days 00:00:00.000001\n", databaseType: "INTERVAL", rawKind: "string", raw: "3 mons 2 days 00:00:00.000001"},
	"interval_precision":            {text: "3 mons 2 days 00:00:01.235\n", databaseType: "INTERVAL", rawKind: "string", raw: "3 mons 2 days 00:00:01.235"},
	"date_short":                    {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"date_trailing":                 {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"time_short":                    {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"time_trailing":                 {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timetz_short":                  {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timetz_trailing":               {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timestamp_short":               {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timestamp_trailing":            {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timestamptz_short":             {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"timestamptz_trailing":          {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"interval_short":                {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "08P01", Message: "insufficient data left in message", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
	"interval_trailing":             {diagnostic: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Detail: "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "", Context: "COPY copy_binary_probe, line 1, column v", Schema: "", Table: "", Column: "", DataType: "", Constraint: ""}},
}
