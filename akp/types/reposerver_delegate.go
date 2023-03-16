package types

import (
	"context"

	argocdv1 "github.com/akuity/api-client-go/pkg/api/gen/argocd/v1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type AkpRepoServerDelegate struct {
	ControlPlane   types.Bool   `tfsdk:"control_plane"`
	ManagedCluster types.Object `tfsdk:"managed_cluster"`
}

var (
	repoServerDelegateAttrTypes = map[string]attr.Type{
		"control_plane": types.BoolType,
		"managed_cluster": types.ObjectType{
			AttrTypes: repoServerDelegateManagedClusterAttrTypes,
		},
	}
)

func MergeRepoServerDelegate(state *AkpRepoServerDelegate, plan *AkpRepoServerDelegate) (*AkpRepoServerDelegate, diag.Diagnostics) {
	diags := diag.Diagnostics{}
	res := &AkpRepoServerDelegate{}

	if plan.ControlPlane.IsUnknown() {
		res.ControlPlane = state.ControlPlane
	} else if plan.ControlPlane.IsNull() {
		res.ControlPlane = types.BoolNull()
	} else {
		res.ControlPlane = plan.ControlPlane
	}

	if plan.ManagedCluster.IsUnknown() {
		res.ManagedCluster = state.ManagedCluster
	} else if plan.ManagedCluster.IsNull() {
		res.ManagedCluster = types.ObjectNull(repoServerDelegateManagedClusterAttrTypes)
	} else {
		var stateRepoServerDelegateMC, planRepoServerDelegateMC AkpRepoServerDelegateManagedCluster
		diags.Append(state.ManagedCluster.As(context.Background(), &stateRepoServerDelegateMC, basetypes.ObjectAsOptions{
			UnhandledNullAsEmpty: true,
		})...)
		diags.Append(plan.ManagedCluster.As(context.Background(), &planRepoServerDelegateMC, basetypes.ObjectAsOptions{})...)
		resRepoServerDelegateMC, d := MergeRepoServerDelegateManagedCluster(&stateRepoServerDelegateMC, &planRepoServerDelegateMC)
		diags.Append(d...)
		res.ManagedCluster, d = types.ObjectValueFrom(context.Background(), repoServerDelegateManagedClusterAttrTypes, resRepoServerDelegateMC)
		diags.Append(d...)
	}

	return res, diags
}

func (x *AkpRepoServerDelegate) UpdateObject(p *argocdv1.RepoServerDelegate) diag.Diagnostics {
	diags := diag.Diagnostics{}
	var d diag.Diagnostics
	if p == nil {
		diags.AddError("Conversion Error", "*argocdv1.RepoServerDelegate is <nil>")
		return diags
	}
	x.ControlPlane = types.BoolValue(p.ControlPlane)

	if p.ManagedCluster == nil || p.ManagedCluster.String() == "" {
		x.ManagedCluster = types.ObjectNull(repoServerDelegateManagedClusterAttrTypes)
	} else {
		managedClusterObject := &AkpRepoServerDelegateManagedCluster{}
		diags.Append(managedClusterObject.UpdateObject(p.ManagedCluster)...)
		x.ManagedCluster, d = types.ObjectValueFrom(context.Background(), repoServerDelegateManagedClusterAttrTypes, managedClusterObject)
		diags.Append(d...)
	}

	return diags
}

func (x *AkpRepoServerDelegate) As(target *argocdv1.RepoServerDelegate) diag.Diagnostics {
	diags := diag.Diagnostics{}

	if x.ControlPlane.IsNull() {
		target.ControlPlane = false
	} else if !x.ControlPlane.IsUnknown() {
		target.ControlPlane = x.ControlPlane.ValueBool()
	}

	if x.ManagedCluster.IsNull() {
		target.ManagedCluster = nil
	} else if !x.ManagedCluster.IsUnknown() {
		managedCluster := AkpRepoServerDelegateManagedCluster{}
		targetManagedCluster := argocdv1.ManagedCluster{}
		diags.Append(x.ManagedCluster.As(context.Background(), managedCluster, basetypes.ObjectAsOptions{})...)
		diags.Append(managedCluster.As(&targetManagedCluster)...)
		target.ManagedCluster = &targetManagedCluster
	}

	return diags
}
