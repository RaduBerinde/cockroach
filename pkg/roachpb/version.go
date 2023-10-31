// Copyright 2017 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package roachpb

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"

	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/redact"
)

// Cmp compares two Versions; returns
//
//	-1 if v is older than otherV,
//	 0 if v is identical to otherV,
//	+1 if v is newer than otherV.
func (v Version) Cmp(otherV Version) int {
	if c := cmp.Compare(v.Major, otherV.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Minor, otherV.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Patch, otherV.Patch); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Internal, otherV.Internal); c != 0 {
		return c
	}
	if c := cmp.Compare(v.PreFinalizationUpgradeStep, otherV.PreFinalizationUpgradeStep); c != 0 {
		// A version that is not finalized comes *before* the finalized version.
		switch {
		case v.PreFinalizationUpgradeStep == 0:
			return +1
		case otherV.PreFinalizationUpgradeStep == 0:
			return -1
		default:
			return c
		}
	}
	return 0
}

// Less returns whether the receiver is older than the parameter.
func (v Version) Less(otherV Version) bool {
	return v.Cmp(otherV) < 0
}

// LessEq returns whether the receiver is older than or equal to the parameter.
func (v Version) LessEq(otherV Version) bool {
	return v.Cmp(otherV) <= 0
}

// AtLeast returns true if the receiver is at least as new as the parameter.
func (v Version) AtLeast(otherV Version) bool {
	return v.Cmp(otherV) >= 0
}

// String implements the fmt.Stringer interface.
func (v Version) String() string { return redact.StringWithoutMarkers(v) }

// SafeFormat implements the redact.SafeFormatter interface.
func (v Version) SafeFormat(p redact.SafePrinter, _ rune) {
	p.Printf("%d.%d", v.Major, v.Minor)
	if v.Patch != 0 {
		// Patch is not used, but we internally support it in case it will be used in
		// the future.
		p.Printf(".%d", v.Patch)
	}
	if v.Internal != 0 {
		p.Printf("-%d", v.Internal)
	} else if v.PreFinalizationUpgradeStep != 0 {
		p.Printf("-pre.%d", v.PreFinalizationUpgradeStep)
	}
}

// IsFinal returns true if this is a final version (as opposed to a
// transitional internal version during upgrade).
func (v Version) IsFinal() bool {
	return v.Internal == 0 && v.PreFinalizationUpgradeStep == 0
}

// PrettyPrint returns the value in a format that makes it apparent whether or
// not it is a fence version.
func (v Version) PrettyPrint() string {
	// If we're a version greater than v20.2 and have an odd internal version,
	// we're a fence version. See fenceVersionFor in pkg/upgrade to understand
	// what these are.
	if !v.LessEq(Version{Major: 20, Minor: 2}) {
		fenceVersion := v.Internal%2 == 1 || v.PreFinalizationUpgradeStep%2 == 1
		if fenceVersion {
			return fmt.Sprintf("%v(fence)", v)
		}
	}
	return v.String()
}

// ParseVersion parses a Version from a string in one of the following forms:
//   - "<major>.<minor>"
//   - "<major>.<minor>-<internal>"
//   - "<major>.<minor>-pre.<upgrade-step>"
func ParseVersion(s string) (Version, error) {
	//                          res[1]    res[2]      res[4]      res[6]         res[7]
	//                            |         |           |           |              |
	//                            V         V           V           V              v
	r := regexp.MustCompile(`^([0-9]+)\.([0-9]+)(|\.([0-9]+))(|-([0-9]+)|-pre\.([0-9]+))$`)
	res := r.FindStringSubmatch(s)
	if res == nil {
		return Version{}, errors.Errorf("invalid version %s", s)
	}
	// Note: at least one of res[6],res[7] will be empty
	parts := []string{res[1], res[2], res[4], res[6], res[7]}
	values := make([]int32, len(parts))
	for i, part := range parts {
		if part != "" {
			n, err := strconv.ParseInt(part, 10, 32)
			if err != nil {
				return Version{}, errors.Wrapf(err, "invalid version %s", s)
			}
			values[i] = int32(n)
		}
	}
	return Version{
		Major:                      values[0],
		Minor:                      values[1],
		Patch:                      values[2],
		Internal:                   values[3],
		PreFinalizationUpgradeStep: values[4],
	}, nil
}

// MustParseVersion calls ParseVersion and panics on error.
func MustParseVersion(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}
