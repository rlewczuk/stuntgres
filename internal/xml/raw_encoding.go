package xml

import (
	"context"
	"strings"
	"stuntgres/internal/sqltype/pgencoding18"
)

// Private raw-reader families, never PostgreSQL encoding IDs.
const (
	rawISO2022JP = -2
	rawASCII = -3
	rawISO2022JP2 = -4
)

// rawEncoding admits literal qualified spellings, not PG name cleaning or ICU
// fuzzy aliases. Its bounded stack key performs ASCII case folding only.
func rawEncoding(label string) int {
	var key [32]byte
	if len(label) > len(key) { return -1 }
	for i := range len(label) {
		c := label[i]
		if c >= 'a' && c <= 'z' { c -= 'a'-'A' }
		key[i] = c
	}
	switch string(key[:len(label)]) {
	case "UTF8", "UTF-8": return 6
	case "ASCII", "US-ASCII": return rawASCII
	case "ISO-2022-JP-2", "ISO2022JP2", "CSISO2022JP2": return rawISO2022JP2
	case "ISO-2022-JP", "ISO2022JP", "CSISO2022JP", "ISO_2022_JP": return rawISO2022JP
	case "EUC_JP", "EUC-JP": return 1
	case "EUC_CN": return 2
	case "EUC_KR": return 3
	case "EUC_TW": return 4
	case "EUC-JISX0213": return 5
	case "LATIN1", "ISO-8859-1": return 8
	case "LATIN2", "ISO-8859-2": return 9
	case "LATIN3", "ISO-8859-3": return 10
	case "LATIN4", "ISO-8859-4": return 11
	case "LATIN5", "ISO-8859-9": return 12
	case "LATIN6", "ISO-8859-10": return 13
	case "LATIN7", "ISO-8859-13": return 14
	case "LATIN8", "ISO-8859-14": return 15
	case "LATIN9", "ISO-8859-15": return 16
	case "LATIN10", "ISO-8859-16": return 17
	case "WINDOWS-1256": return 18
	case "WINDOWS-1258": return 19
	case "CP866": return 20
	case "CP874": return 21
	case "KOI8R", "KOI8-R": return 22
	case "WINDOWS-1251": return 23
	case "WINDOWS-1252": return 24
	case "ISO_8859_5", "ISO-8859-5": return 25
	case "ISO_8859_6", "ISO-8859-6": return 26
	case "ISO_8859_7", "ISO-8859-7": return 27
	case "ISO_8859_8", "ISO-8859-8": return 28
	case "WINDOWS-1250": return 29
	case "WINDOWS-1253": return 30
	case "WINDOWS-1254": return 31
	case "WINDOWS-1255": return 32
	case "WINDOWS-1257": return 33
	case "KOI8U": return 34
	case "SJIS", "SHIFT_JIS": return 35
	case "BIG5": return 36
	case "GBK": return 37
	case "UHC": return 38
	case "GB18030": return 39
	case "JOHAB": return 40
	case "SHIFT_JISX0213": return 41
	}
	return -1
}

func rawCodecName(id int) string {
	if id == rawISO2022JP { return "ISO-2022-JP" }
	if id == rawASCII { return "ASCII" }
	if id == rawISO2022JP2 { return "ISO-2022-JP-2" }
	return pgencoding18.Name(id)
}

// rawDecodeStatus describes real input consumption, including buffering which
// produced no character. fault==-1 means no conversion error. state and jp2
// retain the applicable invocation designation at a real fault for retry.
type rawDecodeStatus struct { size, consumed, incomplete, fault int; state byte; jp2 jp2State; cause error }

// rawVerify bounds all lookahead kernels to a completely verified prefix.
func rawVerify(ctx context.Context, input string, id int) (int, error) {
	next := 0
	for i := 0; i < len(input); {
		if i >= next { if err := ctx.Err(); err != nil { return 0, err }; next = i+4096 }
		// NUL is decoded as U+0000; XML grammar, not the PG string verifier,
		// owns its termination behavior at this distinct byte-reader entry.
		if input[i] == 0 { i++; continue }
		n := 0
		if id==40 {n=rawJOHABWidth(input[i:])}else{n=pgencoding18.Verify(id,input[i:])}
		if n < 0 { return i, ctx.Err() }
		i += n
	}
	return len(input), ctx.Err()
}

// The raw JOHAB handler uses two-byte Hangul/symbol framing, unlike the
// PostgreSQL EUC-style Length/Verify contract for the same immutable mappings.
// glibc 2.36 johab.c FROM_LOOP supplies these byte ranges; mapped-cell admission
// remains the existing PostgreSQL forward map, not a new host codec/table.
func rawJOHABLead(c byte) bool {
	return c>=0x84&&c<=0xd3 || c>=0xd9&&c<=0xde || c>=0xe0&&c<=0xf9
}
func rawJOHABWidth(input string) int {
	c:=input[0]
	if c<0x80{return 1}
	if !rawJOHABLead(c)||len(input)<2{return -1}
	t:=input[1]
	if c<=0xd3 {
		if t>=0x41&&t<=0x7e || t>=0x81&&t<=0xfe{return 2}
		return -1
	}
	if !(t>=0x31&&t<=0x7e || t>=0x91&&t<=0xfe) || c==0xd9&&t>0xe8 || c==0xda&&t>=0xa1&&t<=0xd3 || c==0xde&&t>0xf1{return -1}
	return 2
}
func rawEncodingFailure(kind EncodingErrorKind,id int,input string) error {
	if id==40 {
		n:=1;if rawJOHABLead(input[0]){n=2}
		// Clamp the existing diagnostic formatter to the raw character width,
		// including 8F's two bytes, without changing PostgreSQL Length.
		input=input[:min(n,len(input))]
	}
	return encodingFailure(kind,id,6,input)
}

func rawDirectPass(ctx context.Context, input string, id, verified int, out *strings.Builder) (rawDecodeStatus, error) {
	s := rawDecodeStatus{fault:-1}
	next, nextOutput := 0, 0
	for i := 0; i < verified; {
		if i >= next || s.size >= nextOutput { if err := ctx.Err(); err != nil { return s, err }; next = i+4096; nextOutput=s.size+4096 }
		var a, b uint32
		n := 1
		invalid := false
		if id==40 && input[i]>=0x80 {
			n=2 // the whole suffix was already verified with raw JOHAB framing
			a,b=pgencoding18.LookupUTF8(id,pgencoding18.Pack(input[i:],n),n)
			if a==0{n=0}
		}else if input[i] != 0 { a, b, n, invalid = pgencoding18.Step(id, 6, input[i:verified]) }
		if invalid || n == 0 {
			s.fault, s.consumed = i, i
			kind := UntranslatableCharacter; if invalid { kind = InvalidSequence }
			s.cause = rawEncodingFailure(kind,id,input[i:])
			return s, ctx.Err()
		}
		width := pgencoding18.Width(a)+pgencoding18.Width(b)
		if input[i] == 0 { width = 1 }
		if !appendSize(s.size, width) { return s, conversionSizeError(len(input)) }
		s.size += width
		if out != nil { if input[i] == 0 { out.WriteByte(0) } else { writeWord(out,a); writeWord(out,b) } }
		i += n; s.consumed = i
	}
	if verified < len(input) {
		s.fault = verified
		s.cause = rawEncodingFailure(InvalidSequence,id,input[verified:])
	}
	return s, ctx.Err()
}

// rawDecode writes the unchanged consumed ASCII prefix and converted suffix
// directly into one exactly sized final builder. Sizing/emission are private
// implementation passes, not observable conversion attempts.
func rawDecode(ctx context.Context, input string, prefix, id int) (string, rawDecodeStatus, error) {
	if err := ctx.Err(); err != nil { return "", rawDecodeStatus{}, err }
	if !conversionInputSize(len(input)) { return "", rawDecodeStatus{}, conversionSizeError(len(input)) }
	suffix := input[prefix:]
	if id==rawASCII {
		status,err:=rawASCIIPass(ctx,suffix)
		if err!=nil { return "",status,err }
		if !appendSize(prefix,status.size) { return "",status,conversionSizeError(len(input)) }
		return input[:prefix+status.consumed],status,ctx.Err()
	}
	verified := len(suffix)
	var err error
	if id != rawISO2022JP && id != rawISO2022JP2 { verified, err = rawVerify(ctx, suffix, id); if err != nil { return "", rawDecodeStatus{}, err } }
	var status rawDecodeStatus
	switch id {
	case rawISO2022JP: status,err=iso2022JPPass(ctx,suffix,len(suffix),nil)
	case rawISO2022JP2: status,err=iso2022JP2Pass(ctx,suffix,len(suffix),nil)
	default: status,err=rawDirectPass(ctx,suffix,id,verified,nil)
	}
	if err != nil { return "", status, err }
	if !appendSize(prefix,status.size) { return "", status, conversionSizeError(len(input)) }
	decoded,err:=rawEmit(ctx,input,prefix,id,status)
	return decoded,status,err
}

func rawEmit(ctx context.Context,input string,prefix,id int,status rawDecodeStatus)(string,error){
	if err:=ctx.Err();err!=nil{return "",err}
	var b strings.Builder
	b.Grow(prefix+status.size)
	for at:=0;at<prefix;{
		if err:=ctx.Err();err!=nil{return "",err}
		end:=at+min(4096,prefix-at);b.WriteString(input[at:end]);at=end
	}
	// Stop before the known fault without truncating ISO lookahead. Produced
	// characters must not be reclassified as incomplete physical EOF input.
	suffix:=input[prefix:]
	var err error
	switch id {
	case rawISO2022JP:
		_,err=iso2022JPPass(ctx,suffix,status.consumed,&b)
	case rawISO2022JP2:
		_,err=iso2022JP2Pass(ctx,suffix,status.consumed,&b)
	default:
		suffix=suffix[:status.consumed]
		_,err=rawDirectPass(ctx,suffix,id,len(suffix),&b)
	}
	if err!=nil{return "",err}
	return b.String(),ctx.Err()
}
