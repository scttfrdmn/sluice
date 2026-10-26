// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package certify

// Interval is a closed interval on [0, 1].
type Interval struct {
	Lo, Hi float64
}

// betaQuantile inverts the regularised incomplete beta I_x(a, b) = p by
// bisection; regIncBeta is monotone in x.
func betaQuantile(p, a, b float64) float64 {
	lo, hi := 0.0, 1.0
	for range 200 {
		mid := (lo + hi) / 2
		if regIncBeta(a, b, mid) < p {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-12 {
			break
		}
	}
	return (lo + hi) / 2
}

// ClopperPearson is the exact two-sided (1 − alpha) interval for a binomial
// proportion with k successes in n trials.
func ClopperPearson(k, n int, alpha float64) Interval {
	if n <= 0 {
		return Interval{0, 1}
	}
	var iv Interval
	if k > 0 {
		iv.Lo = betaQuantile(alpha/2, float64(k), float64(n-k+1))
	}
	if k < n {
		iv.Hi = betaQuantile(1-alpha/2, float64(k+1), float64(n-k))
	} else {
		iv.Hi = 1
	}
	return iv
}

// UpperBound is the one-sided (1 − alpha) Clopper–Pearson upper bound on a
// proportion: the certified error rate when k errors are seen in n.
func UpperBound(k, n int, alpha float64) float64 {
	if n <= 0 {
		return 1
	}
	if k >= n {
		return 1
	}
	return betaQuantile(1-alpha, float64(k+1), float64(n-k))
}
