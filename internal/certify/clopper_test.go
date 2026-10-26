// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package certify

import (
	"math"
	"testing"
)

func TestClopperPearsonKnownValues(t *testing.T) {
	// Reference values from scipy.stats.beta.ppf (exact Clopper–Pearson, 95%).
	cases := []struct {
		k, n   int
		lo, hi float64
	}{
		{0, 10, 0, 0.3084971},
		{5, 10, 0.1870860, 0.8129140},
		{10, 10, 0.6915029, 1},
		{37, 100, 0.2755666, 0.4723516},
	}
	for _, c := range cases {
		iv := ClopperPearson(c.k, c.n, 0.05)
		if math.Abs(iv.Lo-c.lo) > 1e-6 || math.Abs(iv.Hi-c.hi) > 1e-6 {
			t.Errorf("CP(%d,%d) = [%.7f, %.7f], want [%.7f, %.7f]", c.k, c.n, iv.Lo, iv.Hi, c.lo, c.hi)
		}
	}
}

func TestUpperBoundZeroErrors(t *testing.T) {
	// With 0 errors in n, the one-sided bound is 1 - alpha^(1/n).
	n, alpha := 59, 0.05
	want := 1 - math.Pow(alpha, 1/float64(n))
	if got := UpperBound(0, n, alpha); math.Abs(got-want) > 1e-9 {
		t.Errorf("UpperBound(0,%d) = %v, want %v", n, got, want)
	}
}
