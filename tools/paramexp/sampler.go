// Package main — sampler: Latin Hypercube, Sobol, and adaptive sampling.
package main

import (
	"fmt"
	"math"
	"sort"
)

// Sampler generates parameter vectors from a ParamSpace.
type Sampler interface {
	Sample(space ParamSpace, n int) []ParamVector
}

// LHSSampler implements Latin Hypercube Sampling for discrete spaces.
// Each dimension is divided into strata; one sample per stratum, shuffled.
type LHSSampler struct{}

func (LHSSampler) Sample(space ParamSpace, n int) []ParamVector {
	if n > space.Size() {
		n = space.Size() // can't sample more than the full space
	}
	dims := len(space.Params)
	// For each dimension, assign stratum indices [0..n-1], then shuffle.
	// The i-th sample takes stratum assignment[i][dim] from each dimension.
	assignment := make([][]int, dims)
	for d := range dims {
		perm := randPerm(n)
		assignment[d] = perm
	}
	vectors := make([]ParamVector, n)
	for i := range n {
		v := ParamVector{}
		for d, p := range space.Params {
			stratum := assignment[d][i]
			// Map stratum [0..n-1] to a value index [0..len(values)-1].
			valIdx := stratum * len(p.Values) / n
			if valIdx >= len(p.Values) {
				valIdx = len(p.Values) - 1
			}
			v[p.Name] = p.Values[valIdx]
		}
		vectors[i] = v
	}
	return vectors
}

// SobolSampler implements a Sobol quasi-random sequence for broad exploration.
// Uses direction numbers for up to 20 dimensions (sufficient for most params).
type SobolSampler struct {
	seed uint64
}

func (s SobolSampler) Sample(space ParamSpace, n int) []ParamVector {
	dims := len(space.Params)
	if dims > 20 {
		// Fall back to LHS for too many dimensions
		return LHSSampler{}.Sample(space, n)
	}
	vectors := make([]ParamVector, n)
	for i := range n {
		point := sobolPoint(uint32(i+1), dims)
		v := ParamVector{}
		for d, p := range space.Params {
			valIdx := int(point[d] * float64(len(p.Values)))
			if valIdx >= len(p.Values) {
				valIdx = len(p.Values) - 1
			}
			v[p.Name] = p.Values[valIdx]
		}
		vectors[i] = v
	}
	return vectors
}

// AdaptiveSampler adds experiments near promising (high-throughput) or
// unstable (high-variance) regions discovered in prior results.
type AdaptiveSampler struct {
	existing []ParamVector // already-sampled vectors
}

func (a *AdaptiveSampler) SampleNear(
	space ParamSpace,
	results []storedResult,
	n int,
	objective string, // metric to optimize (maximize)
) []ParamVector {
	// Sort results by objective (descending = best first).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Metrics[objective] > results[j].Metrics[objective]
	})
	// Take the top-k results and explore their neighbors.
	topK := min(n, len(results))
	if topK == 0 {
		return nil
	}
	neighbors := make([]ParamVector, 0, n)
	for i := 0; i < topK && len(neighbors) < n; i++ {
		base := results[i].Vector
		// For each dimension, try adjacent values.
		for _, p := range space.Params {
			curIdx := indexOf(p.Values, base[p.Name])
			for _, delta := range []int{-1, 1} {
				nIdx := curIdx + delta
				if nIdx < 0 || nIdx >= len(p.Values) {
					continue
				}
				neighbor := copyVector(base)
				neighbor[p.Name] = p.Values[nIdx]
				if !containsVector(a.existing, neighbor) && !containsVector(neighbors, neighbor) {
					neighbors = append(neighbors, neighbor)
					a.existing = append(a.existing, neighbor)
					if len(neighbors) >= n {
						return neighbors
					}
				}
			}
		}
	}
	return neighbors
}

// ---- helpers ----

func randPerm(n int) []int {
	// Simple LCG-based permutation (deterministic; seed from time would add
	// nondeterminism, but reproducibility is better for benchmarks).
	if n <= 1 {
		return seq(n)
	}
	perm := seq(n)
	// Fisher-Yates with a simple PRNG
	seed := uint64(12345)
	for i := n - 1; i > 0; i-- {
		seed = seed*6364136223846793005 + 1442695040888963407
		j := int(seed>>33) % (i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm
}

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func copyVector(v ParamVector) ParamVector {
	c := make(ParamVector, len(v))
	for k, val := range v {
		c[k] = val
	}
	return c
}

func containsVector(slice []ParamVector, v ParamVector) bool {
	for _, s := range slice {
		if vectorsEqual(s, v) {
			return true
		}
	}
	return false
}

func vectorsEqual(a, b ParamVector) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		if vb, ok := b[k]; !ok || va != vb {
			return false
		}
	}
	return true
}

func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

// ---- Sobol direction numbers (first 20 dimensions, max bit 32) ----

var sobolDir = [20]uint32{
	0x80000000, 0x40000000, 0x20000000, 0x10000000, 0x08000000,
	0x04000000, 0x02000000, 0x01000000, 0x00800000, 0x00400000,
	0x00200000, 0x00100000, 0x00080000, 0x00040000, 0x00020000,
	0x00010000, 0x00008000, 0x00004000, 0x00002000, 0x00001000,
}

func sobolPoint(index uint32, dims int) []float64 {
	result := make([]float64, dims)
	for d := 0; d < dims && d < len(sobolDir); d++ {
		var x uint32
		i := index
		for bit := 0; i > 0; bit++ {
			if i&1 != 0 {
				x ^= sobolDir[d] >> bit
			}
			i >>= 1
		}
		result[d] = float64(x) / float64(math.MaxUint32+1)
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// suppress unused import warning
var _ = fmt.Sprintf
