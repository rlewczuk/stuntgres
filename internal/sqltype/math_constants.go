package sqltype

import "math"

// PostgreSQL REL_18_0 uses this rounded decimal constant and these operation
// orders, not multiplication by 180 followed by division by pi (or vice versa).
const radiansPerDegree = 0.0174532925199432957692

func mathConstant(name string, x float64) (float64, error) {
	var result float64
	switch name {
	case "cbrt":
		result = math.Cbrt(x)
	case "degrees":
		result = x / radiansPerDegree
	case "radians":
		result = x * radiansPerDegree
	case "erf":
		result = math.Erf(x)
	case "erfc":
		result = math.Erfc(x)
	case "gamma":
		if math.IsNaN(x) || math.IsInf(x, 1) {
			return x, nil
		}
		if math.IsInf(x, -1) || x == 0 || x < 0 && x == math.Trunc(x) {
			return 0, failure("22003", "value out of range: overflow")
		}
		if x < -33 {
			result = gammaNegativeReflection(x)
		} else {
			result = math.Gamma(x)
		}
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return 0, failure("22003", "value out of range: overflow")
		}
		if result == 0 {
			return 0, failure("22003", "value out of range: underflow")
		}
		return result, nil
	}
	if math.IsInf(result, 0) && !math.IsInf(x, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	// The erf/erfc wrappers permit zero and subnormal tails. The pinned gamma
	// oracle likewise accepts nonzero subnormals; Go does not expose C errno.
	if name != "erf" && name != "erfc" && result == 0 && x != 0 {
		return 0, failure("22003", "value out of range: underflow")
	}
	return result, nil
}

// Reflection with a compensated, scaled positive denominator avoids both
// intermediate overflow and large-argument Gamma approximation error. Reduce
// the sine argument before multiplying by pi to retain near-pole distances.
func gammaNegativeReflection(x float64) float64 {
	r := math.Remainder(x, 2)
	a := math.Abs(r)
	if a > .5 {
		a = 1 - a
	}
	if x < -200 {
		// Noninteger binary64 inputs of magnitude >=200 are at least 2^-45
		// from a pole. Thus |sin(pi*x)| >=2^-44 and Gamma(1-x)>=200!.
		// log(200!) >= integral_1^200 log(t)dt >860, so log|Gamma(x)|
		// is below log(pi)+44log(2)-860 <-828: safely below the
		// half-minimum-subnormal rounding boundary (~-745.13).
		return math.Copysign(0, r)
	}
	const piLow = 1.2246467991473532e-16 // pi - the nearest binary64 math.Pi.
	sineHigh, sineLow := gammaReducedSine(a, piLow)
	sineHigh = math.Copysign(sineHigh, r)
	sineLow = math.Copysign(1, r) * sineLow

	z := -x
	count := int(math.Floor(z))
	fraction := z - float64(count)
	base := 1 + fraction // Exact: input spacing here is coarser than [1,2).
	seed, seedLow := gammaPositiveSeed(fraction)
	high, exponent := math.Frexp(seed)
	low := math.Ldexp(seedLow, -exponent)
	for i := range count {
		integer := float64(i)
		factor := base + integer
		// TwoSum preserves the original fraction when the factor crosses
		// a binade; forming 1-x first would already have discarded it.
		integerHigh := factor - base
		factorLow := (base - (factor - integerHigh)) + (integer - integerHigh)
		high, low = gammaCompensatedProduct(high, low, factor, factorLow)
		m, e := math.Frexp(high)
		high, low, exponent = m, math.Ldexp(low, -e), exponent+e
	}
	// Scale sine too, keeping the denominator normal even beside a pole.
	sineMantissa, sineExponent := math.Frexp(sineHigh)
	sineLow = math.Ldexp(sineLow, -sineExponent)
	product := high * sineMantissa
	productLow := math.FMA(high, sineMantissa, -product) + low*sineMantissa + high*sineLow
	denominator := product + productLow
	denominatorLow := productLow - (denominator - product)
	quotient := math.Pi / denominator
	correction := (math.FMA(-quotient, denominator, math.Pi) + piLow - quotient*denominatorLow) / denominator
	return math.Ldexp(quotient+correction, -exponent-sineExponent)
}

// FMA retains the multiplication residual; FastTwoSum renormalizes it. The
// leading product dominates its residual (tiny sine-series tails may vanish).
func gammaCompensatedProduct(high, low, factor, factorLow float64) (float64, float64) {
	product := high * factor
	residual := math.FMA(high, factor, -product) + high*factorLow + low*factor + low*factorLow
	sum := product + residual
	return sum, residual - (sum - product)
}

// TwoSum followed by renormalization retains cancellation as well as addition
// residuals; unlike FastTwoSum, the leading operands need not be ordered.
func gammaCompensatedAdd(high, low, otherHigh, otherLow float64) (float64, float64) {
	sum := high + otherHigh
	other := sum - high
	residual := (high - (sum - other)) + (otherHigh - other) + low + otherLow
	result := sum + residual
	return result, residual - (result - sum)
}

func gammaCompensatedDivide(high, low, divisor, divisorLow float64) (float64, float64) {
	quotient := high / divisor
	correction := (math.FMA(-quotient, divisor, high) + low - quotient*divisorLow) / divisor
	result := quotient + correction
	return result, correction - (result - quotient)
}

// On [0,pi/2], the sine series through degree23 has remainder below1e-20.
// Retain its rounding residual too: correcting a rounded math.Sin result only
// for angle error does not recover the sine evaluation's own rounding error.
func gammaReducedSine(fraction, piLow float64) (float64, float64) {
	if fraction == .5 {
		return 1, 0
	}
	angle := math.Pi * fraction
	angleLow := math.FMA(math.Pi, fraction, -angle) + piLow*fraction
	square, squareLow := gammaCompensatedProduct(angle, angleLow, angle, angleLow)
	high, low := angle, angleLow
	term, termLow := angle, angleLow
	for i := range 11 {
		k := i + 1
		term, termLow = gammaCompensatedProduct(term, termLow, -square, -squareLow)
		term, termLow = gammaCompensatedDivide(term, termLow, float64(2*k*(2*k+1)), 0)
		high, low = gammaCompensatedAdd(high, low, term, termLow)
		// Subsequent terms shrink by at least sixfold here. Once this
		// term is below the expansion's precision, its tail is negligible.
		if math.Abs(term) < math.Abs(high)*1e-34 {
			break
		}
	}
	return high, low
}

// Small-domain rational coefficients from Go1.26.0 math/gamma.go (Cephes
// gamma.c, Release2.8): https://github.com/golang/go/blob/go1.26.0/src/math/gamma.go
// Coefficient low parts are the nearest binary64 residuals of the published
// decimal constants, independently derived with decimal precision110.
//
// Copyright 2010 The Go Authors. All rights reserved.
// Copyright 2009 The Go Authors.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//   - Redistributions of source code must retain the above copyright notice,
//     this list of conditions and the following disclaimer.
//   - Redistributions in binary form must reproduce the above copyright notice,
//     this list of conditions and the following disclaimer in the documentation
//     and/or other materials provided with the distribution.
//   - Neither the name of Google LLC nor the names of its contributors may be used
//     to endorse or promote products derived from this software without specific
//     prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
// LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
// CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
// SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
// CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
// ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// POSSIBILITY OF SUCH DAMAGE.
//
// Cephes Math Library Release 2.8: June, 2000
// Copyright 1984, 1987, 1989, 1992, 2000 by Stephen L. Moshier
// The readme file at http://netlib.sandia.gov/cephes/ says:
// Some software in this archive may be from the book Methods and Programs for
// Mathematical Functions (Prentice-Hall or Simon & Schuster International, 1989)
// or from the Cephes Mathematical Library, a commercial product. In either
// event, it is copyrighted by the author. What you see here may be used freely
// but it comes with no support or guarantee.
// The two known misprints in the book are repaired here in the source listings
// for the gamma function and the incomplete beta integral.
// Stephen L. Moshier
// moshier@na-net.ornl.gov
//
// Gamma(1+f) = P(f)/Q(f)/(1+f), f in[0,1]. Preserve Horner and both
// division residuals as a seed expansion; one rounded Gamma value would discard
// that information before the accurately compensated recurrence even starts.
func gammaPositiveSeed(fraction float64) (float64, float64) {
	if fraction == 0 || fraction == 1 {
		return 1, 0
	}
	p, pLow := 1.60119522476751861407e-04, 1.3315510927832196e-20
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 1.19135147006586384913e-03, 1.2976436495816761e-20)
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 1.04213797561761569935e-02, -8.667812505778274e-19)
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 4.76367800457137231464e-02, 2.0474122526178748e-18)
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 2.07448227648435975150e-01, -9.243771036593535e-18)
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 4.94214826801497100753e-01, 1.3869669787996216e-17)
	p, pLow = gammaPolynomialStep(p, pLow, fraction, 9.99999999999999996796e-01, -3.204e-18)
	q, qLow := -2.31581873324120129819e-05, 1.46295700411402e-21
	q, qLow = gammaPolynomialStep(q, qLow, fraction, 5.39605580493303397842e-04, 4.781192347383298e-20)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, -4.45641913851797240494e-03, 3.867287753008773e-19)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, 1.18139785222060435552e-02, 2.3790030483881785e-19)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, 3.58236398605498653373e-02, 4.600184215986199e-19)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, -2.34591795718243348568e-01, -3.031464428772568e-18)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, 7.14304917030273074085e-02, 5.633213838044408e-18)
	q, qLow = gammaPolynomialStep(q, qLow, fraction, 1.00000000000000000320e+00, 3.2e-18)
	high, low := gammaCompensatedDivide(p, pLow, q, qLow)
	return gammaCompensatedDivide(high, low, 1+fraction, 0)
}

func gammaPolynomialStep(high, low, x, coefficient, coefficientLow float64) (float64, float64) {
	high, low = gammaCompensatedProduct(high, low, x, 0)
	return gammaCompensatedAdd(high, low, coefficient, coefficientLow)
}
