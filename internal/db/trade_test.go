package db

import (
	"math/rand"
	"testing"
)

// The warp graph must be fully connected — an unreachable sector is a
// black hole for whoever docks there — with sane, symmetric, bounded
// degrees. Pure function, so this needs no database.
func TestBuildTradeWarps(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		const n = 500
		warps := buildTradeWarps(rand.New(rand.NewSource(seed)), n)

		// Symmetry and degree bounds.
		for i := 1; i <= n; i++ {
			if len(warps[i]) == 0 || len(warps[i]) > 6 {
				t.Fatalf("seed %d: sector %d has %d warps", seed, i, len(warps[i]))
			}
			for _, w := range warps[i] {
				if w < 1 || w > n || w == i {
					t.Fatalf("seed %d: sector %d has bad warp %d", seed, i, w)
				}
				back := false
				for _, ret := range warps[w] {
					if ret == i {
						back = true
					}
				}
				if !back {
					t.Fatalf("seed %d: warp %d→%d is one-way", seed, i, w)
				}
			}
		}

		// Connectivity from sector 1 (everyone's home).
		seen := make([]bool, n+1)
		queue := []int{1}
		seen[1] = true
		count := 0
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			count++
			for _, w := range warps[cur] {
				if !seen[w] {
					seen[w] = true
					queue = append(queue, w)
				}
			}
		}
		if count != n {
			t.Fatalf("seed %d: only %d of %d sectors reachable", seed, count, n)
		}
	}
}
