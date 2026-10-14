package sqltype

import (
	"context"
	"fmt"
	"strconv"
)

// RegTSResolver and RegTSFormatter are owned by one statement publication.
type RegTSResolver interface {
	ResolveRegconfig(context.Context, string) (uint32, error)
	ResolveRegdictionary(context.Context, string) (uint32, error)
}
type RegTSFormatter interface {
	FormatRegconfig(uint32) (string, error)
	FormatRegdictionary(uint32) (string, error)
}

func IsRegTS(id ID) bool { return id == Regconfig || id == Regdictionary }

// RegTSCastPair admits only released carrier edges and explicit character I/O.
func RegTSCastPair(source, target ID) bool {
	return IsRegTS(source) && (source == target || target == Oid || target == Int4 || target == Int8 || IsCharacter(target)) ||
		IsRegTS(target) && (source == Oid || source == Int2 || source == Int4 || source == Int8 || source == Text || source == VarChar)
}
func RegTSValue(d *Descriptor, id uint32) (Value, error) {
	if d == nil || !IsRegTS(d.id) || !d.Concrete() {
		return Value{}, fmt.Errorf("invalid text search alias descriptor")
	}
	return Value{typ: d, oid: id, integer: 1}, nil
}
func (v Value) RegTSOID() (uint32, error) {
	if v.null || v.typ == nil || !IsRegTS(v.typ.BaseType().id) || v.integer != 1 {
		return 0, fmt.Errorf("not a text search identity")
	}
	return v.oid, nil
}
func (d *Descriptor) ContainsRegTS() bool {
	if d == nil {
		return false
	}
	p := d.BaseType()
	if p.IsArray() {
		return p.element.ContainsRegTS()
	}
	if p.IsComposite() {
		for _, m := range p.composite.members {
			if m.Usage.Type.ContainsRegTS() {
				return true
			}
		}
	}
	return IsRegTS(p.id)
}

var regTSOIDInput = Descriptor{id: Oid, name: "oid"}

func regTSInput(ctx context.Context, d *Descriptor, text string, env InputEnvironment) (Value, error) {
	if text == "-" {
		return RegTSValue(d, 0)
	}
	numeric := len(text) > 0
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		v, err := regTSOIDInput.oidInput(text)
		if err != nil {
			return Value{}, err
		}
		return RegTSValue(d, v.oid)
	}
	if env.RegTS == nil {
		return Value{}, fmt.Errorf("text search input lacks catalog authority")
	}
	var id uint32
	var err error
	if d.id == Regconfig {
		id, err = env.RegTS.ResolveRegconfig(ctx, text)
	} else {
		id, err = env.RegTS.ResolveRegdictionary(ctx, text)
	}
	if err != nil {
		return Value{}, err
	}
	return RegTSValue(d, id)
}
func regTSNumericOutput(id uint32) string {
	if id == 0 {
		return "-"
	}
	return strconv.FormatUint(uint64(id), 10)
}
