package xml

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"stuntgres/internal/sqltype/pgencoding18"
)

const (
	xsltUTF16 = -20 - iota
	xsltUTF16LE
	xsltUTF16BE
	xsltUTF32
	xsltUTF32LE
	xsltUTF32BE
	xsltUCS2LE
	xsltUCS2BE
	xsltUTF7IMAP
	xsltHTMLEncoding
	xsltISO885911
)

// An encoder belongs to one save, including its lookahead and shift state. The
// pending scalar preserves combined mappings across serializer write boundaries.
// Only the final owned byte builder is published; no intermediate UTF8 document
// or conversion-sized scratch buffer is necessary.
type xsltOutputEncoder struct {
	p            *parser
	b            strings.Builder
	id           int
	started      bool
	pending      rune
	hasPending   bool
	g0           jp2Set
	g2           jp2G2
	jpLanguage   byte
	shifted      bool
	announcement byte
	cnSelected   byte
	utf7Bits     uint32
	utf7Count    uint
	utf7Shift    bool
}

// Handler lookup is deliberately distinct from PostgreSQL encoding-name
// normalization. An unknown handler is the released memory saver's *absent*
// encoder: it retains the requested declaration spelling and writes UTF8. A
// known handler always executes its actual encoder, never this recovery path.
func xsltOutputEncoding(label string) int {
	if len(label) > 128 {
		return -1
	}
	switch strings.ToUpper(label) {
	case "", "UTF8", "UTF-8":
		return 6
	case "UTF16", "UTF-16":
		return xsltUTF16
	case "UTF16LE", "UTF-16LE":
		return xsltUTF16LE
	case "UTF16BE", "UTF-16BE":
		return xsltUTF16BE
	case "UTF32", "UTF-32":
		return xsltUTF32
	case "UTF32LE", "UTF-32LE", "UCS-4LE":
		return xsltUTF32LE
	case "UTF32BE", "UTF-32BE", "UCS-4BE", "UCS-4", "UCS4", "CSUCS4", "ISO-10646", "ISO-10646/UCS4/":
		return xsltUTF32BE
	case "UCS-2", "UCS2", "UCS-2LE", "UNICODELITTLE", "ISO-10646/UCS2/":
		return xsltUCS2LE
	case "UCS-2BE", "UNICODEBIG":
		return xsltUCS2BE
	case "ISO2022KR", "CSISO2022KR":
		return rawISO2022KR
	case "ISO2022CN", "CSISO2022CN":
		return rawISO2022CN
	case "UTF7":
		return rawUTF7
	case "UTF-7-IMAP":
		return xsltUTF7IMAP
	case "HTML":
		return xsltHTMLEncoding
	case "ISO-8859-11", "ISO8859-11", "ISO_8859-11", "TIS-620", "TIS620":
		return xsltISO885911
	case "SHIFT-JIS", "SHIFT_JIS":
		return 35
	case "SHIFT-JISX0213":
		return 41
	case "CP932", "MS_KANJI", "SJIS":
		return 35
	case "EUC-CN", "GB2312", "GB_2312-80", "CSGB2312":
		return 2
	case "EUC-KR", "KSC5601", "KS_C_5601-1987":
		return 3
	case "EUC-TW":
		return 4
	case "EUC-JP-MS", "EUCJP", "CSEUCPKDFMTJAPANESE":
		return 1
	case "ISO8859-1", "ISO_8859-1", "ISO_8859-1:1987", "L1", "IBM819", "CP819", "CSISOLATIN1":
		return 8
	case "ANSI_X3.4-1968", "ANSI_X3.4-1986", "ISO646-US", "ISO_646.IRV:1991", "ISO-IR-6", "CP367", "IBM367", "CSASCII":
		return rawASCII
	}
	if id := rawEncoding(label); id != -1 {
		return id
	}
	// iconv also admits these literal aliases. Do not accept PG's arbitrary
	// punctuation removal: an unrecognized label must remain an absent handler.
	upper := strings.ToUpper(label)
	for _, prefix := range []string{"CP", "WINDOWS-"} {
		if strings.HasPrefix(upper, prefix) {
			switch upper[len(prefix):] {
			case "1250":
				return 29
			case "1251":
				return 23
			case "1252":
				return 24
			case "1253":
				return 30
			case "1254":
				return 31
			case "1255":
				return 32
			case "1256":
				return 18
			case "1257":
				return 33
			case "1258":
				return 19
			case "866":
				return 20
			case "874":
				return 21
			}
		}
	}
	for _, prefix := range []string{"ISO8859-", "ISO_8859-", "ISO-8859-"} {
		if strings.HasPrefix(upper, prefix) {
			switch upper[len(prefix):] {
			case "1":
				return 8
			case "2":
				return 9
			case "3":
				return 10
			case "4":
				return 11
			case "5":
				return 25
			case "6":
				return 26
			case "7":
				return 27
			case "8":
				return 28
			case "9":
				return 12
			case "10":
				return 13
			case "13":
				return 14
			case "14":
				return 15
			case "15":
				return 16
			case "16":
				return 17
			}
		}
	}
	return -1
}

func (e *xsltOutputEncoder) bytes(s string) {
	if !e.p.ensureBuilder(&e.b, len(s)) {
		return
	}
	for at := 0; at < len(s); {
		if e.p.err = e.p.ctx.Err(); e.p.err != nil {
			return
		}
		end := at + min(4096, len(s)-at)
		e.b.WriteString(s[at:end])
		at = end
	}
}
func (e *xsltOutputEncoder) byte(c byte) {
	if e.p.ensureBuilder(&e.b, 1) {
		e.b.WriteByte(c)
	}
}
func (e *xsltOutputEncoder) word(v uint32) {
	for n := pgencoding18.Width(v); n > 0; n-- {
		e.byte(byte(v >> uint((n-1)*8)))
	}
}
func (e *xsltOutputEncoder) start() {
	if e.started {
		return
	}
	e.started = true
	switch e.id {
	case xsltUTF16:
		e.bytes("\xff\xfe")
	case xsltUTF32:
		e.bytes("\xff\xfe\x00\x00")
	case rawISO2022KR:
		e.bytes("\x1b$)C")
	}
}
func (e *xsltOutputEncoder) write(s string) {
	if e.p.err != nil || s == "" {
		return
	}
	e.start()
	if e.id == 6 {
		e.bytes(s)
		return
	}
	nextPoll := 0
	for at := 0; at < len(s) && e.p.err == nil; {
		if at >= nextPoll {
			e.p.err = e.p.ctx.Err()
			if e.p.err != nil {
				return
			}
			nextPoll = at + 1024
		}
		r, n := utf8.DecodeRuneInString(s[at:])
		if r == utf8.RuneError && n == 1 {
			e.p.err = fmt.Errorf("invalid UTF8 in XSLT result")
			return
		}
		at += n
		if e.hasPending {
			if e.id >= 0 {
				var pair [8]byte
				n1 := utf8.EncodeRune(pair[:], e.pending)
				n2 := utf8.EncodeRune(pair[n1:], r)
				a, b, used, bad := pgencoding18.Step(6, e.id, string(pair[:n1+n2]))
				if !bad && used == n1+n2 {
					e.word(a)
					e.word(b)
					e.hasPending = false
					continue
				}
			}
			e.scalar(e.pending)
		}
		e.pending = r
		e.hasPending = true
	}
}
func (e *xsltOutputEncoder) unit(u uint16, be bool) {
	if be {
		e.byte(byte(u >> 8))
		e.byte(byte(u))
	} else {
		e.byte(byte(u))
		e.byte(byte(u >> 8))
	}
}
func (e *xsltOutputEncoder) ncr(r rune) {
	// libxml's output-buffer conversion recovery encodes the decimal reference
	// through the same stateful handler, including the shift back to ASCII.
	var buf [16]byte
	b := append(buf[:0], '&', '#')
	b = strconv.AppendInt(b, int64(r), 10)
	b = append(b, ';')
	for _, c := range b {
		e.scalar(rune(c))
	}
}
func xsltMappedOutput(id int, r rune) uint32 {
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], r)
	a, _, used, bad := pgencoding18.Step(6, id, string(buf[:n]))
	if bad || used == 0 {
		return 0
	}
	return a
}
func (e *xsltOutputEncoder) scalar(r rune) {
	if e.p.err != nil {
		return
	}
	switch e.id {
	case xsltUTF16, xsltUTF16LE, xsltUTF16BE, xsltUCS2LE, xsltUCS2BE:
		be := e.id == xsltUTF16BE || e.id == xsltUCS2BE
		if r > 0xffff {
			if e.id == xsltUCS2LE || e.id == xsltUCS2BE {
				e.ncr(r)
				return
			}
			a, b := utf16.EncodeRune(r)
			e.unit(uint16(a), be)
			e.unit(uint16(b), be)
		} else {
			e.unit(uint16(r), be)
		}
	case xsltUTF32, xsltUTF32LE, xsltUTF32BE:
		if e.id == xsltUTF32BE {
			for shift := 24; shift >= 0; shift -= 8 {
				e.byte(byte(uint32(r) >> uint(shift)))
			}
		} else {
			for shift := 0; shift <= 24; shift += 8 {
				e.byte(byte(uint32(r) >> uint(shift)))
			}
		}
	case rawASCII:
		if r < 128 {
			e.byte(byte(r))
		} else {
			e.ncr(r)
		}
	case rawISO2022JP, rawISO2022JP2:
		e.japanese(r)
	case rawISO2022KR:
		e.korean(r)
	case rawISO2022CN:
		e.chinese(r)
	case rawUTF7, xsltUTF7IMAP:
		e.utf7(r)
	case xsltHTMLEncoding:
		if r < 128 {
			e.byte(byte(r))
		} else if name := xsltHTMLEntity(r); name != "" {
			e.byte('&')
			e.bytes(name)
			e.byte(';')
		} else {
			e.ncr(r)
		}
	case xsltISO885911:
		switch {
		case r < 0xa0:
			e.byte(byte(r))
		case r == 0xa0 && e.id == xsltISO885911:
			e.byte(0xa0)
		case r >= 0xe01 && r <= 0xe3a:
			e.byte(byte(r - 0xe01 + 0xa1))
		case r >= 0xe3f && r <= 0xe5b:
			e.byte(byte(r - 0xe3f + 0xdf))
		default:
			e.ncr(r)
		}
	default:
		if r == 0 {
			e.byte(0)
			return
		}
		if r < 128 {
			e.byte(byte(r))
			return
		}
		a := xsltMappedOutput(e.id, r)
		if a == 0 {
			e.ncr(r)
		} else {
			e.word(a)
		}
	}
}
func (e *xsltOutputEncoder) selectJP(set jp2Set) {
	if e.g0 == set {
		return
	}
	switch set {
	case jp2ASCII:
		e.bytes("\x1b(B")
	case jp2Roman:
		e.bytes("\x1b(J")
	case jp2Kana:
		e.bytes("\x1b(I")
	case jp2JIS0208:
		e.bytes("\x1b$B")
	case jp2JIS0212:
		e.bytes("\x1b$(D")
	case jp2GB2312:
		e.bytes("\x1b$A")
	case jp2KSC5601:
		e.bytes("\x1b$(C")
	}
	e.g0 = set
}
func (e *xsltOutputEncoder) japanese(r rune) {
	if e.id == rawISO2022JP2 {
		if r >= 0xe0000 && r <= 0xe007f {
			tag := byte(r & 127)
			if tag >= 'A' && tag <= 'Z' {
				tag += 'a' - 'A'
			}
			switch {
			case tag == 1:
				e.jpLanguage = 4
			case tag == 'j' && e.jpLanguage == 4:
				e.jpLanguage = 5
			case tag == 'a' && e.jpLanguage == 5:
				e.jpLanguage = 1
			case tag == 'k' && e.jpLanguage == 4:
				e.jpLanguage = 6
			case tag == 'o' && e.jpLanguage == 6:
				e.jpLanguage = 2
			case tag == 'z' && e.jpLanguage == 4:
				e.jpLanguage = 7
			case tag == 'h' && e.jpLanguage == 7:
				e.jpLanguage = 3
			case tag == 127 || e.jpLanguage >= 4:
				e.jpLanguage = 0
			}
			return
		}
		if e.jpLanguage >= 4 {
			e.jpLanguage = 0
		}
	}
	preferred := e.jpLanguage
	currentLanguage := byte(1)
	if e.g0 == jp2GB2312 {
		currentLanguage = 3
	} else if e.g0 == jp2KSC5601 {
		currentLanguage = 2
	}
	if preferred == 0 || preferred == currentLanguage || e.g0 == jp2ASCII {
		if e.jpSet(e.g0, r) {
			return
		}
	}
	if preferred == 0 && e.g2 != jp2Unassigned && e.jpEuropean(r, e.g2) {
		return
	}
	if r < 128 {
		e.selectJP(jp2ASCII)
		e.byte(byte(r))
		if r == '\n' {
			e.g2 = jp2Unassigned
		}
		return
	}
	if e.id == rawISO2022JP {
		if e.jpSet(jp2Roman, r) || e.jpSet(jp2JIS0208, r) {
			return
		}
		e.ncr(r)
		return
	}
	order := [4]byte{1, 0, 3, 2}
	if preferred == 2 {
		order = [4]byte{2, 0, 1, 3}
	} else if preferred == 3 {
		order = [4]byte{3, 0, 1, 2}
	}
	for _, language := range order {
		switch language {
		case 0:
			if e.jpEuropean(r, jp2Latin) || e.jpEuropean(r, jp2Greek) {
				return
			}
		case 1:
			if e.jpSet(jp2Roman, r) || e.jpSet(jp2JIS0208, r) || e.jpSet(jp2JIS0212, r) {
				return
			}
		case 2:
			if e.jpSet(jp2KSC5601, r) {
				return
			}
		case 3:
			if e.jpSet(jp2GB2312, r) {
				return
			}
		}
	}
	if e.jpSet(jp2Kana, r) {
		return
	}
	e.ncr(r)
}
func (e *xsltOutputEncoder) jpSet(set jp2Set, r rune) bool {
	switch set {
	case jp2ASCII:
		if r < 128 {
			e.byte(byte(r))
			if r == '\n' {
				e.g2 = jp2Unassigned
			}
			return true
		}
		return false
	case jp2Roman:
		c := r
		if r == 0xa5 {
			c = '\\'
		} else if r == 0x203e {
			c = '~'
		} else if r == '\\' || r == '~' {
			return false
		}
		if c <= 0x20 || c >= 0x7f {
			return false
		}
		e.selectJP(set)
		e.byte(byte(c))
		return true
	case jp2Kana:
		if r < 0xff61 || r > 0xff9f {
			return false
		}
		e.selectJP(set)
		e.byte(byte(r - 0xff61 + 0x21))
		return true
	}
	id := 1
	if set == jp2GB2312 {
		id = 2
	} else if set == jp2KSC5601 {
		id = 3
	}
	a := xsltMappedOutput(id, r)
	if set == jp2JIS0212 {
		if a>>16 != 0x8f {
			return false
		}
	} else if a < 0xa1a1 || a > 0xfefe {
		return false
	}
	e.selectJP(set)
	e.byte(byte(a>>8) & 0x7f)
	e.byte(byte(a) & 0x7f)
	return true
}
func (e *xsltOutputEncoder) jpEuropean(r rune, set jp2G2) bool {
	a := uint32(r)
	if set == jp2Greek {
		a = xsltMappedOutput(27, r)
	}
	if a < 0x80 || a > 0xff {
		return false
	}
	if e.g2 != set {
		if set == jp2Latin {
			e.bytes("\x1b.A")
		} else {
			e.bytes("\x1b.F")
		}
		e.g2 = set
	}
	e.bytes("\x1bN")
	e.byte(byte(a) & 0x7f)
	return true
}
func (e *xsltOutputEncoder) korean(r rune) {
	if r < 128 {
		if e.shifted {
			e.byte(15)
			e.shifted = false
		}
		e.byte(byte(r))
		return
	}
	a := xsltMappedOutput(3, r)
	if a < 0xa1a1 || a > 0xfefe {
		e.ncr(r)
		return
	}
	if !e.shifted {
		e.byte(14)
		e.shifted = true
	}
	e.byte(byte(a>>8) & 0x7f)
	e.byte(byte(a) & 0x7f)
}
func (e *xsltOutputEncoder) chinese(r rune) {
	if r < 128 {
		if e.cnSelected != 0 {
			e.byte(15)
			e.cnSelected = 0
		}
		e.byte(byte(r))
		if r == '\n' {
			e.announcement = 0
		}
		return
	}
	gb, tw := xsltMappedOutput(2, r), xsltMappedOutput(4, r)
	use, a := byte(1), gb
	if e.cnSelected != 1 && e.announcement&3 == 2 {
		use, a = 2, tw
		if a > 0xffff {
			a = 0
		}
	}
	if a < 0xa1a1 || a > 0xfefe {
		if tw>>16 == 0x8ea2 {
			use, a = 3, tw
		} else if use == 1 && tw >= 0xa1a1 && tw <= 0xfefe {
			use, a = 2, tw
		} else if use == 2 && gb >= 0xa1a1 && gb <= 0xfefe {
			use, a = 1, gb
		} else {
			e.ncr(r)
			return
		}
	}
	// Selection and announcements are separate encoder state. Preserve the
	// handler's actual post-SS2 selection, not the raw decoder's input state.
	if e.cnSelected != use {
		if use == 3 {
			if e.announcement&4 == 0 {
				e.bytes("\x1b$*H")
				e.announcement |= 4
			}
			e.bytes("\x1bN")
		} else {
			if e.announcement&3 != use {
				if use == 1 {
					e.bytes("\x1b$)A")
				} else {
					e.bytes("\x1b$)G")
				}
				e.announcement = e.announcement&4 | use
			}
			if e.cnSelected == 0 {
				e.byte(14)
			}
		}
	}
	e.byte(byte(a>>8) & 0x7f)
	e.byte(byte(a) & 0x7f)
	e.cnSelected = use
}

const xsltBase64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func xsltUTF7Direct(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("'(),-./:? \t\r\n", r)
}
func (e *xsltOutputEncoder) endUTF7(explicit bool) {
	if !e.utf7Shift {
		return
	}
	if e.utf7Count > 0 {
		e.utf7Sextet(e.utf7Bits << (6 - e.utf7Count) & 63)
	}
	if explicit {
		e.byte('-')
	}
	e.utf7Shift = false
	e.utf7Bits = 0
	e.utf7Count = 0
}
func (e *xsltOutputEncoder) utf7Unit(u uint16) {
	e.utf7Bits = e.utf7Bits<<16 | uint32(u)
	e.utf7Count += 16
	for e.utf7Count >= 6 {
		e.utf7Count -= 6
		e.utf7Sextet(e.utf7Bits >> e.utf7Count & 63)
	}
	e.utf7Bits &= (1 << e.utf7Count) - 1
}
func (e *xsltOutputEncoder) utf7(r rune) {
	imap := e.id == xsltUTF7IMAP
	direct := xsltUTF7Direct(r)
	shift := rune('+')
	if imap {
		direct = r >= 32 && r <= 126 && r != '&'
		shift = '&'
	}
	if direct {
		explicit := imap || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '/' && r <= '9' || r == '-'
		e.endUTF7(explicit)
		e.byte(byte(r))
		return
	}
	if r == shift && !e.utf7Shift {
		e.byte(byte(shift))
		e.byte('-')
		return
	}
	if !e.utf7Shift {
		e.byte(byte(shift))
		e.utf7Shift = true
	}
	if r > 0xffff {
		a, b := utf16.EncodeRune(r)
		e.utf7Unit(uint16(a))
		e.utf7Unit(uint16(b))
	} else {
		e.utf7Unit(uint16(r))
	}
}
func (e *xsltOutputEncoder) utf7Sextet(value uint32) {
	if value == 63 && e.id == xsltUTF7IMAP {
		e.byte(',')
	} else {
		e.byte(xsltBase64[value])
	}
}
func (e *xsltOutputEncoder) finish() string {
	if e.hasPending {
		e.scalar(e.pending)
		e.hasPending = false
	}
	// xsltSaveResultToString copies before xmlOutputBufferClose's final iconv
	// reset. Ordinary ASCII following nonASCII and NCR recovery do reset state;
	// a final shift is not fabricated at this earlier publication boundary.
	return e.b.String()
}

// Canonical HTML4 output names, as selected by libxml's UTF8ToHtml handler.
// Dense ranges and a sorted sparse tail avoid a per-invocation map or reversing
// a decoder's aliases. Characters without an HTML4 name use decimal references.
var xsltHTMLLatin = [...]string{
	"nbsp", "iexcl", "cent", "pound", "curren", "yen", "brvbar", "sect", "uml", "copy", "ordf", "laquo", "not", "shy", "reg", "macr",
	"deg", "plusmn", "sup2", "sup3", "acute", "micro", "para", "middot", "cedil", "sup1", "ordm", "raquo", "frac14", "frac12", "frac34", "iquest",
	"Agrave", "Aacute", "Acirc", "Atilde", "Auml", "Aring", "AElig", "Ccedil", "Egrave", "Eacute", "Ecirc", "Euml", "Igrave", "Iacute", "Icirc", "Iuml",
	"ETH", "Ntilde", "Ograve", "Oacute", "Ocirc", "Otilde", "Ouml", "times", "Oslash", "Ugrave", "Uacute", "Ucirc", "Uuml", "Yacute", "THORN", "szlig",
	"agrave", "aacute", "acirc", "atilde", "auml", "aring", "aelig", "ccedil", "egrave", "eacute", "ecirc", "euml", "igrave", "iacute", "icirc", "iuml",
	"eth", "ntilde", "ograve", "oacute", "ocirc", "otilde", "ouml", "divide", "oslash", "ugrave", "uacute", "ucirc", "uuml", "yacute", "thorn", "yuml",
}
var xsltHTMLGreekUpper = [...]string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta", "Iota", "Kappa", "Lambda", "Mu", "Nu", "Xi", "Omicron", "Pi", "Rho", "", "Sigma", "Tau", "Upsilon", "Phi", "Chi", "Psi", "Omega"}
var xsltHTMLGreekLower = [...]string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa", "lambda", "mu", "nu", "xi", "omicron", "pi", "rho", "sigmaf", "sigma", "tau", "upsilon", "phi", "chi", "psi", "omega"}
var xsltHTMLSparse = [...]struct {
	r    rune
	name string
}{
	{338, "OElig"}, {339, "oelig"}, {352, "Scaron"}, {353, "scaron"}, {376, "Yuml"}, {402, "fnof"}, {710, "circ"}, {732, "tilde"},
	{977, "thetasym"}, {978, "upsih"}, {982, "piv"}, {8194, "ensp"}, {8195, "emsp"}, {8201, "thinsp"}, {8204, "zwnj"}, {8205, "zwj"}, {8206, "lrm"}, {8207, "rlm"},
	{8211, "ndash"}, {8212, "mdash"}, {8216, "lsquo"}, {8217, "rsquo"}, {8218, "sbquo"}, {8220, "ldquo"}, {8221, "rdquo"}, {8222, "bdquo"}, {8224, "dagger"}, {8225, "Dagger"}, {8226, "bull"}, {8230, "hellip"},
	{8240, "permil"}, {8242, "prime"}, {8243, "Prime"}, {8249, "lsaquo"}, {8250, "rsaquo"}, {8254, "oline"}, {8260, "frasl"}, {8364, "euro"},
	{8465, "image"}, {8472, "weierp"}, {8476, "real"}, {8482, "trade"}, {8501, "alefsym"}, {8592, "larr"}, {8593, "uarr"}, {8594, "rarr"}, {8595, "darr"}, {8596, "harr"}, {8629, "crarr"},
	{8656, "lArr"}, {8657, "uArr"}, {8658, "rArr"}, {8659, "dArr"}, {8660, "hArr"}, {8704, "forall"}, {8706, "part"}, {8707, "exist"}, {8709, "empty"}, {8711, "nabla"}, {8712, "isin"}, {8713, "notin"}, {8715, "ni"}, {8719, "prod"},
	{8721, "sum"}, {8722, "minus"}, {8727, "lowast"}, {8730, "radic"}, {8733, "prop"}, {8734, "infin"}, {8736, "ang"}, {8743, "and"}, {8744, "or"}, {8745, "cap"}, {8746, "cup"}, {8747, "int"}, {8756, "there4"}, {8764, "sim"}, {8773, "cong"}, {8776, "asymp"},
	{8800, "ne"}, {8801, "equiv"}, {8804, "le"}, {8805, "ge"}, {8834, "sub"}, {8835, "sup"}, {8836, "nsub"}, {8838, "sube"}, {8839, "supe"}, {8853, "oplus"}, {8855, "otimes"}, {8869, "perp"}, {8901, "sdot"},
	{8968, "lceil"}, {8969, "rceil"}, {8970, "lfloor"}, {8971, "rfloor"}, {9001, "lang"}, {9002, "rang"}, {9674, "loz"}, {9824, "spades"}, {9827, "clubs"}, {9829, "hearts"}, {9830, "diams"},
}

func xsltHTMLEntity(r rune) string {
	if r >= 160 && r <= 255 {
		return xsltHTMLLatin[r-160]
	}
	if r >= 913 && r <= 937 {
		return xsltHTMLGreekUpper[r-913]
	}
	if r >= 945 && r <= 969 {
		return xsltHTMLGreekLower[r-945]
	}
	lo, hi := 0, len(xsltHTMLSparse)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if xsltHTMLSparse[mid].r < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(xsltHTMLSparse) && xsltHTMLSparse[lo].r == r {
		return xsltHTMLSparse[lo].name
	}
	return ""
}
