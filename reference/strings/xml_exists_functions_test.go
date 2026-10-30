package reference_test

import "testing"

func xmlConditionCases(conditions []string) []struct {
	path string
	want bool
} {
	out := make([]struct {
		path string
		want bool
	}, 0, len(conditions)*2)
	for _, condition := range conditions {
		out = append(out, struct {
			path string
			want bool
		}{"/r[" + condition + "]", true}, struct {
			path string
			want bool
		}{"/r[not(" + condition + ")]", false})
	}
	return out
}
func TestReferenceXMLExistsOperators(t *testing.T) {
	xmlExistsCases(t, `<r><a>1</a><a>2</a><b>2</b></r>`, xmlConditionCases([]string{
		`count(a|a)=2`, `count(a|b)=3`, `a=b and a!=b`, `a=2`, `a<2`, `a=true()`, `not(missing=true())`,
		`1+2*3=7`, `(1+2)*3=9`, `-3 mod 2=-1`, `5 div 2=2.5`, `1 div 0 > 0`, `not((0 div 0)=(0 div 0))`,
		`not(false()) and (true() or $unbound)`, `not(false() and $unbound)`, `not(missing='')`, `not(missing=0)`, `missing=false()`,
		`not(missing!='')`, `a!=b and a=b`, `---2=-2`, `2=1+1`, `count((a|b)[1])=1`, `string((a|b)[1])='1'`, `count((a|b)/parent::r)=1`,
		`true()=2`, `true()!=0`, `a>1`, `2<a[1]+2`, `a<=b`, `a>=b`, `a!=3`, `not(a=3)`, `10 mod 3=1`, `-10 mod -3=-1`,
	}))
}
func TestReferenceXMLExistsCoreFunctions(t *testing.T) {
	xmlExistsCases(t, `<r xml:lang='en-US'><a>2</a><a>3</a></r>`, xmlConditionCases([]string{
		`count(a)=2`, `a[position()=last()]=3`, `local-name(.)='r'`, `namespace-uri(.)=''`, `name(.)='r'`,
		`string(a)='2'`, `concat('a','b','c')='abc'`, `starts-with('abc','ab')`, `contains('abc','bc')`, `substring-before('abc','b')='a'`, `substring-after('abc','b')='c'`, `substring('12345',2,3)='234'`,
		`string-length('é𝄞')=2`, `normalize-space('  a  b  ')='a b'`, `translate('bar','abc','ABC')='BAr'`,
		`boolean(a)`, `not(missing)`, `true()`, `not(false())`, `lang('en')`, `not(lang('eng'))`,
		`number(' 2 ')=2`, `sum(a)=5`, `floor(-1.2)=-2`, `ceiling(-1.2)=-1`, `round(-1.5)=-1`,
		`string()='23'`, `string-length()=2`, `normalize-space()='23'`, `number()=23`, `local-name()='r'`, `namespace-uri()=''`, `name()='r'`,
		`substring('12345',1.5,2.6)='234'`, `substring('12345',0,3)='12'`, `substring('12345',0 div 0,3)=''`, `substring('12345',1,0 div 0)=''`,
		`substring('12345',-42,1 div 0)='12345'`, `substring('12345',-1 div 0,1 div 0)=''`, `substring('12345',-1 div 0)='12345'`,
		`normalize-space(' a ')=' a '`, `translate('abca','aabc','XY')='XX'`, `translate('abc','ab','X')='Xc'`,
		`not(number('+1')=1)`, `number('1e2')=100`, `1e2=100`, `number('1e')=1`, `1e=1`, `number('1e+')=1`, `1e+=1`, `number('-')=0`, `not(number('0x10')=16)`, `number('-.5')=-0.5`,
		`round(1.5)=2`, `round(-0.5)=0`, `1 div round(-0.5)<0`, `string(1 div 0)='Infinity'`, `string(-1 div 0)='-Infinity'`, `string(0 div 0)='NaN'`, `sum(missing)=0`,
		`string(0.0000001)='1e-07'`, `string(1000000000000000000000)='1e+21'`, `string(0.000001)='1e-06'`, `string(0.00001)='0.00001'`, `string(1000000000)='1000000000'`, `string(1000000001)='1000000001'`, `string(10000000000)='1e+10'`, `string(2147483648)='2.147483648e+09'`, `string(1 div 3)='0.333333333333333'`, `string(1.2345678901234567)='1.234567890123457'`, `substring-before('abc','')=''`, `substring-after('abc','')='abc'`, `starts-with('abc','')`,
	}))
	xmlExistsCases(t, `<r xml:lang='en-US'><a xml:id='a'>a b</a><a xml:id='b' xml:lang='fr'/><a>a</a></r>`, xmlConditionCases([]string{
		`count(id('a b a'))=2`, `count(id(a))=2`, `a[1][lang('EN')]`, `a[2][lang('fr')]`, `not(a[2][lang('en')])`, `a[3][lang('en-us')]`,
	}))
}
