package session

import "stuntgres/internal/sqltype"

// Presentation captures the typed input and output environments of the
// store under its mutex. Admission serializes session settings; publication
// uses the same capture when formatting its supplementary integrity fence.
func (s *SettingsStore) Presentation() (input sqltype.InputEnvironment, output sqltype.OutputEnvironment) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Value("xmloption") == "document" {
		input.XMLMode = sqltype.XMLDocument
	}
	if s.Value("bytea_output") == "escape" {
		output.ByteaOutput = sqltype.ByteaEscape
	}
	output.XMLBinaryHex = s.Value("xmlbinary") == "hex"
	output.QuoteAllIdentifiers = s.Value("quote_all_identifiers") == "on"
	_, output.ClientEncoding = sqltype.ClientEncodingName(s.Value("client_encoding"))
	input.Transport = s.clientEncodingLocked()
	output.Transport = input.Transport
	zone, ok := s.Typed["timezone"]
	if !ok {
		zone = s.DefaultTyped["timezone"]
	}
	style, ok := s.Typed["datestyle"]
	if !ok {
		style = s.DefaultTyped["datestyle"]
	}
	abbreviations, ok := s.Typed["timezone_abbreviations"]
	if !ok {
		abbreviations = s.DefaultTyped["timezone_abbreviations"]
	}
	input.TimezoneAbbreviations = abbreviations.TimezoneAbbreviations
	input.Zone, output.Zone = zone.Zone, zone.Zone
	input.DateStyle, output.DateStyle = style.DateStyle, style.DateStyle
	output.IntervalStyle = sqltype.ParseIntervalStyle(s.Value("intervalstyle"))
	input.IntervalStyle = output.IntervalStyle
	output.ExtraFloatDigits = int32(s.TypedValue("extra_float_digits").Number)
	output.FloatPrecisionSet = true
	input.NumericLocale = s.TypedValue("lc_numeric").Locale
	input.MonetaryLocale = s.TypedValue("lc_monetary").Locale
	input.TimeLocale = s.TypedValue("lc_time").Locale
	output.NumericLocale, output.MonetaryLocale, output.TimeLocale = input.NumericLocale, input.MonetaryLocale, input.TimeLocale
	return input, output
}
