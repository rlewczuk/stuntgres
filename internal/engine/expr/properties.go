package expr

// Volatility orders expressions from Immutable to Volatile.
type Volatility uint8

const (
	Immutable Volatility = iota
	Stable
	Volatile
)

// Properties are the analysis properties of a bound expression.
type Properties struct {
	Volatility                                       Volatility
	RowDependent, ParameterDependent, StageDependent bool
	DomainDependent                                  bool
	// RuntimeDomainConversion prevents folding a checked conversion or any
	// enclosing expression. It does not require a VALUE frame or alter volatility.
	RuntimeDomainConversion bool
}

// MergeProperties combines the properties of two operands: the higher
// volatility wins and each dependence flag is set if either operand sets it.
func MergeProperties(a, b Properties) Properties {
	if b.Volatility > a.Volatility {
		a.Volatility = b.Volatility
	}
	a.RowDependent = a.RowDependent || b.RowDependent
	a.ParameterDependent = a.ParameterDependent || b.ParameterDependent
	a.StageDependent = a.StageDependent || b.StageDependent
	a.DomainDependent = a.DomainDependent || b.DomainDependent
	a.RuntimeDomainConversion = a.RuntimeDomainConversion || b.RuntimeDomainConversion
	return a
}

// IntrinsicPropertyRule states the properties a builtin call must carry.
type IntrinsicPropertyRule uint8

const (
	ExactStable IntrinsicPropertyRule = iota
	ExactVolatile
	VolatileOnly
	ExactImmutable
)

// IntrinsicProperties returns the properties a call under rule starts with.
// An unknown rule yields the zero Properties.
func IntrinsicProperties(rule IntrinsicPropertyRule) Properties {
	switch rule {
	case ExactImmutable:
		return Properties{Volatility: Immutable}
	case ExactStable:
		return Properties{Volatility: Stable}
	case ExactVolatile, VolatileOnly:
		return Properties{Volatility: Volatile}
	}
	return Properties{}
}

// PropertiesMatch reports whether properties satisfy rule. The exact rules
// require the volatility and no dependence; VolatileOnly checks volatility
// alone.
func PropertiesMatch(properties Properties, rule IntrinsicPropertyRule) bool {
	switch rule {
	case ExactImmutable:
		return properties == (Properties{Volatility: Immutable})
	case ExactStable:
		return properties == (Properties{Volatility: Stable})
	case ExactVolatile:
		return properties == (Properties{Volatility: Volatile})
	case VolatileOnly:
		return properties.Volatility == Volatile
	}
	return false
}
