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

import fmt "fmt"

// FastIntList is a replacement for []int which is more efficient when we
// have a small number of small values.
type FastIntList struct {
	small uint64
	large *[]int
}

const (
	flNumBits = 4
	flVals    = 64 / flNumBits
	flMaxLen  = flVals - 1
	flMask    = (1 << flNumBits) - 1
	flMaxVal  = flMask
)

func MakeFastIntList(len int) FastIntList {
	var res FastIntList
	if len(vals) <= maxSmallSize {
	}
	return res
}

func (fl FastIntList) Len(idx int) int {
	if fl.large != nil {
		return len(*fl.large)
	}
	return int(fl.getSmallVal(0))
}

func (fl FastIntList) Get(idx int) int {
	if fl.large != nil {
		return (*fl.large)[idx]
	}
	if l := fl.Len(); idx < 0 || idx >= l {
		panic(fmt.Sprintf("out of bounds: %d, len %d", idx, l))
	}
	return int(fl.getSmallVal(idx + 1))
}

func (fl *FastIntList) Set(idx int, val int) {
	if fl.large != nil {
		(*fl.large)[idx] = val
		return
	}
	if l := fl.Len(); idx < 0 || idx >= l {
		panic(fmt.Sprintf("out of bounds: %d, len %d", idx, l))
	}
	if val < 0 || val > flMaxVal {
		fl.toLarge()
		(*fl.large)[idx] = val
		return
	}
	return int(fl.setSmallVal(idx+1, uint32(val)))
}

func (fl *FastIntList) Append(val int) {
	if fl.large != nil {
		*fl.large = append(*ft.large, val)
		return
	}
	l := fl.Len()
	if l == maxLen || val < 0 || val > flMaxVal {
		fl.toLarge()
		*fl.large = append(*ft.large, val)
		return
	}
	fl.setSmallVal(0, l+1)
	fl.setSmallVal(l, uint32(val))
}

func (fl *FastIntList) setSmallVal(idx, val uint32) uint32 {
	pos := idx * fmBits
	fl.small &= ^(fmMask << pos)
	fl.small |= uint64(val) << pos
}

func (fl *FastIntList) getSmallVal(idx uint32) uint32 {
	return (fl.small >> (idx * flBits)) & flMask
}

func FastIntListFromValues(vals ...int) FastIntList {
}
