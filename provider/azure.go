package provider

import (
	"fmt"
	"io/ioutil"
	"strings"

	"gopkg.in/yaml.v2"

	log "github.com/Sirupsen/logrus"

	"github.com/Azure/azure-sdk-for-go/arm/dns"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/to"

	"github.com/kubernetes-incubator/external-dns/endpoint"
	"github.com/kubernetes-incubator/external-dns/plan"
)

const (
	azureRecordTTL = 300
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

// Maximum number of the pager feature of Records
const (
	maxPageElementNum = 100
)

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
func (p *AzureProvider) Records(_ string) (endpoints []*endpoint.Endpoint, _ error) {
	log.Debug("retrieving Azure DNS records.")
	var top int32 = maxPageElementNum
	zones, err := p.filteredZone()
	if err != nil {
		return nil, err
	}

	for _, zone := range zones {
		recordSetList, err := p.recordsClient.ListByDNSZone(p.resourceGroup, *zone.Name, &top)
		if err != nil {
			return nil, err
		}

		for _, record := range *recordSetList.Value {
			switch *record.Type {
			case "A", "CNAME", "TXT":
				endpoints = append(endpoints, endpoint.NewEndpoint(getDNSName(*record.Name), getTarget(record), *record.Type))
			default:
			}
		}
	}
	return endpoints, nil
}
func getDNSName(recordName, zoneName string) string {
	if recordName == "@" {
		return zoneName
	}
	return fmt.Sprintf("%s.%s", recordName, zoneName)
}

func (p *AzureProvider) filteredZone() (filteredZone []*dns.Zone, _ error) {
	var top int32 = maxPageElementNum
	zones, err := p.zonesClient.List(&top)
	if err != nil {
		return nil, err
	}
	for _, element := range *zones.Value {
		if *element.Name == p.domainFilter {
			filteredZone = append(filteredZone, &element)
		}
	}
	return filteredZone, nil
}
func getTarget(recordSet dns.RecordSet) string {

	m := map[string]string{
		"A":     *(*recordSet.RecordSetProperties.ARecords)[0].Ipv4Address,
		"CNAME": *(*recordSet.RecordSetProperties.CnameRecord).Cname,
		"TXT":   (*(*recordSet.RecordSetProperties.TxtRecords)[0].Value)[0],
	}
	return m[*recordSet.Type]
}

// ApplyChanges applies the given changes.
//
// Returns nil if the operation was successful or an error if the operation failed.
func (p *AzureProvider) ApplyChanges(_ string, changes *plan.Changes) error {
	zones, err := p.azureZones()
	if err != nil {
		return err
	}

	deleted, updated := mapAzureChanges(zones, changes)
	p.deleteAzureRecords(deleted)
	p.updateAzureRecords(updated)
	return nil
}

func (p *AzureProvider) azureZones() ([]dns.Zone, error) {
	log.Debug("retrieving Azure DNS zones.")

	var zones []dns.Zone
	list, err := p.zonesClient.ListByResourceGroup(p.resourceGroup, nil)
	if err != nil {
		return zones, err
	}

	for list.Value != nil && len(*list.Value) > 0 {
		for _, zone := range *list.Value {
			if zone.Name != nil && strings.HasSuffix(*zone.Name, p.domainFilter) {
				zones = append(zones, zone)
			}
		}

		list, err = p.zonesClient.ListNextResults(list)
		if err != nil {
			return zones, err
		}
	}
	log.Debugf("found %d Azure DNS zone(s).", len(zones))
	return zones, nil
}

type azureChangeMap map[*dns.Zone][]*endpoint.Endpoint

func mapAzureChanges(zones []dns.Zone, changes *plan.Changes) (azureChangeMap, azureChangeMap) {
	ignored := map[string]bool{}
	deleted := azureChangeMap{}
	updated := azureChangeMap{}

	mapChange := func(changeMap azureChangeMap, change *endpoint.Endpoint) {
		zone := findAzureZone(zones, change.DNSName)
		if zone == nil {
			if _, ok := ignored[change.DNSName]; !ok {
				ignored[change.DNSName] = true
				log.Infof("ignoring changes to '%s' because a suitable Azure DNS zone was not found.", change.DNSName)
			}
			return
		}
		// Change unspecified record types to adress
		if change.RecordType == "" {
			change.RecordType = "A"
		}
		changeMap[zone] = append(changeMap[zone], change)
	}

	for _, change := range changes.Delete {
		mapChange(deleted, change)
	}

	for _, change := range changes.UpdateOld {
		mapChange(deleted, change)
	}

	for _, change := range changes.Create {
		mapChange(updated, change)
	}

	for _, change := range changes.UpdateNew {
		mapChange(updated, change)
	}
	return deleted, updated
}

func findAzureZone(zones []dns.Zone, name string) *dns.Zone {
	var result *dns.Zone

	// Go through every zone looking for the longest name (i.e. most specific) as a matching suffix
	for _, zone := range zones {
		if strings.HasSuffix(name, *zone.Name) {
			if result == nil || len(*zone.Name) > len(*result.Name) {
				result = &zone
			}
		}
	}
	return result
}

func (p *AzureProvider) deleteAzureRecords(deleted azureChangeMap) {
	// Delete records first
	for zone, endpoints := range deleted {
		for _, endpoint := range endpoints {
			name := recordSetNameForZone(zone, endpoint)
			if p.dryRun {
				log.Infof("would delete %s record named '%s' for Azure DNS zone '%s'.", endpoint.RecordType, name, *zone.Name)
			} else {
				log.Infof("deleting %s record named '%s' for Azure DNS zone '%s'.", endpoint.RecordType, name, *zone.Name)
				if _, err := p.recordsClient.Delete(p.resourceGroup, *zone.Name, name, dns.RecordType(endpoint.RecordType), ""); err != nil {
					log.Errorf(
						"failed to delete %s record named '%s' for Azure DNS zone '%s': %v",
						endpoint.RecordType,
						name,
						*zone.Name,
						err,
					)
				}
			}
		}
	}
}

func (p *AzureProvider) updateAzureRecords(updated azureChangeMap) {
	for zone, endpoints := range updated {
		for _, endpoint := range endpoints {
			name := recordSetNameForZone(zone, endpoint)
			if p.dryRun {
				log.Infof(
					"would update %s record named '%s' to '%s' for Azure DNS zone '%s'.",
					endpoint.RecordType,
					name,
					endpoint.Target,
					*zone.Name,
				)
			} else {
				log.Infof(
					"updating %s record named '%s' to '%s' for Azure DNS zone '%s'.",
					endpoint.RecordType,
					name,
					endpoint.Target,
					*zone.Name,
				)
			}
			recordSet, err := newRecordSet(endpoint)
			if err == nil && !p.dryRun {
				_, err = p.recordsClient.CreateOrUpdate(
					p.resourceGroup,
					*zone.Name,
					name,
					dns.RecordType(endpoint.RecordType),
					recordSet,
					"",
					"",
				)
			}
			if err != nil {
				log.Errorf(
					"failed to update %s record named '%s' to '%s' for DNS zone '%s': %v",
					endpoint.RecordType,
					name,
					endpoint.Target,
					*zone.Name,
					err,
				)
			}
		}
	}
}

func recordSetNameForZone(zone *dns.Zone, endpoint *endpoint.Endpoint) string {
	// Remove the zone from the record set
	name := endpoint.DNSName
	name = name[:len(name)-len(*zone.Name)]
	name = strings.TrimSuffix(name, ".")

	// For root, use @
	if name == "" {
		return "@"
	}
	return name
}

func newRecordSet(endpoint *endpoint.Endpoint) (dns.RecordSet, error) {
	switch dns.RecordType(endpoint.RecordType) {
	case dns.A:
		return dns.RecordSet{
			RecordSetProperties: &dns.RecordSetProperties{
				TTL: to.Int64Ptr(azureRecordTTL),
				ARecords: &[]dns.ARecord{
					{
						Ipv4Address: to.StringPtr(endpoint.Target),
					},
				},
			},
		}, nil
	case dns.CNAME:
		return dns.RecordSet{
			RecordSetProperties: &dns.RecordSetProperties{
				TTL: to.Int64Ptr(azureRecordTTL),
				CnameRecord: &dns.CnameRecord{
					Cname: to.StringPtr(endpoint.Target),
				},
			},
		}, nil
	case dns.TXT:
		return dns.RecordSet{
			RecordSetProperties: &dns.RecordSetProperties{
				TTL: to.Int64Ptr(azureRecordTTL),
				TxtRecords: &[]dns.TxtRecord{
					{
						Value: &[]string{
							endpoint.Target,
						},
					},
				},
			},
		}, nil
	}
	return dns.RecordSet{}, fmt.Errorf("unsupported record type '%s'.", endpoint.RecordType)
}
