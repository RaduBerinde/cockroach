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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionStringRoundtrip(t *testing.T) {
	versions := []Version{
		{Major: 1, Minor: 2},
		{Major: 2, Minor: 0, Patch: 3},
		{Major: 1, Minor: 2, Patch: 2},
		{Major: 24, Minor: 1},
		{Major: 1, Minor: 2, Internal: 2},
		{Major: 1, Minor: 2, Patch: 3, Internal: 15},
		{Major: 24, Minor: 1, PreFinalizationUpgradeStep: 2},
		{Major: 24, Minor: 2, Patch: 10, PreFinalizationUpgradeStep: 15},
	}
	for _, v := range versions {
		s := v.String()
		v2, err := ParseVersion(s)
		require.NoError(t, err)
		require.Equal(t, v, v2)
	}
}

func TestVersionCmp(t *testing.T) {
	// We use an ordered list of versions and make sure that all pair-wise
	// comparisons are as expected.
	ordered := []string{
		"0.0-pre.2",
		"0.0-pre.4",
		"0.0",
		"0.0-1",
		"0.0-15",
		"0.0.1-pre.1",
		"0.0.1-pre.2",
		"0.0.1",
		"0.0.1-15",
		"2.0-pre.2",
		"2.0-pre.4",
		"2.0",
		"2.0-2",
		"2.0-4",
		"23.2",
		"24.1-pre.2",
		"24.1-pre.4",
		"24.1",
		"24.2-pre.2",
		"24.2-pre.4",
		"24.2",
	}
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			x := MustParseVersion(ordered[i])
			y := MustParseVersion(ordered[j])
			require.Equalf(t, -1, x.Cmp(y), "x: %s  y: %s", x, y)
			require.Equalf(t, +1, y.Cmp(x), "x: %s  y: %s", x, y)
			require.Truef(t, x.Less(y), "x: %s  y: %s", x, y)
			require.Truef(t, x.LessEq(y), "x: %s  y: %s", x, y)
			require.Truef(t, x.LessEq(x), "x: %s  y: %s", x, y)
			require.Truef(t, y.AtLeast(x), "x: %s  y: %s", x, y)
		}
	}
}
