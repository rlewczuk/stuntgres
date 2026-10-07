package sqltype

import "unicode/utf8"

// clipName takes validated UTF8 and retains complete code points in the visible
// 63-byte payload. Textual NAME input clips silently, unlike binary namerecv.
func clipName(text string) string {
	if len(text) <= 63 {
		return text
	}
	boundary := 63
	for !utf8.RuneStart(text[boundary]) {
		boundary--
	}
	return text[:boundary]
}
