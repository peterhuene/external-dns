package provider

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"

	log "github.com/Sirupsen/logrus"

	"github.com/Azure/azure-sdk-for-go/arm/dns"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"

	"github.com/kubernetes-incubator/external-dns/endpoint"
	"github.com/kubernetes-incubator/external-dns/plan"
)

type config struct {
	Cloud          string `json:"cloud" yaml:"cloud"`
	TenantID       string `json:"tenantId" yaml:"tenantId"`
	SubscriptionID string `json:"subscriptionId" yaml:"subscriptionId"`
	ResourceGroup  string `json:"resourceGroup" yaml:"resourceGroup"`
	Location       string `json:"location" yaml:"location"`
	ClientID       string `json:"aadClientId" yaml:"aadClientId"`
	ClientSecret   string `json:"aadClientSecret" yaml:"aadClientSecret"`
}

// AzureProvider implements the DNS provider for Microsoft's Azure cloud platform.
type AzureProvider struct {
	domainFilter  string
	dryRun        bool
	resourceGroup string
	zonesClient   dns.ZonesClient
	recordsClient dns.RecordSetsClient
}

// NewAzureProvider creates a new Azure provider.
//
// Returns the provider or an error if a provider could not be created.
func NewAzureProvider(configFile string, domainFilter string, dryRun bool) (Provider, error) {
	if configFile == "" {
		return nil, fmt.Errorf("the --azure-config-file option is required")
	}

	contents, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Azure config file '%s': %v", configFile, err)
	}
	cfg := config{}
	err = yaml.Unmarshal(contents, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to read Azure config file '%s': %v", configFile, err)
	}

	var environment azure.Environment
	if cfg.Cloud == "" {
		environment = azure.PublicCloud
	} else {
		environment, err = azure.EnvironmentFromName(cfg.Cloud)
		if err != nil {
			return nil, fmt.Errorf("invalid cloud value '%s': %v", cfg.Cloud, err)
		}
	}

	oauthConfig, err := adal.NewOAuthConfig(environment.ActiveDirectoryEndpoint, cfg.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to retreive OAuth config: %v", err)
	}

	token, err := adal.NewServicePrincipalToken(*oauthConfig, cfg.ClientID, cfg.ClientSecret, environment.ResourceManagerEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create service principal token: %v", err)
	}

	zonesClient := dns.NewZonesClient(cfg.SubscriptionID)
	zonesClient.Authorizer = autorest.NewBearerAuthorizer(token)
	recordsClient := dns.NewRecordSetsClient(cfg.SubscriptionID)
	recordsClient.Authorizer = autorest.NewBearerAuthorizer(token)

	provider := &AzureProvider{
		domainFilter:  domainFilter,
		dryRun:        dryRun,
		resourceGroup: cfg.ResourceGroup,
		zonesClient:   zonesClient,
		recordsClient: recordsClient,
	}
	return provider, nil
}

// Records gets the current records.
//
// Returns the current records or an error if the operation failed.
func (p *AzureProvider) Records(_ string) ([]*endpoint.Endpoint, error) {
	log.Debug("retrieving Azure DNS records.")
	return nil, fmt.Errorf("not yet implemented")
}

// ApplyChanges applies the given changes.
//
// Returns nil if the operation was successful or an error if the operation failed.
func (p *AzureProvider) ApplyChanges(_ string, changes *plan.Changes) error {
	log.Debug("applying changes to Azure DNS.")
	return fmt.Errorf("not yet implemented")
}
