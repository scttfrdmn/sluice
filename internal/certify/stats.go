// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

// Package certify holds the exact-binomial machinery behind certified error
// rates (PROJECT.md §3 "Quality constraint", §4 E0 intervals).
//
// The incomplete-beta, binomial-CDF, Hoeffding–Bentkus and minimum-size code
// in this file is copied from github.com/scttfrdmn/go-cascade
// internal/calibrate/stats.go (CC_PROMPT.md: copy, do not re-derive).
// Clopper–Pearson intervals are added in clopper.go. Section references
// (§2.9) in the copied comments are to go-cascade's design document.
package certify

import "math"

// regIncBeta is the regularised incomplete beta function I_x(a,b), evaluated
// with the Lentz continued fraction. It underpins the exact binomial tail.
func regIncBeta(a, b, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	la, _ := math.Lgamma(a + b)
	lb, _ := math.Lgamma(a)
	lc, _ := math.Lgamma(b)
	front := math.Exp(la - lb - lc + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betacf(a, b, x) / a
	}
	return 1 - front*betacf(b, a, 1-x)/b
}

func betacf(a, b, x float64) float64 {
	const (
		maxIter = 300
		eps     = 3e-14
		tiny    = 1e-300
	)
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		fm := float64(m)
		m2 := 2 * fm

		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c

		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// BinomCDF returns P(X <= k) for X ~ Binomial(n, p).
func BinomCDF(k, n int, p float64) float64 {
	switch {
	case k < 0:
		return 0
	case k >= n:
		return 1
	case p <= 0:
		return 1
	case p >= 1:
		return 0
	}
	return regIncBeta(float64(n-k), float64(k+1), 1-p)
}

// h1 is the Kullback-Leibler divergence between Bernoulli(a) and Bernoulli(b).
func h1(a, b float64) float64 {
	const tiny = 1e-12
	a = math.Min(math.Max(a, 0), 1)
	if b <= tiny || b >= 1-tiny {
		return math.Inf(1)
	}
	var t1, t2 float64
	if a > tiny {
		t1 = a * math.Log(a/b)
	}
	if a < 1-tiny {
		t2 = (1 - a) * math.Log((1-a)/(1-b))
	}
	return t1 + t2
}

// HoeffdingBentkus returns a valid p-value for the null hypothesis
// "true risk > alpha", given an empirical risk rhat over n exchangeable
// calibration points with per-point risk in [0,1].
//
// It is the minimum of a Hoeffding bound and a Bentkus bound; the Hoeffding
// term is tighter in the tails and the Bentkus term near the boundary, and
// taking the minimum is valid because each is separately valid.
//
// Sanity check worth remembering: with rhat = 0 the Hoeffding term reduces to
// exactly (1-alpha)^n, the probability of observing no errors when the true
// risk sits right at alpha.
func HoeffdingBentkus(rhat float64, n int, alpha float64) float64 {
	if n <= 0 {
		return 1
	}
	if rhat >= alpha {
		return 1 // no evidence the risk is below alpha
	}
	pHoeff := math.Exp(-float64(n) * h1(rhat, alpha))
	k := int(math.Ceil(float64(n) * rhat))
	pBentkus := math.E * BinomCDF(k, n, alpha)
	return math.Min(1, math.Min(pHoeff, pBentkus))
}

// MinCalibrationSize returns the smallest n at which a threshold could be
// certified at (alpha, delta) *even in the best case* — zero observed errors.
//
// It follows from the sanity check above: at rhat = 0 the Hoeffding term is
// exactly (1-alpha)^n, so rejecting the null needs (1-alpha)^n <= delta, i.e.
// n >= ln(delta)/ln(1-alpha). Below that no amount of good luck certifies
// anything, because the finite-sample penalty alone exceeds the budget.
//
// This is the honest floor for reading a small-sample certificate. A run that
// calibrates under it and fails to certify has learned nothing about the policy
// — the sample was too small to certify a *perfect* one. Distinguishing "the
// bound refused" from "there were not enough points to ask" matters wherever
// sample size is itself a variable, which is exactly the case when shadow
// sampling trades sample size for distributional correctness (§2.9).
func MinCalibrationSize(alpha, delta float64) int {
	if alpha <= 0 || alpha >= 1 || delta <= 0 || delta >= 1 {
		return 0
	}
	return int(math.Ceil(math.Log(delta) / math.Log(1-alpha)))
}
