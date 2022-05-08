// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tree

// AlterTenantSetClusterSetting represents an ALTER TENANT
// SET CLUSTER SETTING statement.
type AlterTenantSetClusterSetting struct {
	Name  string
	Value Expr
	// TenantID is the ID of the tenant, or DNull if TenantAll is true.
	TenantID Expr
	// TenantAll is true when we are setting an all-tenant override (which applies
	// to all tenants which don't have a tenant-specific override for the setting).
	TenantAll bool
}

// Format implements the NodeFormatter interface.
func (n *AlterTenantSetClusterSetting) Format(ctx *FmtCtx) {
	ctx.WriteString("ALTER TENANT ")
	if n.TenantAll {
		ctx.WriteString("ALL")
	} else {
		ctx.FormatNode(n.TenantID)
	}
	ctx.WriteByte(' ')
	// Reuse the formatting logic from SET CLUSTER SETTING.
	ctx.FormatNode(&SetClusterSetting{
		Name:  n.Name,
		Value: n.Value,
	})
}

// ShowTenantClusterSetting represents a SHOW CLUSTER SETTING ... FOR TENANT statement.
type ShowTenantClusterSetting struct {
	Name     string
	TenantID Expr
}

// Format implements the NodeFormatter interface.
func (node *ShowTenantClusterSetting) Format(ctx *FmtCtx) {
	// Reuse the formatting code from SHOW CLUSTER SETTING.
	ctx.FormatNode(&ShowClusterSetting{Name: node.Name})
	ctx.WriteString(" FOR TENANT ")
	ctx.FormatNode(node.TenantID)
}

// ShowTenantClusterSettingList represents a SHOW CLUSTER SETTINGS FOR TENANT statement.
type ShowTenantClusterSettingList struct {
	// All indicates whether to include non-public settings in the output.
	All      bool
	TenantID Expr
}

// Format implements the NodeFormatter interface.
func (node *ShowTenantClusterSettingList) Format(ctx *FmtCtx) {
	// Reuse the formatting code from SHOW CLUSTER SETTINGS.
	ctx.FormatNode(&ShowClusterSettingList{All: node.All})
	ctx.WriteString(" FOR TENANT ")
	ctx.FormatNode(node.TenantID)
}
