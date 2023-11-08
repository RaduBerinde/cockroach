// Copyright 2023 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package settings

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
)

// VersionHandle is a helper that provides a streamlined API around the cluster
// version setting. It is associated with a specific Values instance.
type VersionHandle struct {
	sv *Values
}

// Initialize initializes the global cluster version. Before this method has
// been called, usage of the cluster version (through Handle) is illegal and
// leads to a fatal error.
//
// Initialization of the cluster version is tightly coupled with the setting of
// the active cluster version (`Handle.SetActiveVersion` below). Look towards
// there for additional commentary.
func (h VersionHandle) Initialize(ctx context.Context, ver roachpb.Version) error {
	return Version.initialize(ctx, ver, h.sv)
}

// AssertInitialized checks whether Initialize() has been called yet. This
// is used in test code to assert that an initial cluster version has
// been set when that matters.
func (h VersionHandle) AssertInitialized(ctx context.Context) {
	_ = Version.activeVersion(ctx, h.sv)
}

// ActiveVersion returns the cluster's current active version: the minimum
// cluster version the caller may assume is in effect.
//
// ActiveVersion fatals if the cluster version setting has not been
// initialized (through `Initialize()`).
func (h VersionHandle) ActiveVersion(ctx context.Context) clusterversion.ClusterVersion {
	return Version.activeVersion(ctx, h.sv)
}

// ActiveVersionOrEmpty is like ActiveVersion, but returns an empty version
// if the active version was not initialized.
func (h VersionHandle) ActiveVersionOrEmpty(ctx context.Context) clusterversion.ClusterVersion {
	return Version.activeVersionOrEmpty(ctx, h.sv)
}

// IsActive returns true if the features of the supplied version key are
// active at the running version. In other words, if a particular version
// `v` returns true from this method, it means that you're guaranteed that
// all of the nodes in the cluster have running binaries that are at least
// as new as `v`, and that those nodes will never be downgraded to a binary
// with a version less than `v`.
//
// If this returns true then all nodes in the cluster will eventually see
// this version. However, this is not atomic because version gates (for a
// given version) are pushed through to each node concurrently. Because of
// this, nodes should not be gating proper handling of remotely initiated
// requests that their binary knows how to handle on this state. The
// following example shows why this is important:
//
//	The cluster restarts into the new version and the operator issues a SET
//	VERSION, but node1 learns of the bump 10 seconds before node2, so during
//	that window node1 might be receiving "old" requests that it itself
//	wouldn't issue any more. Similarly, node2 might be receiving "new"
//	requests that its binary must necessarily be able to handle (because the
//	SET VERSION was successful) but that it itself wouldn't issue yet.
//
// This is still a useful method to have as node1, in the example above, can
// use this information to know when it's safe to start issuing "new"
// outbound requests. When receiving these "new" inbound requests, despite
// not seeing the latest active version, node2 is aware that the sending
// node has, and it will too, eventually.
func (h VersionHandle) IsActive(ctx context.Context, key clusterversion.Key) bool {
	return Version.activeVersion(ctx, h.sv).IsActive(key)
}

// LatestVersion returns the latest cluster version understood by this binary.
func (h VersionHandle) LatestVersion() roachpb.Version {
	return h.sv.latestVersion
}

// MinSupportedVersion returns the earliest cluster version that can
// interoperate with this binary.
func (h VersionHandle) MinSupportedVersion() roachpb.Version {
	return h.sv.minSupportedVersion
}

// SetActiveVersion lets the caller set the given cluster version as the
// currently active one. When a new active version is set, all subsequent
// calls to `ActiveVersion`, `IsActive`, etc. will reflect as much. The
// ClusterVersion supplied here is one retrieved from other node.
//
// This has a very specific intended usage pattern, and is probably only
// appropriate for usage within the BumpClusterVersion RPC and during server
// initialization.
//
// NB: It's important to note that this method is tightly coupled to cluster
// version initialization (through `Initialize` above) and the version
// persisted to disk. Specifically the following invariant must hold true:
//
//	If a version vX is active on a given server, upon restart, the version
//	that is immediately active must be >= vX (in practice it'll almost
//	always be vX).
//
// This is currently achieved by always durably persisting the target
// cluster version to the store local keys.StoreClusterVersionKey() before
// setting it to be active. This persisted version is also consulted during
// node restarts when initializing the cluster version, as seen by this
// node.
func (h VersionHandle) SetActiveVersion(
	ctx context.Context, cv clusterversion.ClusterVersion,
) error {
	// We only perform binary version validation here. SetActiveVersion is only
	// called on cluster versions received from other nodes (where `SET CLUSTER
	// SETTING version` was originally called). The stricter form of validation
	// happens there. SetActiveVersion is simply the cluster version bump that
	// follows from it.
	if err := validateBinaryVersions(cv.Version, h.sv); err != nil {
		return err
	}

	encoded, err := protoutil.Marshal(&cv)
	if err != nil {
		return err
	}

	Version.setInternal(ctx, h.sv, encoded)
	return nil

}

// SetOnChange installs a callback that's invoked when the active cluster
// version changes. The callback should avoid doing long-running or blocking
// work; it's called on the same goroutine handling all cluster setting
// updates.
func (h VersionHandle) SetOnChange(
	fn func(ctx context.Context, newVersion clusterversion.ClusterVersion),
) {
	Version.SetOnChange(h.sv, func(ctx context.Context) {
		fn(ctx, h.ActiveVersion(ctx))
	})
}
