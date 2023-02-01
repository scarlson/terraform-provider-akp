package akp

import (
	"context"
	"fmt"
	"time"

	argocdv1 "github.com/akuity/api-client-go/pkg/api/gen/argocd/v1"
	idv1 "github.com/akuity/api-client-go/pkg/api/gen/types/id/v1"
	healthv1 "github.com/akuity/api-client-go/pkg/api/gen/types/status/health/v1"
	reconv1 "github.com/akuity/api-client-go/pkg/api/gen/types/status/reconciliation/v1"
	ctxutil "github.com/akuity/api-client-go/pkg/utils/context"
	"k8s.io/client-go/rest"

	"github.com/akuity/terraform-provider-akp/akp/kube"
	akptypes "github.com/akuity/terraform-provider-akp/akp/types"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/exp/slices"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ resource.Resource = &AkpClusterResource{}
var _ resource.ResourceWithImportState = &AkpClusterResource{}

func NewAkpClusterResource() resource.Resource {
	return &AkpClusterResource{}
}

// AkpClusterResource defines the resource implementation.
type AkpClusterResource struct {
	akpCli *AkpCli
	kcfg   *rest.Config
}

func (r *AkpClusterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

func (r *AkpClusterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	akpCli, ok := req.ProviderData.(*AkpCli)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *AkpCli, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}
	r.akpCli = akpCli
}

func (r *AkpClusterResource) getKcfg(ctx context.Context, kubeConf types.Object) (*rest.Config, diag.Diagnostics) {
	var kubeConfig kube.KubeConfig
	var diag diag.Diagnostics
	if kubeConf.IsNull() || kubeConf.IsUnknown() {
		diag.AddWarning("Kubectl not configured", "Cannot update agent because kubectl configuration is missing")
		return nil, diag
	}
	tflog.Debug(ctx, fmt.Sprintf("Kube config: %s", kubeConf))
	diag = kubeConf.As(ctx, &kubeConfig, basetypes.ObjectAsOptions{
		UnhandledNullAsEmpty:    true,
		UnhandledUnknownAsEmpty: true,
	})
	if diag.HasError() {
		return nil, diag
	}
	kcfg, err := kube.InitializeConfiguration(&kubeConfig)
	tflog.Debug(ctx, fmt.Sprintf("Kcfg: %s", kcfg))
	if err != nil {
		diag.AddError("Kubectl error", fmt.Sprintf("Cannot insitialize Kubectl. Check kubernetes configuration. Error: %s", err))
		return nil, diag
	}
	return kcfg, diag
}

func (r *AkpClusterResource) applyManifests(ctx context.Context, manifests string, cfg *rest.Config) diag.Diagnostics {
	diag := diag.Diagnostics{}
	kubectl, err := kube.NewKubectl(cfg)
	if err != nil {
		diag.AddError("Kubernetes error", fmt.Sprintf("failed to create kubectl, error=%s", err))
	}
	resources, err := kube.SplitYAML([]byte(manifests))
	tflog.Info(ctx, fmt.Sprintf("%d resources to create", len(resources)))
	if err != nil {
		diag.AddError("YAML error", fmt.Sprintf("failed to parse manifest, error=%s", err))
	}

	for _, un := range resources {
		msg, err := kubectl.ApplyResource(ctx, &un, kube.ApplyOpts{})
		if err != nil {
			diag.AddError("Kubernetes error", fmt.Sprintf("failed to apply manifest %s, error=%s", un, err))
			return diag
		}
		tflog.Debug(ctx, msg)
	}
	return diag
}

func (r *AkpClusterResource) deleteManifests(ctx context.Context, manifests string, cfg *rest.Config) diag.Diagnostics {
	diag := diag.Diagnostics{}
	kubectl, err := kube.NewKubectl(cfg)
	if err != nil {
		diag.AddError("Kubernetes error", fmt.Sprintf("failed to create kubectl, error=%s", err))
	}
	resources, err := kube.SplitYAML([]byte(manifests))
	tflog.Info(ctx, fmt.Sprintf("%d resources to delete", len(resources)))
	if err != nil {
		diag.AddError("YAML error", fmt.Sprintf("failed to parse manifest, error=%s", err))
	}

	// Delete the resources in reverse order
	for i := len(resources) - 1; i >= 0; i-- {
		msg, err := kubectl.DeleteResource(ctx, &resources[i], kube.DeleteOpts{
			IgnoreNotFound:  true,
			WaitForDeletion: true,
			Force:           false,
		})
		if err != nil {
			diag.AddError("Kubernetes error", fmt.Sprintf("failed to delete manifest %s, error=%s", &resources[i], err))
			return diag
		}
		tflog.Debug(ctx, msg)
	}
	return diag
}

func (r *AkpClusterResource) waitClusterReconStatus(ctx context.Context, cluster *argocdv1.Cluster, instanceId string) (*argocdv1.Cluster, error) {
	reconStatus := cluster.GetReconciliationStatus()
	breakStatusesRecon := []reconv1.StatusCode{reconv1.StatusCode_STATUS_CODE_SUCCESSFUL, reconv1.StatusCode_STATUS_CODE_FAILED}

	for !slices.Contains(breakStatusesRecon, reconStatus.GetCode()) {
		time.Sleep(1 * time.Second)
		apiResp, err := r.akpCli.Cli.GetInstanceCluster(ctx, &argocdv1.GetInstanceClusterRequest{
			OrganizationId: r.akpCli.OrgId,
			InstanceId:     instanceId,
			Id:             cluster.Id,
			IdType:         idv1.Type_ID,
		})
		if err != nil {
			return nil, err
		}
		cluster = apiResp.GetCluster()
		reconStatus = cluster.GetReconciliationStatus()
		tflog.Debug(ctx, fmt.Sprintf("Cluster recon status: %s", reconStatus.String()))
	}
	return cluster, nil
}

func (r *AkpClusterResource) waitClusterHealthStatus(ctx context.Context, cluster *argocdv1.Cluster, instanceId string) (*argocdv1.Cluster, error) {
	healthStatus := cluster.GetHealthStatus()
	breakStatusesHealth := []healthv1.StatusCode{healthv1.StatusCode_STATUS_CODE_HEALTHY, healthv1.StatusCode_STATUS_CODE_DEGRADED}

	for !slices.Contains(breakStatusesHealth, healthStatus.GetCode()) {
		time.Sleep(1 * time.Second)
		apiResp, err := r.akpCli.Cli.GetInstanceCluster(ctx, &argocdv1.GetInstanceClusterRequest{
			OrganizationId: r.akpCli.OrgId,
			InstanceId:     instanceId,
			Id:             cluster.Id,
			IdType:         idv1.Type_ID,
		})
		if err != nil {
			return nil, err
		}
		cluster = apiResp.GetCluster()
		healthStatus = cluster.GetHealthStatus()
		tflog.Debug(ctx, fmt.Sprintf("Cluster health status: %s", healthStatus.String()))
	}
	return cluster, nil
}

func (r *AkpClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan *akptypes.AkpClusterKube

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

	if resp.Diagnostics.HasError() {
		return
	}

	ctx = ctxutil.SetClientCredential(ctx, r.akpCli.Cred)
	customArgoproj := plan.CustomImageRegistryArgoproj.ValueString()
	customAkuity := plan.CustomImageRegistryAkuity.ValueString()
	autoupgrade := plan.AutoUpgradeDisabled.ValueBool()
	var labels map[string]string
	var annotations map[string]string
	resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &labels, true)...)
	resp.Diagnostics.Append(plan.Annotations.ElementsAs(ctx, &annotations, true)...)
	apiReq := &argocdv1.CreateInstanceClusterRequest{
		OrganizationId:  r.akpCli.OrgId,
		Name:            plan.Name.ValueString(),
		InstanceId:      plan.InstanceId.ValueString(),
		Description:     plan.Description.ValueString(),
		Namespace:       plan.Namespace.ValueString(),
		NamespaceScoped: plan.NamespaceScoped.ValueBool(),
		Data: &argocdv1.ClusterData{
			Size:                        akptypes.StringClusterSize[plan.Size.ValueString()],
			CustomImageRegistryArgoproj: &customArgoproj,
			CustomImageRegistryAkuity:   &customAkuity,
			AutoUpgradeDisabled:         &autoupgrade,
			Labels:                      labels,
			Annotations:                 annotations,
		},
		Upsert: false,
	}
	tflog.Info(ctx, "Creating Cluster...")
	tflog.Debug(ctx, fmt.Sprintf("Api Request: %s", apiReq))
	apiResp, err := r.akpCli.Cli.CreateInstanceCluster(ctx, apiReq)
	tflog.Debug(ctx, fmt.Sprintf("Api Response: %s", apiResp))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create Akuity cluster. %s", err))
		return
	}
	cluster, err := r.waitClusterReconStatus(ctx, apiResp.GetCluster(), plan.InstanceId.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to check cluster reconciliation status. %s", err))
		return
	}
	tflog.Info(ctx, "Cluster created")

	state := &akptypes.AkpClusterKube{
		InstanceId: plan.InstanceId,
	}
	resp.Diagnostics.Append(state.UpdateCluster(apiResp.GetCluster())...)
	resp.Diagnostics.Append(state.UpdateManifests(ctx, r.akpCli.Cli, r.akpCli.OrgId)...)

	state.KubeConfig = plan.KubeConfig
	kcfg, diag := r.getKcfg(ctx, plan.KubeConfig)
	resp.Diagnostics.Append(diag...)
	// Apply the manifests
	if kcfg != nil {
		tflog.Info(ctx, "Applying the manifests...")
		resp.Diagnostics.Append(r.applyManifests(ctx, state.Manifests.ValueString(), kcfg)...)
		cluster, err = r.waitClusterHealthStatus(ctx, cluster, plan.InstanceId.ValueString())
		state.AgentVersion = types.StringValue(cluster.AgentState.GetVersion())
	}
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AkpClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state *akptypes.AkpClusterKube

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}

	ctx = ctxutil.SetClientCredential(ctx, r.akpCli.Cred)
	apiReq := &argocdv1.GetInstanceClusterRequest{
		OrganizationId: r.akpCli.OrgId,
		InstanceId:     state.InstanceId.ValueString(),
		Id:             state.Id.ValueString(),
		IdType:         idv1.Type_ID,
	}
	tflog.Debug(ctx, fmt.Sprintf("Api Request: %s", apiReq))
	apiResp, err := r.akpCli.Cli.GetInstanceCluster(ctx, apiReq)
	switch status.Code(err) {
	case codes.OK:
		tflog.Debug(ctx, fmt.Sprintf("Api Response: %s", apiResp))
	case codes.NotFound:
		resp.State.RemoveResource(ctx)
		return
	default:
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read Akuity cluster. %s", err))
		return
	}

	resp.Diagnostics.Append(state.UpdateCluster(apiResp.GetCluster())...)

	if state.Manifests.IsNull() || state.Manifests.IsUnknown() {
		resp.Diagnostics.Append(state.UpdateManifests(ctx, r.akpCli.Cli, r.akpCli.OrgId)...)
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AkpClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan *akptypes.AkpClusterKube

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx = ctxutil.SetClientCredential(ctx, r.akpCli.Cred)
	customArgoproj := plan.CustomImageRegistryArgoproj.ValueString()
	customAkuity := plan.CustomImageRegistryAkuity.ValueString()
	autoupgrade := plan.AutoUpgradeDisabled.ValueBool()
	var labels map[string]string
	var annotations map[string]string
	resp.Diagnostics.Append(plan.Labels.ElementsAs(ctx, &labels, true)...)
	resp.Diagnostics.Append(plan.Annotations.ElementsAs(ctx, &annotations, true)...)
	apiReq := &argocdv1.UpdateInstanceClusterRequest{
		OrganizationId: r.akpCli.OrgId,
		InstanceId:     plan.InstanceId.ValueString(),
		Id:             plan.Id.ValueString(),
		Description:    plan.Description.ValueString(),
		Data: &argocdv1.ClusterData{
			Size:                        akptypes.StringClusterSize[plan.Size.ValueString()],
			CustomImageRegistryArgoproj: &customArgoproj,
			CustomImageRegistryAkuity:   &customAkuity,
			AutoUpgradeDisabled:         &autoupgrade,
			Labels:                      labels,
			Annotations:                 annotations,
		},
	}
	tflog.Debug(ctx, fmt.Sprintf("Api Request: %s", apiReq))
	apiResp, err := r.akpCli.Cli.UpdateInstanceCluster(ctx, apiReq)
	tflog.Debug(ctx, fmt.Sprintf("Api Respons: %s", apiResp))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update Akuity cluster. %s", err))
		return
	}

	state := &akptypes.AkpClusterKube{
		InstanceId: plan.InstanceId,
	}
	resp.Diagnostics.Append(state.UpdateCluster(apiResp.GetCluster())...)
	resp.Diagnostics.Append(state.UpdateManifests(ctx, r.akpCli.Cli, r.akpCli.OrgId)...)
	state.KubeConfig = plan.KubeConfig

	kcfg, diag := r.getKcfg(ctx, plan.KubeConfig)
	resp.Diagnostics.Append(diag...)
	// Update k8s resources with terraform only if autoupgarde is disabled for the cluster
	if state.AutoUpgradeDisabled.ValueBool() && kcfg != nil {
		tflog.Info(ctx, "Applying the manifests...")
		resp.Diagnostics.Append(r.applyManifests(ctx, state.Manifests.ValueString(), kcfg)...)
		cluster, _ := r.waitClusterHealthStatus(ctx, apiResp.GetCluster(), plan.InstanceId.ValueString())
		state.AgentVersion = types.StringValue(cluster.AgentState.GetVersion())
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AkpClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state *akptypes.AkpClusterKube

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}
	kcfg, diag := r.getKcfg(ctx, state.KubeConfig)
	resp.Diagnostics.Append(diag...)
	// Delete the kubernetes resources
	if kcfg != nil {
		tflog.Info(ctx, "Deleting the resources...")
		diag = r.deleteManifests(ctx, state.Manifests.ValueString(), kcfg)
	}
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	ctx = ctxutil.SetClientCredential(ctx, r.akpCli.Cred)
	apiReq := &argocdv1.DeleteInstanceClusterRequest{
		OrganizationId: r.akpCli.OrgId,
		InstanceId:     state.InstanceId.ValueString(),
		Id:             state.Id.ValueString(),
	}
	tflog.Debug(ctx, fmt.Sprintf("Api Request: %s", apiReq))
	apiResp, err := r.akpCli.Cli.DeleteInstanceCluster(ctx, apiReq)
	tflog.Debug(ctx, fmt.Sprintf("Api Response: %s", apiResp))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete Akuity cluster. %s", err))
		return
	}
	time.Sleep(1 * time.Second)
}

// TODO: Implement cluster import
func (r *AkpClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
