package sqltype

import (
	"context"
	"fmt"
	"math/bits"
	"strings"

	"stuntgres/internal/sqlstate"
)

// ACLAuthority is inert engine-owned provenance, not a role index. Its nonzero
// size makes independent engines' embedded authorities distinguishable.
type ACLAuthority struct{ identity byte }

const ACLPrivilegeLetters = "arwdDxtXUCTcsAm"
const ACLPrivilegeMask uint32 = 1<<len(ACLPrivilegeLetters) - 1

type aclitemDatum struct {
	authority                             *ACLAuthority
	grantee, grantor, privileges, options uint32
}

// ACLItemResolver captures one immutable role snapshot for an input operation.
// The warning hook queues an operation-owned warning, never a user callback.
type ACLItemResolver interface {
	ACLItemAuthority() *ACLAuthority
	ResolveACLRole(context.Context, string) (uint32, error)
	WarnACLGrantor(context.Context)
}

type ACLItemFormatter interface {
	FormatACLRole(*ACLAuthority, uint32) (string, error)
}

func ACLItemValue(d *Descriptor, authority *ACLAuthority, grantee, grantor, privileges, options uint32) (Value, error) {
	if d == nil || d.id != ACLItem || !d.Concrete() || authority == nil || privileges & ^ACLPrivilegeMask != 0 || options & ^privileges != 0 {
		return Value{}, fmt.Errorf("invalid aclitem value")
	}
	return Value{typ: d, aclitem: aclitemDatum{authority, grantee, grantor, privileges, options}}, nil
}

// ACLItem returns checked inline identities and both released privilege masks.
func (v Value) ACLItem() (*ACLAuthority, uint32, uint32, uint32, uint32, error) {
	d := v.aclitem
	if v.null || v.typ == nil || !v.typ.Concrete() || v.typ.BaseType().id != ACLItem || !d.valid() {
		return nil, 0, 0, 0, 0, fmt.Errorf("not an aclitem value")
	}
	return d.authority, d.grantee, d.grantor, d.privileges, d.options, nil
}

func (d aclitemDatum) valid() bool {
	return d.authority != nil && d.privileges & ^ACLPrivilegeMask == 0 && d.options & ^d.privileges == 0
}

func (d *Descriptor) ContainsACLItem() bool {
	if d == nil {
		return false
	}
	physical := d.BaseType()
	if r := physical.RangeType(); r != nil {
		return r.rangeSubtype.ContainsACLItem()
	}
	if physical.IsArray() {
		return physical.element.ContainsACLItem()
	}
	if physical.IsComposite() {
		for _, member := range physical.composite.members {
			if member.Usage.Type.ContainsACLItem() {
				return true
			}
		}
	}
	return physical.id == ACLItem
}

func aclSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
func aclAlpha(c byte) bool    { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func aclNameChar(c byte) bool { return aclAlpha(c) || c >= '0' && c <= '9' || c == '_' || c >= 128 }

// aclName mirrors REL_18_0 getid, including concatenated quote segments and
// an unterminated quoted final grantor. The bound is decoded bytes, not runes.
func aclName(text string, offset int) (string, int, error) {
	for offset < len(text) && aclSpace(text[offset]) {
		offset++
	}
	start := offset
	for offset < len(text) && aclNameChar(text[offset]) {
		if offset-start == 63 {
			return "", 0, aclNameTooLong()
		}
		offset++
	}
	if offset == len(text) || text[offset] != '"' {
		end := offset
		for offset < len(text) && aclSpace(text[offset]) {
			offset++
		}
		return text[start:end], offset, nil
	}
	var decoded [63]byte
	length := copy(decoded[:], text[start:offset])
	inQuotes := false
	for offset < len(text) && (inQuotes || text[offset] == '"' || aclNameChar(text[offset])) {
		c := text[offset]
		offset++
		if c == '"' {
			if !inQuotes {
				inQuotes = true
				continue
			}
			if offset == len(text) || text[offset] != '"' {
				inQuotes = false
				continue
			}
			offset++
		}
		if length == len(decoded) {
			return "", 0, aclNameTooLong()
		}
		decoded[length] = c
		length++
	}
	for offset < len(text) && aclSpace(text[offset]) {
		offset++
	}
	return string(decoded[:length]), offset, nil
}

func aclNameTooLong() error {
	return sqlstate.SoftInputError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42622", Message: "identifier too long", Detail: "Identifier must be less than 64 characters."})
}

func aclHint(message, hint string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Hint: hint}
}

func (d *Descriptor) aclitemInput(ctx context.Context, text string, env InputEnvironment) (Value, error) {
	resolver := env.ACLItem
	if resolver == nil || resolver.ACLItemAuthority() == nil {
		return Value{}, fmt.Errorf("missing aclitem input authority")
	}
	name, offset, err := aclName(text, 0)
	if err != nil {
		return Value{}, err
	}
	if offset == len(text) || text[offset] != '=' {
		if name != "user" && name != "group" {
			return Value{}, aclHint(`unrecognized key word: "`+name+`"`, `ACL key word must be "group" or "user".`)
		}
		name, offset, err = aclName(text, offset)
		if err != nil {
			return Value{}, err
		}
		if name == "" {
			return Value{}, aclHint("missing name", `A name must follow the "group" or "user" key word.`)
		}
	}
	if offset == len(text) || text[offset] != '=' {
		return Value{}, failure("22P02", `missing "=" sign`)
	}
	offset++
	var privileges, options, last uint32
	for offset < len(text) && (aclAlpha(text[offset]) || text[offset] == '*') {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		c := text[offset]
		offset++
		if c == '*' {
			options |= last
		} else {
			index := strings.IndexByte(ACLPrivilegeLetters, c)
			if index < 0 {
				return Value{}, failure("22P02", `invalid mode character: must be one of "`+ACLPrivilegeLetters+`"`)
			}
			last = 1 << index
		}
		privileges |= last
	}
	var grantee uint32
	if name != "" {
		grantee, err = resolver.ResolveACLRole(ctx, name)
		if err != nil {
			return Value{}, err
		}
	}
	grantor := uint32(10)
	if offset < len(text) && text[offset] == '/' {
		name, offset, err = aclName(text, offset+1)
		if err != nil {
			return Value{}, err
		}
		if name == "" {
			return Value{}, failure("22P02", `a name must follow the "/" sign`)
		}
		grantor, err = resolver.ResolveACLRole(ctx, name)
		if err != nil {
			return Value{}, err
		}
	} else {
		resolver.WarnACLGrantor(ctx)
	}
	for offset < len(text) && aclSpace(text[offset]) {
		offset++
	}
	if offset != len(text) {
		return Value{}, failure("22P02", "extra garbage at the end of the ACL specification")
	}
	return ACLItemValue(d, resolver.ACLItemAuthority(), grantee, grantor, privileges, options)
}

func aclOutputNameSize(name string) (length int, quoted bool) {
	length = len(name)
	for i := range len(name) {
		quoted = quoted || name[i] >= 128 || !aclNameChar(name[i])
		if name[i] == '"' {
			length++
		}
	}
	if quoted {
		length += 2
	}
	return length, quoted
}

func writeACLName(output *strings.Builder, name string, quoted bool) {
	if !quoted {
		output.WriteString(name)
		return
	}
	output.WriteByte('"')
	for i := range len(name) {
		if name[i] == '"' {
			output.WriteByte('"')
		}
		output.WriteByte(name[i])
	}
	output.WriteByte('"')
}

func (v Value) aclitemOutput(env OutputEnvironment) (string, error) {
	authority, grantee, grantor, privileges, options, err := v.ACLItem()
	if err != nil {
		return "", err
	}
	if env.ACLItem == nil {
		return "", fmt.Errorf("missing aclitem output authority")
	}
	granteeName := ""
	if grantee != 0 {
		granteeName, err = env.ACLItem.FormatACLRole(authority, grantee)
		if err != nil {
			return "", err
		}
	}
	grantorName, err := env.ACLItem.FormatACLRole(authority, grantor)
	if err != nil {
		return "", err
	}
	granteeLength, quoteGrantee := aclOutputNameSize(granteeName)
	grantorLength, quoteGrantor := aclOutputNameSize(grantorName)
	var output strings.Builder
	output.Grow(granteeLength + grantorLength + bits.OnesCount32(privileges) + bits.OnesCount32(options) + 2)
	writeACLName(&output, granteeName, quoteGrantee)
	output.WriteByte('=')
	for i := range len(ACLPrivilegeLetters) {
		bit := uint32(1) << i
		if privileges&bit != 0 {
			output.WriteByte(ACLPrivilegeLetters[i])
			if options&bit != 0 {
				output.WriteByte('*')
			}
		}
	}
	output.WriteByte('/')
	writeACLName(&output, grantorName, quoteGrantor)
	return output.String(), nil
}

func aclEquality(ctx context.Context, a, b Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !a.aclitem.valid() || !b.aclitem.valid() {
		return false, fmt.Errorf("invalid aclitem equality value")
	}
	return a.aclitem == b.aclitem, nil
}
