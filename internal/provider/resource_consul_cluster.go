package provider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/hashicorp/cloud-sdk-go/clients/cloud-consul-service/preview/2020-08-26/client/consul_service"
	consulmodels "github.com/hashicorp/cloud-sdk-go/clients/cloud-consul-service/preview/2020-08-26/models"
	sharedmodels "github.com/hashicorp/cloud-sdk-go/clients/cloud-shared/v1/models"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/hashicorp/terraform-provider-hcp/internal/clients"
	"github.com/hashicorp/terraform-provider-hcp/internal/consul"
)

// defaultClusterTimeoutDuration is the amount of time that can elapse
// before a cluster read operation should timeout.
var defaultClusterTimeoutDuration = time.Minute * 5

// createUpdateTimeoutDuration is the amount of time that can elapse
// before a cluster create or update operation should timeout.
var createUpdateTimeoutDuration = time.Minute * 30

// deleteTimeoutDuration is the amount of time that can elapse
// before a cluster delete operation should timeout.
var deleteTimeoutDuration = time.Minute * 25

// consulCusterResourceCloudProviders is the list of cloud providers
// where a HCP Consul cluster can be provisioned.
var consulCusterResourceCloudProviders = []string{
	"aws",
}

// consulClusterResourceTierLevels is the list of different tier
// levels that an HCP Consul cluster can be as.
var consulClusterResourceTierLevels = []string{
	"Development",
	"Production",
}

// resourceConsulCluster represents an HCP Consul cluster.
func resourceConsulCluster() *schema.Resource {
	return &schema.Resource{
		Description:   "The Consul cluster resource allow you to manage an HCP Consul cluster.",
		CreateContext: resourceConsulClusterCreate,
		ReadContext:   resourceConsulClusterRead,
		UpdateContext: resourceConsulClusterUpdate,
		DeleteContext: resourceConsulClusterDelete,
		Timeouts: &schema.ResourceTimeout{
			Default: &defaultClusterTimeoutDuration,
			Create:  &createUpdateTimeoutDuration,
			Update:  &createUpdateTimeoutDuration,
			Delete:  &deleteTimeoutDuration,
		},
		Importer: &schema.ResourceImporter{
			StateContext: resourceConsulClusterImport,
		},
		Schema: map[string]*schema.Schema{
			// required inputs
			"cluster_id": {
				Description:      "The ID of the HCP Consul cluster.",
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateStringNotEmpty,
			},
			"hvn_id": {
				Description:      "The ID of the HVN this HCP Consul cluster is associated to.",
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateStringNotEmpty,
			},
			"cluster_tier": {
				Description:      "The cluster tier of this HCP Consul cluster.",
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateStringInSlice(consulClusterResourceTierLevels, true),
				DiffSuppressFunc: func(_, old, new string, _ *schema.ResourceData) bool {
					return strings.ToLower(old) == strings.ToLower(new)
				},
			},
			"cloud_provider": {
				Description:      "The provider where the HCP Consul cluster is located. Only 'aws' is available at this time.",
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateStringInSlice(consulCusterResourceCloudProviders, true),
			},
			"region": {
				Description:      "The region where the HCP Consul cluster is located.",
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         true,
				ValidateDiagFunc: validateStringNotEmpty,
			},
			// optional fields
			"public_endpoint": {
				Description: "Denotes that the cluster has an public endpoint for the Consul UI. Defaults to false.",
				Type:        schema.TypeBool,
				Default:     false,
				Optional:    true,
				ForceNew:    true,
			},
			"project_id": {
				Description: "The ID of the project this HCP Consul cluster is located.",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
			},
			"min_consul_version": {
				Description:      "The minimum Consul version of the cluster. If not specified, it is defaulted to the version that is currently recommended by HCP.",
				Type:             schema.TypeString,
				Optional:         true,
				ValidateDiagFunc: validateSemVer,
				DiffSuppressFunc: func(_, old, new string, _ *schema.ResourceData) bool {
					// Suppress diff is normalized versions match OR min_consul_version is removed from the resource
					// since min_consul_version is required in order to upgrade the cluster to a new Consul version.
					return consul.NormalizeVersion(old) == consul.NormalizeVersion(new) || new == ""
				},
			},
			"datacenter": {
				Description: "The Consul data center name of the cluster. If not specified, it is defaulted to the value of `id`.",
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
			},
			"connect_enabled": {
				Description: "Denotes the Consul connect feature should be enabled for this cluster.  Default to true.",
				Type:        schema.TypeBool,
				Default:     true,
				Optional:    true,
				ForceNew:    true,
			},
			// computed outputs
			"state": {
				Description: "The state of the cluster.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_automatic_upgrades": {
				Description: "Denotes that automatic Consul upgrades are enabled.",
				Type:        schema.TypeBool,
				Computed:    true,
			},
			"consul_snapshot_interval": {
				Description: "The Consul snapshot interval.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_snapshot_retention": {
				Description: "The retention policy for Consul snapshots.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_config_file": {
				Description: "The cluster config encoded as a Base64 string.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_ca_file": {
				Description: "The cluster CA file encoded as a Base64 string.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_connect": {
				Description: "Denotes that Consul connect is enabled.",
				Type:        schema.TypeBool,
				Computed:    true,
			},
			"consul_version": {
				Description: "The Consul version of the cluster.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_public_endpoint_url": {
				Description: "The public URL for the Consul UI. This will be empty if `public_endpoint` is `true`.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_private_endpoint_url": {
				Description: "The private URL for the Consul UI.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_root_token_accessor_id": {
				Description: "The accessor ID of the root ACL token that is generated upon cluster creation. If a new root token is generated using the `hcp_consul_root_token` resource, this field is no longer valid.",
				Type:        schema.TypeString,
				Computed:    true,
			},
			"consul_root_token_secret_id": {
				Description: "The secret ID of the root ACL token that is generated upon cluster creation. If a new root token is generated using the `hcp_consul_root_token` resource, this field is no longer valid.",
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func resourceConsulClusterCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client := meta.(*clients.Client)

	clusterID := d.Get("cluster_id").(string)

	loc, err := buildConsulClusterResourceLocation(ctx, d, client)
	if err != nil {
		return diag.FromErr(err)
	}

	// Check for an existing Consul cluster
	_, err = clients.GetConsulClusterByID(ctx, client, loc, clusterID)
	if err != nil {
		var apiErr *runtime.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != 404 {
			return diag.Errorf("unable to check for presence of an existing Consul Cluster (%s): %+v")
		}

		// a 404 indicates a Consul cluster was not found
		log.Printf("[INFO] Consul cluster (%s) not found, proceeding with create", clusterID)
	} else {
		return diag.Errorf("a Consul cluster with cluster_id=%s and project_id=%s already exists - to be managed via Terraform this resource needs to be imported into the State.  Please see the resource documentation for hcp_consul_cluster for more information.", clusterID, loc.ProjectID)
	}

	// fetch available version from HCP
	availableConsulVersions, err := consul.GetAvailableHCPConsulVersions(ctx, meta.(*clients.Client).Config.HCPApiDomain)
	if err != nil || availableConsulVersions == nil {
		return diag.Errorf("error fetching available HCP Consul versions: %+v", err)
	}

	// determine recommended version
	consulVersion := consul.RecommendedVersion(availableConsulVersions)
	v, ok := d.GetOk("min_consul_version")
	if ok {
		consulVersion = consul.NormalizeVersion(v.(string))
	}

	// check if version is valid and available
	if !consul.IsValidVersion(consulVersion, availableConsulVersions) {
		return diag.Errorf("specified Consul version (%s) is unavailable; must be one of: %+v", consulVersion, availableConsulVersions)
	}

	createConsulClusterParams := consul_service.NewCreateParams()
	createConsulClusterParams.Context = ctx
	createConsulClusterParams.Body = &consulmodels.HashicorpCloudConsul20200826CreateRequest{
		Cluster: &consulmodels.HashicorpCloudConsul20200826Cluster{
			Config:        nil,
			ConsulVersion: consulVersion,
			ID:            clusterID,
			Location:      loc,
		},
	}

	createConsulClusterParams.ClusterLocationOrganizationID = loc.OrganizationID
	createConsulClusterParams.ClusterLocationProjectID = loc.ProjectID

	return nil
}

func resourceConsulClusterRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return nil
}

func resourceConsulClusterUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return nil
}

func resourceConsulClusterDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return nil
}

func resourceConsulClusterImport(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	return nil, nil
}

// buildConsulClusterResourceLocation builds a Hashicorp Cloud Location based off of the resource data's
// org, project, region, and provider details
func buildConsulClusterResourceLocation(ctx context.Context, d *schema.ResourceData, client *clients.Client) (
	*sharedmodels.HashicorpCloudLocationLocation, error) {

	provider := d.Get("cloud_provider").(string)
	region := d.Get("region").(string)

	projectID := client.Config.ProjectID
	organizationID := client.Config.OrganizationID
	projectIDVal, ok := d.GetOk("project_id")
	if ok {
		projectID = projectIDVal.(string)

		// Try to get organization_id from state, since project_id might have come from state.
		organizationID = d.Get("organization_id").(string)
	}

	if projectID == "" {
		return nil, fmt.Errorf("missing project_id: a project_id must be specified on the Consul cluster resource or the provider")
	}

	if organizationID == "" {
		var err error
		organizationID, err = clients.GetParentOrganizationIDByProjectID(ctx, client, projectID)

		if err != nil {
			return nil, fmt.Errorf("unable to retrieve organization ID for proejct [project_id=%s]: %+v", projectID, err)
		}
	}

	return &sharedmodels.HashicorpCloudLocationLocation{
		OrganizationID: organizationID,
		ProjectID:      projectID,
		Region: &sharedmodels.HashicorpCloudLocationRegion{
			Provider: provider,
			Region:   region,
		},
	}, nil
}
