package main

import (
	"fmt"
	"math/rand"
	"sort"
)

var vals []int

// moveTopKToFront swaps elements in the range [start, end) so that all
// elements in the range [start, k) are all <= than all elements in the range
// [k, end).
func moveTopKToFront(data sort.Interface, start, end, k int, rng *rand.Rand) {
	//fmt.Println("Move", vals[start:end], k)
	if k < start || k > end {
		panic(fmt.Sprintf("k (%d) outside of range [%d, %d)", k, start, end))
	}
	if k == start || k == end {
		return
	}

	// The strategy is to choose a random pivot and partition the data into
	// three regions: elements < pivot, elements == pivot, elements > pivot.
	//
	// We first partition into two regions: elements <= pivot and
	// elements > pivot and further refine the first region if necessary.

	// Choose a random pivot and move it to the front.
	data.Swap(start, start+rng.Intn(end-start))
	pivot := start
	l, r := start+1, end
	for l < r {
		// Invariants:
		//  - elements in the range [start, l) are <= pivot
		//  - elements in the range [r, end) are > pivot
		for l < r && !data.Less(pivot, l) {
			l++
		}
		for l < r && data.Less(pivot, r-1) {
			r--
		}
		if l == r {
			break
		}
		data.Swap(l, r-1)
		l++
		r--
	}
	mid := l
	//fmt.Println("After pivoting", vals[start:mid], vals[mid:end])
	// Everything in the range [start, mid) is <= than the pivot.
	// Everything in the range [mid, end) is > than the pivot.
	if k >= mid {
		// In this case, we eliminated at least the pivot (and all elements
		// equal to it).
		moveTopKToFront(data, mid, end, k, rng)
		return
	}

	// If we eliminated a decent amount of elements, we can recurse on [0, mid).
	// If the elements were distinct we would do this unconditionally, but in
	// general we could have a lot of elements equal to the pivot.
	if end-mid > (end-start)/4 {
		moveTopKToFront(data, start, mid, k, rng)
		return
	}

	// Now we work on the range [0, mid). Move everything that is equal to the
	// pivot to the back.
	data.Swap(pivot, mid-1)
	pivot = mid - 1
	for l, r = start, pivot-1; l <= r; {
		if data.Less(l, pivot) {
			l++
		} else {
			data.Swap(l, r)
			r--
		}
	}
	// Now everything in the range [start, l) is < than the pivot. Everything in the
	// range [l, mid) is equal to the pivot. If k is in the [l, mid) range we
	// are done, otherwise we recurse on [start, l).
	if k <= l {
		moveTopKToFront(data, start, l, k, rng)
	}
}

func MoveTopKToFront(data sort.Interface, k int) {
	if data.Len() <= k {
		return
	}
	// We want the call to be deterministic so we use a predictable seed.
	r := rand.New(rand.NewSource(int64(data.Len()*1000 + k)))
	moveTopKToFront(data, 0, data.Len(), k, r)
}

func main() {
	tests := [][]int{
		{1, 1, 1},
		{1, 2, 1, 1},
		{1, 2, 1, 2},
		{2, 2, 1, 2},
		{0, 2, 9, 11, 6, 1, 7, 1, 5, 1, 4, 3, 0, 11},
	}
	for _, data := range tests {
		fmt.Println()
		for k := 0; k <= len(data); k++ {
			vals = append([]int(nil), data...)
			MoveTopKToFront(sort.IntSlice(vals), k)
			fmt.Println(vals[0:k], "", vals[k:len(vals)])
			for l := 0; l < k; l++ {
				for r := k; r < len(data); r++ {
					if vals[l] > vals[r] {
						fmt.Println("FAIL", l, r)
					}
				}
			}
		}
	}
}
