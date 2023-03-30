package akp

import (
	"context"
	"fmt"

	argocdv1 "github.com/akuity/api-client-go/pkg/api/gen/argocd/v1"
	akptypes "github.com/akuity/terraform-provider-akp/akp/types"
	httpctx "github.com/akuity/grpc-gateway-client/pkg/http/context"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ datasource.DataSource = &AkpClustersDataSource{}

func NewAkpClustersDataSource() datasource.DataSource {
	return &AkpClustersDataSource{}
}

// AkpClustersDataSource defines the data source implementation.
type AkpClustersDataSource struct {
	akpCli *AkpCli
}

type AkpClustersDataSourceModel struct {
	Id         types.String           `tfsdk:"id"`
	InstanceId types.String           `tfsdk:"instance_id"`
	Clusters   []*akptypes.AkpCluster `tfsdk:"clusters"`
}

func (d *AkpClustersDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_clusters"
}

func (d *AkpClustersDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Find all clusters attached to an Argo CD instance",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"instance_id": schema.StringAttribute{
				MarkdownDescription: "Argo CD Instance ID",
				Required:            true,
			},
			"clusters": schema.ListNestedAttribute{
				MarkdownDescription: "List of clusters",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Cluster ID",
							Computed:            true,
						},
						"manifests": schema.StringAttribute{
							MarkdownDescription: "Agent Installation Manifests",
							Computed:            true,
							Sensitive:           true,
						},
						"instance_id": schema.StringAttribute{
							MarkdownDescription: "Argo CD Instance ID",
							Computed:            true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "Cluster Name",
							Computed:            true,
						},
						"description": schema.StringAttribute{
							MarkdownDescription: "Cluster Description",
							Computed:            true,
						},
						"namespace": schema.StringAttribute{
							MarkdownDescription: "Agent Installation Namespace",
							Computed:            true,
						},
						"namespace_scoped": schema.BoolAttribute{
							MarkdownDescription: "Agent Namespace Scoped",
							Computed:            true,
						},
						"size": schema.StringAttribute{
							MarkdownDescription: "Cluster Size. One of `small`, `medium` or `large`",
							Computed:            true,
						},
						"auto_upgrade_disabled": schema.BoolAttribute{
							MarkdownDescription: "Disable Agents Auto Upgrade",
							Computed:            true,
						},
						"labels": schema.MapAttribute{
							ElementType:         types.StringType,
							MarkdownDescription: "Cluster Labels",
							Computed:            true,
						},
						"annotations": schema.MapAttribute{
							ElementType:         types.StringType,
							MarkdownDescription: "Cluster Annotations",
							Computed:            true,
						},
						"agent_version": schema.StringAttribute{
							MarkdownDescription: "Installed agent version",
							Computed:            true,
						},
					},
				},
			},
		},
	}
}

func (d *AkpClustersDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
	d.akpCli = akpCli
}

func (d *AkpClustersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state AkpClustersDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)

	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Debug(ctx, "Reading an instance clusters")

	ctx = httpctx.SetAuthorizationHeader(ctx, d.akpCli.Cred.Scheme(), d.akpCli.Cred.Credential())
	apiReq := &argocdv1.ListInstanceClustersRequest{
		OrganizationId: d.akpCli.OrgId,
		InstanceId:     state.InstanceId.ValueString(),
	}
	tflog.Debug(ctx, fmt.Sprintf("Api Request: %s", apiReq))
	apiResp, err := d.akpCli.Cli.ListInstanceClusters(ctx, apiReq)
	tflog.Debug(ctx, fmt.Sprintf("Api Response: %s", apiResp))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read instance clusters, got error: %s", err))
		return
	}

	clusters := apiResp.GetClusters()

	for _, cluster := range clusters {
		stateCluster := &akptypes.AkpCluster{
			InstanceId: state.InstanceId,
		}
		resp.Diagnostics.Append(stateCluster.UpdateCluster(cluster)...)
		resp.Diagnostics.Append(stateCluster.UpdateManifests(ctx, d.akpCli.Cli, d.akpCli.OrgId)...)
		state.Clusters = append(state.Clusters, stateCluster)
	}
	state.Id = types.StringValue(state.InstanceId.ValueString())
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
