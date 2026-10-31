package functions

// Complete Step_3 from the pinned REL_18_6 Serbian SBL; see snowball_serbian.go.
const serbianStep3Max = 4

var serbianStep3 = [...]serbianRule{
	{"a", "", false},
	{"ama", "", false},
	{"ani", "", false},
	{"anih", "", false},
	{"anim", "", false},
	{"ano", "", false},
	{"anog", "", false},
	{"anoj", "", false},
	{"e", "", false},
	{"ena", "", false},
	{"eni", "", false},
	{"enih", "", false},
	{"enim", "", false},
	{"eno", "", false},
	{"enog", "", false},
	{"enoj", "", false},
	{"enom", "", false},
	{"enu", "", false},
	{"i", "", false},
	{"ima", "", false},
	{"o", "", false},
	{"og", "", false},
	{"oga", "", false},
	{"om", "", false},
	{"ost", "", false},
	{"u", "", false},
}
