package functions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqltype"
)

type ispellMask struct {
	letters string
	negate  bool
}
type ispellAffix struct {
	flag, strip, add string
	bits             uint8
	suffix           bool
	mask             []ispellMask
	program          *sqltype.PGRegexProgram
}

var ispellNewFormat = errors.New("new-format affix commands")

func (b *IspellCompiler) LoadAffixes(ctx context.Context, name string) error {
	data, err := b.files.resource(ctx, name, "affix", "affix")
	if err != nil {
		return err
	}
	b.d.afffile = name
	err = b.readOldAffixes(ctx, name+".affix", data)
	if errors.Is(err, ispellNewFormat) {
		return b.readNewAffixes(ctx, name+".affix", data)
	}
	return err
}
func (b *IspellCompiler) addCompound(ctx context.Context, s string, bits uint8) error {
	at := 0
	for at < len(s) && ispellSpace(s[at]) {
		if at&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		at++
	}
	s = s[at:]
	if s == "" {
		return dictionaryError("F0000", "syntax error")
	}
	end := 0
	for end < len(s) && !ispellSpace(s[end]) {
		if end&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		end++
	}
	name, err := ispellAffixField(ctx, s[:end])
	if err != nil {
		return err
	}
	flag, err := b.compoundFlag(name)
	if err != nil {
		return err
	}
	b.compound[flag] |= bits
	b.d.useCompound = true
	return nil
}

// MySpell fields use C whitespace, stop at a field-leading comment, and
// retain at most BUFSIZ-1 bytes per field, as get_nextfield does upstream.
func ispellAffixFields(ctx context.Context, line string) ([5]string, int, error) {
	var fields [5]string
	n := 0
	for at := 0; at < len(line) && n < len(fields); {
		for at < len(line) && ispellSpace(line[at]) {
			if at&255 == 0 {
				if err := ctx.Err(); err != nil {
					return fields, n, err
				}
			}
			at++
		}
		if at == len(line) || line[at] == '#' {
			break
		}
		start := at
		for at < len(line) && !ispellSpace(line[at]) {
			if at&255 == 0 {
				if err := ctx.Err(); err != nil {
					return fields, n, err
				}
			}
			at++
		}
		field, err := ispellAffixField(ctx, line[start:at])
		if err != nil {
			return fields, n, err
		}
		fields[n] = field
		n++
	}
	return fields, n, ctx.Err()
}
func ispellAffixField(ctx context.Context, s string) (string, error) {
	if len(s) < 8192 {
		return s, ctx.Err()
	}
	var field strings.Builder
	field.Grow(8191)
	for _, r := range s {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if field.Len()+utf8.RuneLen(r) < 8192 {
			field.WriteRune(r)
		}
	}
	return field.String(), ctx.Err()
}
func (b *IspellCompiler) readNewAffixes(ctx context.Context, file, data string) error {
	err := readDictionaryLines(ctx, file, data, func(line string) error {
		if line == "" || ispellSpace(line[0]) || line[0] == '#' {
			return nil
		}
		// Required compound operands precede generic short-line/comment
		// handling. The release matches command prefixes and treats a flag
		// starting with '#' as data, not as a field-leading comment.
		switch {
		case strings.HasPrefix(line, "COMPOUNDFLAG"):
			return b.addCompound(ctx, line[len("COMPOUNDFLAG"):], ispellAll)
		case strings.HasPrefix(line, "COMPOUNDBEGIN"):
			return b.addCompound(ctx, line[len("COMPOUNDBEGIN"):], ispellBegin)
		case strings.HasPrefix(line, "COMPOUNDMIDDLE"):
			return b.addCompound(ctx, line[len("COMPOUNDMIDDLE"):], ispellMiddle)
		case strings.HasPrefix(line, "COMPOUNDLAST"):
			return b.addCompound(ctx, line[len("COMPOUNDLAST"):], ispellEnd)
		case strings.HasPrefix(line, "COMPOUNDEND"):
			return b.addCompound(ctx, line[len("COMPOUNDEND"):], ispellEnd)
		case strings.HasPrefix(line, "ONLYINCOMPOUND"):
			return b.addCompound(ctx, line[len("ONLYINCOMPOUND"):], ispellOnly)
		case strings.HasPrefix(line, "COMPOUNDPERMITFLAG"):
			return b.addCompound(ctx, line[len("COMPOUNDPERMITFLAG"):], ispellPermit)
		case strings.HasPrefix(line, "COMPOUNDFORBIDFLAG"):
			return b.addCompound(ctx, line[len("COMPOUNDFORBIDFLAG"):], ispellForbid)
		}
		if strings.HasPrefix(line, "FLAG") {
			// FLAG uses the raw remainder, including a leading '#', rather
			// than generic fields. Empty/default operands leave the mode alone.
			at := len("FLAG")
			for at < len(line) && ispellSpace(line[at]) {
				if at&255 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				at++
			}
			operand := line[at:]
			switch {
			case operand == "":
			case strings.HasPrefix(operand, "long"):
				b.mode = "long"
			case strings.HasPrefix(operand, "num"):
				b.mode = "num"
			case strings.HasPrefix(operand, "default"):
			default:
				return dictionaryError("F0000", `Ispell dictionary supports only "default", "long", and "num" flag values`)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var bits uint8
	suffix := false
	err = readDictionaryLines(ctx, file, data, func(line string) error {
		if line == "" || ispellSpace(line[0]) || line[0] == '#' {
			return nil
		}
		fields, n, err := ispellAffixFields(ctx, line)
		if err != nil {
			return err
		}
		f := fields[:n]
		if len(f) == 0 {
			return nil
		}
		command := strings.ToLower(f[0])
		if strings.HasPrefix(command, "af") {
			// Alias commands precede short-line handling. The scanner's
			// zero-filled operand distinguishes missing counts from empty entries.
			operand := fields[1]
			if b.aliases == nil {
				n, err := ispellNumber(operand)
				if err != nil || n <= 0 {
					return dictionaryError("F0000", "invalid number of flag vector aliases")
				}
				b.aliasCount = int(n)
				b.aliases = []string{""}
			} else {
				if len(b.aliases) > b.aliasCount {
					return dictionaryError("F0000", fmt.Sprintf("number of aliases exceeds specified number %d", b.aliasCount))
				}
				b.aliases = append(b.aliases, operand)
			}
			return nil
		}
		if len(f) < 4 || (!strings.HasPrefix(command, "pfx") && !strings.HasPrefix(command, "sfx")) {
			return nil
		}
		if f[1] == "" || (b.mode != "long" && b.mode != "num" && len(f[1]) > 1) || (b.mode == "long" && len(f[1]) > 2) {
			return nil
		}
		if len(f) == 4 {
			suffix = strings.HasPrefix(command, "sfx")
			bits = 0
			if len(f[2]) > 0 && (f[2][0] == 'y' || f[2][0] == 'Y') {
				bits = ispellCross
			}
			return nil
		}
		strip, err := ispellLower(ctx, f[2])
		if err != nil {
			return err
		}
		add, continuation, _ := strings.Cut(f[3], "/")
		continuation, err = b.alias(continuation)
		if err != nil {
			return err
		}
		continuationBits, err := b.flagBits(ctx, continuation)
		if err != nil {
			return err
		}
		add, err = ispellLower(ctx, add)
		if err != nil {
			return err
		}
		mask, err := ispellLower(ctx, f[4])
		if err != nil {
			return err
		}
		if strings.HasPrefix(strip, "0") {
			strip = ""
		}
		if strings.HasPrefix(add, "0") {
			add = ""
		}
		return b.addAffix(ctx, f[1], bits|continuationBits, strip, add, mask, suffix)
	})
	if err != nil {
		return err
	}
	if b.aliases != nil && len(b.aliases) != b.aliasCount+1 {
		return dictionaryError("F0000", fmt.Sprintf("number of aliases is less than specified number %d", b.aliasCount))
	}
	return ctx.Err()
}
func (b *IspellCompiler) readOldAffixes(ctx context.Context, file, data string) error {
	active, suffix := false, false
	var flag string
	var bits uint8
	old := false
	newFormat := func() error {
		if old {
			return dictionaryError("F0000", "affix file contains both old-style and new-style commands")
		}
		return ispellNewFormat
	}
	return readDictionaryLines(ctx, file, data, func(line string) error {
		lower, err := ispellLower(ctx, line)
		if err != nil {
			return err
		}
		if strings.HasPrefix(lower, "#") || lower == "\n" {
			return nil
		}
		if strings.HasPrefix(lower, "compoundwords") {
			if at := strings.IndexAny(line, "lL"); at >= 0 {
				old = true
				rest := line[at:]
				for len(rest) > 0 && !ispellSpace(rest[0]) {
					rest = rest[1:]
				}
				rest = strings.TrimLeft(rest, " \t\r\n\v\f")
				if len(rest) > 0 && rest[0] < 128 {
					return b.addCompound(ctx, rest, ispellAll)
				}
			}
			return nil
		}
		if strings.HasPrefix(lower, "prefixes") {
			old = true
			active = true
			suffix = false
			return nil
		}
		if strings.HasPrefix(lower, "suffixes") {
			old = true
			active = true
			suffix = true
			return nil
		}
		if strings.HasPrefix(lower, "flag") {
			rest := strings.TrimLeft(line[4:], " \t\r\n\v\f")
			bits = 0
			if len(rest) > 0 && rest[0] == '*' {
				bits = ispellCross
				rest = rest[1:]
			} else if len(rest) > 0 && rest[0] == '~' {
				bits = ispellOnly
				rest = rest[1:]
			}
			if len(rest) > 0 && rest[0] == '\\' {
				rest = rest[1:]
			}
			if len(rest) > 0 && rest[0] < 128 && (len(rest) == 1 || rest[1] == ':' || rest[1] == '#' || ispellSpace(rest[1])) {
				old = true
				flag = rest[:1]
				return nil
			}
			return newFormat()
		}
		for _, prefix := range []string{"COMPOUNDFLAG", "COMPOUNDMIN", "PFX", "SFX"} {
			if strings.HasPrefix(line, prefix) {
				return newFormat()
			}
		}
		if !active {
			return nil
		}
		mask, replacement, ok := strings.Cut(lower, ">")
		if !ok {
			return nil
		}
		mask, err = ispellOldMask(ctx, mask)
		if err != nil {
			return err
		}
		if strings.HasPrefix(mask, "#") {
			return nil
		}
		strip, add, err := ispellOldReplacement(ctx, replacement)
		if err != nil {
			return err
		}
		if mask == "" || (strip == "" && add == "") {
			return nil
		}
		return b.addAffix(ctx, flag, bits, strip, add, mask, suffix)
	})
}
func ispellAlpha(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func ispellOldReplacement(ctx context.Context, s string) (string, string, error) {
	var strip, add strings.Builder
	state := 0
	for _, r := range s {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if r < 128 && ispellSpace(byte(r)) {
			continue
		}
		syntax := func() (string, string, error) { return "", "", dictionaryError("F0000", "syntax error") }
		switch state {
		case 0:
			if r == '-' {
				state = 1
			} else if ispellAlpha(r) || r == '\'' {
				if add.Len() < 8191 {
					add.WriteRune(r)
				}
				state = 3
			} else {
				return syntax()
			}
		case 1:
			if r == ',' {
				state = 2
			} else if ispellAlpha(r) {
				if strip.Len() < 8191 {
					strip.WriteRune(r)
				}
			} else {
				return syntax()
			}
		case 2:
			if r == '-' {
				return strip.String(), add.String(), nil
			}
			if !ispellAlpha(r) {
				return syntax()
			}
			if add.Len() < 8191 {
				add.WriteRune(r)
			}
			state = 3
		case 3:
			if r == '#' {
				return strip.String(), add.String(), nil
			}
			if !ispellAlpha(r) {
				return syntax()
			}
			if add.Len() < 8191 {
				add.WriteRune(r)
			}
		}
	}
	return strip.String(), add.String(), nil
}
func ispellOldMask(ctx context.Context, s string) (string, error) {
	if len(s) < 8192 && strings.IndexAny(s, " \t\r\n\v\f") < 0 {
		return s, ctx.Err()
	}
	var mask strings.Builder
	mask.Grow(min(len(s), 8191))
	for _, r := range s {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if r < 128 && ispellSpace(byte(r)) {
			continue
		}
		if mask.Len()+utf8.RuneLen(r) < 8192 {
			mask.WriteRune(r)
		}
	}
	return mask.String(), ctx.Err()
}
func (b *IspellCompiler) addAffix(ctx context.Context, flag string, bits uint8, strip, add, mask string, suffix bool) error {
	a := &ispellAffix{flag: flag, bits: bits, strip: strip, add: add, suffix: suffix}
	if bits&(ispellOnly|ispellPermit) != 0 && bits&ispellAll == 0 {
		a.bits |= ispellAll
	}
	if mask != "" && mask != "." {
		fast, ok := ispellSimpleMask(mask)
		if ok {
			a.mask = fast
		} else {
			pattern := mask + "$"
			if !suffix {
				pattern = "^" + mask
			}
			program, err := sqltype.CompilePGRegex(ctx, pattern, sqltype.PGRegexOptions{}, sqltype.CollationRef{ID: 950})
			if err != nil {
				return err
			}
			a.program = program
		}
	}
	b.affixes = append(b.affixes, a)
	return ctx.Err()
}
func ispellSimpleMask(pattern string) ([]ispellMask, bool) {
	var masks []ispellMask
	for len(pattern) > 0 {
		r, n := utf8.DecodeRuneInString(pattern)
		if ispellAlpha(r) {
			masks = append(masks, ispellMask{letters: pattern[:n]})
			pattern = pattern[n:]
			continue
		}
		if r != '[' {
			return nil, false
		}
		pattern = pattern[n:]
		negate := strings.HasPrefix(pattern, "^")
		if negate {
			pattern = pattern[1:]
		}
		end := strings.IndexByte(pattern, ']')
		if end < 0 || end == 0 && !negate {
			return nil, false
		}
		for _, r := range pattern[:end] {
			if !ispellAlpha(r) {
				return nil, false
			}
		}
		masks = append(masks, ispellMask{letters: pattern[:end], negate: negate})
		pattern = pattern[end+1:]
	}
	return masks, true
}
