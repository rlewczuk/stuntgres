package xml

import (
	"context"
	"strings"
)

type xsltParamPair struct{ name, value string }

func xsltSplitParams(ctx context.Context, s string) ([]xsltParamPair, error) {
	p := parser{ctx: ctx}
	var out []xsltParamPair
	for start := 0; start < len(s); {
		equal := start
		for equal < len(s) && s[equal] != '=' {
			if equal&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			equal++
		}
		if equal == len(s) {
			break
		}
		end := equal + 1
		for end < len(s) && s[end] != ',' {
			if end&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			end++
		}
		out = checkedAppend(&p, out, xsltParamPair{s[start:equal], s[equal+1 : end]})
		if p.err != nil {
			return nil, p.err
		}
		start = end + 1
	}
	return out, ctx.Err()
}

type xsltScope struct {
	parent    *xsltScope
	values    map[string]xpathValue
	inherited map[string]bool
}

func xsltNewScope(parent *xsltScope) *xsltScope { return &xsltScope{parent: parent} }
func (s *xsltScope) define(key string, value xpathValue) {
	if s.values == nil {
		s.values = make(map[string]xpathValue)
	}
	s.values[key] = value
	delete(s.inherited, key)
}
func (s *xsltScope) defineParam(key string, value xpathValue) {
	s.define(key, value)
	if s.inherited == nil {
		s.inherited = make(map[string]bool)
	}
	s.inherited[key] = true
}
func (s *xsltScope) inheritedParam(key string) bool {
	for ; s != nil; s = s.parent {
		if _, ok := s.values[key]; ok {
			return s.inherited[key]
		}
	}
	return false
}
func (s *xsltScope) lookup(key string) (xpathValue, bool) {
	for ; s != nil; s = s.parent {
		if v, ok := s.values[key]; ok {
			return v, true
		}
	}
	return xpathValue{}, false
}
func (r *xsltRuntime) userParameters(pairs []xsltParamPair) error {
	f := r.frame
	old := f.environment
	f.environment = &xsltEnvironment{runtime: r, current: r.source.root, userValues: true}
	f.node, f.position, f.size, f.bindings = r.source.root, 1, 1, nil
	defer func() { f.environment = old }()
	for _, pair := range pairs {
		if !f.poll() {
			return f.p.err
		}
		name := xsltName{Local: pair.name}
		if strings.HasPrefix(pair.name, "{") {
			uri, local, ok := strings.Cut(pair.name[1:], "}")
			if !ok {
				r.fail()
			} else {
				name.URI, name.Local = uri, local
			}
		} else {
			prefix, local := xsltSplitQName(pair.name)
			if prefix != "" {
				uri, ok := r.program.root.namespaces[prefix]
				if !ok {
					r.fail()
				}
				name.Prefix, name.URI = prefix, uri
			}
			name.Local = local
		}
		key := name.key()
		_, duplicate := r.user[key]
		if duplicate {
			r.fail()
		}
		if declaration := r.program.globals[key]; declaration != nil && declaration.op == "variable" {
			continue
		}
		expr, err := compileXPath(f.p.ctx, pair.value)
		if err != nil {
			return err
		}
		value := f.eval(expr)
		if f.p.err != nil {
			return f.p.err
		}
		if !duplicate {
			r.user[key] = value
		}
	}
	return nil
}
