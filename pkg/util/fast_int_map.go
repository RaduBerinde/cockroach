// Copyright 2018 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package util

import (
	"bytes"
	"fmt"
	"math/bits"
	"sort"

	"golang.org/x/tools/container/intsets"
)

// FastIntMap is a replacement for map[int]int which is more efficient when the
// values are small. It can be passed by value (but Copy must be used for
// independent modification of copies).
type FastIntMap struct {
	small [fmWords]uint64
	large map[int]int
}

// Empty returns true if the map is empty.
func (m FastIntMap) Empty() bool {
	return m.small == [fmWords]uint64{} && len(m.large) == 0
}

// Copy returns a FastIntMap that can be independently modified.
func (m FastIntMap) Copy() FastIntMap {
	if m.large == nil {
		return FastIntMap{small: m.small}
	}
	largeCopy := make(map[int]int, len(m.large))
	for k, v := range m.large {
		largeCopy[k] = v
	}
	return FastIntMap{large: largeCopy}
}

// Set maps a key to the given value.
func (m *FastIntMap) Set(key, val int) {
	if m.large == nil {
		if key >= 0 && key < fmVals && val >= 0 && val <= fmMaxValue {
			m.setSmallVal(uint32(key), int32(val))
			return
		}
		m.large = m.toLarge()
		m.small = [fmWords]uint64{}
	}
	m.large[key] = val
}

// Unset unmaps the given key.
func (m *FastIntMap) Unset(key int) {
	if m.large == nil {
		if key < 0 || key >= fmVals {
			return
		}
		m.setSmallVal(uint32(key), -1)
	}
	delete(m.large, key)
}

// Get returns the current value mapped to key, or ok=false if the
// key is unmapped.
func (m FastIntMap) Get(key int) (value int, ok bool) {
	if m.large == nil {
		if key < 0 || key >= fmVals {
			return -1, false
		}
		val := m.getSmallVal(uint32(key))
		return int(val), (val != -1)
	}
	value, ok = m.large[key]
	return value, ok
}

// Len returns the number of keys in the map.
func (m FastIntMap) Len() int {
	if m.large != nil {
		return len(m.large)
	}
	res := 0
	for w := 0; w < fmWords; w++ {
		v := m.small[w]
		// We want to count the number of non-zero groups. To do this, we OR all
		// the bits of each group into the low-bit of that group, apply a fmMask
		// selecting just those low bits and count the number of 1s.
		// To OR the bits efficiently, we first OR the high half of each group into
		// the low half of each group, and repeat.
		// Note: this code assumes that fmBits is a power of two.
		for i := uint32(fmBits / 2); i > 0; i /= 2 {
			v |= (v >> i)
		}
		res += bits.OnesCount64(v & fmGroupLowBitMask)
	}
	return res
}

// MaxKey returns the maximum key that is in the map. If the map
// is empty, returns ok=false.
func (m FastIntMap) MaxKey() (_ int, ok bool) {
	if m.large == nil {
		for w := fmWords - 1; w >= 0; w-- {
			if val := m.small[w]; val != 0 {
				// Example (with fmBits = 4)
				//   pos:   3    2    1    0
				//   bits:  0000 0000 0010 0000
				// To get the left-most non-zero group, we calculate how many groups are
				// covered by the leading zeros.
				pos := fmValsPerWord - 1 - bits.LeadingZeros64(val)/fmBits
				return w*fmValsPerWord + pos, true
			}
		}
		return 0, false
	}
	if len(m.large) == 0 {
		return 0, false
	}
	max := intsets.MinInt
	for k := range m.large {
		if max < k {
			max = k
		}
	}
	return max, true
}

// MaxValue returns the maximum value that is in the map. If the map
// is empty, returns ok=false.
func (m FastIntMap) MaxValue() (_ int, ok bool) {
	if m.large == nil {
		// In the small case, all values are positive.
		max := -1
		for w := 0; w < fmWords; w++ {
			if m.small[w] != 0 {
				// To optimize for small maps, we stop when the rest of the values are
				// unset. See the comment in MaxKey.
				numVals := fmValsPerWord - bits.LeadingZeros64(m.small[w])/fmBits
				for i := 0; i < numVals; i++ {
					val := int(m.getSmallVal(uint32(w*fmValsPerWord + i)))
					// NB: val is -1 here if this key isn't in the map.
					if max < val {
						max = val
					}
				}
			}
		}
		if max == -1 {
			return 0, false
		}
		return max, true
	}
	if len(m.large) == 0 {
		return 0, false
	}
	max := intsets.MinInt
	for _, v := range m.large {
		if max < v {
			max = v
		}
	}
	return max, true
}

// ForEach calls the given function for each key/value pair in the map (in
// arbitrary order).
func (m FastIntMap) ForEach(fn func(key, val int)) {
	if m.large == nil {
		for i := 0; i < fmVals; i++ {
			if val := m.getSmallVal(uint32(i)); val != -1 {
				fn(i, int(val))
			}
		}
	} else {
		for k, v := range m.large {
			fn(k, v)
		}
	}
}

// String prints out the contents of the map in the following format:
//   map[key1:val1 key2:val2 ...]
// The keys are in ascending order.
func (m FastIntMap) String() string {
	var buf bytes.Buffer
	buf.WriteString("map[")
	first := true

	if m.large != nil {
		keys := make([]int, 0, len(m.large))
		for k := range m.large {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			if !first {
				buf.WriteByte(' ')
			}
			first = false
			fmt.Fprintf(&buf, "%d:%d", k, m.large[k])
		}
	} else {
		for i := 0; i < fmVals; i++ {
			if val := m.getSmallVal(uint32(i)); val != -1 {
				if !first {
					buf.WriteByte(' ')
				}
				first = false
				fmt.Fprintf(&buf, "%d:%d", i, val)
			}
		}
	}
	buf.WriteByte(']')
	return buf.String()
}

// These constants determine the "small" representation: we pack <fmVals>
// values of <fmBits> bits into <fmWords> 64-bit words. Each value is 0 if the
// corresponding key is not set, otherwise it is the value+1.
//
// It's desirable for efficiency that fmBits, fmValsPerWord are powers of two.
//
// The current settings support a map from keys in [0, 31] to values in [0, 14].
// Note that one value is reserved to indicate an unmapped element.
const (
	fmWords       = 2
	fmBits        = 4
	fmValsPerWord = 64 / fmBits             // 16
	fmVals        = fmWords * fmValsPerWord // 32
	fmMask        = (1 << fmBits) - 1
	fmMaxValue    = fmMask - 1
	// Mask for the low bits of each group: 0001 0001 0001 ...
	fmGroupLowBitMask = 0x1111111111111111
)

// Returns -1 if the value is unmapped.
func (m FastIntMap) getSmallVal(idx uint32) int32 {
	word := idx / fmValsPerWord
	pos := (idx % fmValsPerWord) * fmBits
	return int32((m.small[word]>>pos)&fmMask) - 1
}

func (m *FastIntMap) setSmallVal(idx uint32, val int32) {
	word := idx / fmValsPerWord
	pos := (idx % fmValsPerWord) * fmBits
	// Clear out any previous value
	m.small[word] &= ^(fmMask << pos)
	m.small[word] |= uint64(val+1) << pos
}

func (m *FastIntMap) toLarge() map[int]int {
	res := make(map[int]int, fmVals)
	for i := 0; i < fmVals; i++ {
		val := m.getSmallVal(uint32(i))
		if val != -1 {
			res[i] = int(val)
		}
	}
	return res
}
