// Copyright 2020 The Cockroach Authors.
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
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
	"github.com/cockroachdb/errors"
)

// VersionSetting is the setting type that allows users to control the cluster
// version. It starts off at an initial version and takes into account the
// current version to validate proposed updates. This is (necessarily) tightly
// coupled with the setting implementation in pkg/clusterversion, and it's done
// through the VersionSettingImpl interface. We rely on the implementation to
// decode to and from raw bytes, and to perform the validation itself. The
// VersionSetting itself is then just the tiny shim that lets us hook into the
// rest of the settings machinery (by interfacing with Values, to load and store
// cluster versions).
type VersionSetting struct {
	common
}

var _ NonMaskedSetting = &VersionSetting{}

// initialize initializes cluster version. Before this method has been called,
// usage of the version is illegal and leads to a fatal error.
func (v *VersionSetting) initialize(
	ctx context.Context, version roachpb.Version, sv *Values,
) error {
	if ver := v.activeVersionOrEmpty(ctx, sv); ver != (clusterversion.ClusterVersion{}) {
		// Allow initializing a second time as long as it's not regressing.
		//
		// This is useful in tests that use MakeTestingClusterSettings() which
		// initializes the version, and the start a server which again
		// initializes it once more.
		//
		// It's also used in production code during bootstrap, where the version
		// is first initialized to MinSupportedVersion and then re-initialized to
		// BootstrapVersion (=LatestVersion).
		if version.Less(ver.Version) {
			return errors.AssertionFailedf("cannot initialize version to %s because already set to: %s",
				version, ver)
		}
		if version == ver.Version {
			// Don't trigger callbacks, etc, a second time.
			return nil
		}
		// Now version > ver.Version.
	}
	if err := validateBinaryVersions(version, sv); err != nil {
		return err
	}

	// Return the serialized form of the new version.
	newV := clusterversion.ClusterVersion{Version: version}
	encoded, err := protoutil.Marshal(&newV)
	if err != nil {
		return err
	}
	v.setInternal(ctx, sv, encoded)
	return nil
}

// activeVersion returns the cluster's current active version: the minimum
// cluster version the caller may assume is in effect.
//
// activeVersion fatals if the version has not been initialized.
func (v *VersionSetting) activeVersion(
	ctx context.Context, sv *Values,
) clusterversion.ClusterVersion {
	ver := v.activeVersionOrEmpty(ctx, sv)
	if ver == (clusterversion.ClusterVersion{}) {
		log.Fatalf(ctx, "version not initialized")
	}
	return ver
}

// activeVersionOrEmpty is like activeVersion, but returns an empty version if
// the active version was not initialized.
func (v *VersionSetting) activeVersionOrEmpty(
	ctx context.Context, sv *Values,
) clusterversion.ClusterVersion {
	encoded := v.getInternal(sv)
	if encoded == nil {
		return clusterversion.ClusterVersion{}
	}
	var curVer clusterversion.ClusterVersion
	// NB: our linter requires using protoutil.Unmarshal here, but it causes an
	// unnecessary allocation. This and other uses in this file are exceptions.
	// TODO(pavelkalinnikov): don't parse proto on each time reading this setting.
	if err := curVer.Unmarshal(encoded.([]byte)); err != nil {
		log.Fatalf(ctx, "%v", err)
	}
	return curVer
}

// Validate checks whether a version update is permitted. It takes in the
// old and the proposed new value (both in encoded form). This is called by
// SET CLUSTER SETTING.
func (v *VersionSetting) Validate(ctx context.Context, sv *Values, oldV, newV []byte) error {
	newCV, err := clusterversion.Decode(oldV)
	if err != nil {
		return err
	}

	if err := validateBinaryVersions(newCV.Version, sv); err != nil {
		return err
	}

	oldCV, err := clusterversion.Decode(newV)
	if err != nil {
		return err
	}

	// Versions cannot be downgraded.
	if newCV.Version.Less(oldCV.Version) {
		return errors.Errorf(
			"versions cannot be downgraded (attempting to downgrade from %s to %s)",
			oldCV.Version, newCV.Version)
	}

	// Prevent cluster version upgrade until cluster.preserve_downgrade_option
	// is reset.
	if downgrade := PreserveDowngradeVersion.Get(sv); downgrade != "" {
		return errors.Errorf(
			"cannot upgrade to %s: cluster.preserve_downgrade_option is set to %s",
			newCV.Version, downgrade)
	}

	return nil
}

func validateBinaryVersions(ver roachpb.Version, sv *Values) error {
	if sv.minSupportedVersion == (roachpb.Version{}) {
		panic("MinSupportedVersion not set")
	}
	if sv.latestVersion.Less(ver) {
		return errors.Errorf("cannot upgrade to %s: node running %s",
			ver, sv.latestVersion)
	}
	if ver.Less(sv.minSupportedVersion) {
		return errors.Errorf("node at %s cannot run %s (minimum version is %s)",
			sv.latestVersion, ver, sv.minSupportedVersion)
	}
	return nil
}

// SettingsListDefault returns the value that should be presented by
// `./cockroach gen settings-list`.
func (v *VersionSetting) SettingsListDefault() string {
	return clusterversion.Latest.Version().String()
}

// Typ is part of the Setting interface.
func (*VersionSetting) Typ() string {
	// This is named "m" (instead of "v") for backwards compatibility reasons.
	return VersionSettingValueType
}

// VersionSettingValueType is the value type string (m originally for
// "migration") used in the system.settings table.
const VersionSettingValueType = "m"

// String is part of the Setting interface.
func (v *VersionSetting) String(sv *Values) string {
	encV := []byte(v.Get(sv))
	if encV == nil {
		panic("unexpected nil value")
	}
	cv, err := clusterversion.Decode(encV)
	if err != nil {
		panic(err)
	}
	return cv.String()
}

// Encoded is part of the NonMaskedSetting interface.
func (v *VersionSetting) Encoded(sv *Values) string {
	return v.Get(sv)
}

// EncodedDefault is part of the NonMaskedSetting interface.
func (v *VersionSetting) EncodedDefault() string {
	return encodedDefaultVersion
}

const encodedDefaultVersion = "unsupported"

// DecodeToString decodes and renders an encoded value.
func (v *VersionSetting) DecodeToString(encoded string) (string, error) {
	if encoded == encodedDefaultVersion {
		return encodedDefaultVersion, nil
	}
	cv, err := clusterversion.Decode([]byte(encoded))
	if err != nil {
		return "", err
	}
	return cv.String(), nil
}

// Get retrieves the encoded value (in string form) in the setting. It panics if
// set() has not been previously called.
func (v *VersionSetting) Get(sv *Values) string {
	encV := v.getInternal(sv)
	if encV == nil {
		panic(fmt.Sprintf("missing value for version setting in slot %d", v.slot))
	}
	return string(encV.([]byte))
}

// getInternal returns the setting's current value.
func (v *VersionSetting) getInternal(sv *Values) interface{} {
	return sv.getGeneric(v.slot)
}

// setInternal updates the setting's value in the provided Values container.
func (v *VersionSetting) setInternal(ctx context.Context, sv *Values, newVal interface{}) {
	sv.setGeneric(ctx, v.slot, newVal)
}

// setToDefault is part of the extendingSetting interface. This is a no-op for
// VersionSetting. They don't have defaults that they can go back to at any
// time.
func (v *VersionSetting) setToDefault(ctx context.Context, sv *Values) {}

// RegisterVersionSetting adds the provided version setting to the global
// registry.
func RegisterVersionSetting(
	class Class, key InternalKey, desc string, opts ...SettingOption,
) *VersionSetting {
	setting := &VersionSetting{}
	register(class, key, desc, setting)
	setting.apply(opts)
	return setting
}

var Version = RegisterVersionSetting(
	ApplicationLevel,
	"version",
	"set the active cluster version in the format '<major>.<minor>'", // hide optional `-<internal>,
	WithPublic,
	WithReportable(true),
)

var PreserveDowngradeVersion = RegisterStringSetting(
	ApplicationLevel,
	"cluster.preserve_downgrade_option",
	"disable (automatic or manual) cluster version upgrade from the specified version until reset",
	"",
	WithValidateString(func(sv *Values, s string) error {
		if sv == nil || s == "" {
			return nil
		}
		clusterVersion := Version.activeVersion(context.TODO(), sv).Version
		downgradeVersion, err := roachpb.ParseVersion(s)
		if err != nil {
			return err
		}

		// cluster.preserve_downgrade_option can only be set to the current cluster version.
		if downgradeVersion != clusterVersion {
			return errors.Errorf(
				"cannot set cluster.preserve_downgrade_option to %s (cluster version is %s)",
				s, clusterVersion)
		}
		return nil
	}),
	WithReportable(true),
	WithPublic,
)
