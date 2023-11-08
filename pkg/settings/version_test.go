// Copyright 2023 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package settings_test

import (
	"context"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/stretchr/testify/require"
)

func BenchmarkClusterVersionSettingIsActive(b *testing.B) {
	s := cluster.MakeTestingClusterSettings()
	ctx := context.Background()
	active := true
	for i := 0; i < b.N; i++ {
		active = s.Version().IsActive(ctx, clusterversion.Latest) && active
	}
	require.True(b, active)
}
