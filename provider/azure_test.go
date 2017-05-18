package provider

import (
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/arm/dns"
	"github.com/Azure/go-autorest/autorest"
	"github.com/kubernetes-incubator/external-dns/endpoint"
)

// MockStruct and methods
type mockZonesClient struct {
	mockZoneListResult *dns.ZoneListResult
}

type mockRecordsClient struct {
	mockRecordSet *[]dns.RecordSet
}

func stringPointer(s string) *string {
	return &s
}

func (client mockZonesClient) List(top *int32) (result dns.ZoneListResult, err error) {
	return *client.mockZoneListResult, nil
}

func createMockZone(zone string) *dns.Zone {
	return &dns.Zone{
		Name: stringPointer(zone),
	}
}

func (client mockZonesClient) ListByResourceGroup(resourceGroupName string, top *int32) (result dns.ZoneListResult, err error) {
	return dns.ZoneListResult{}, errors.New("(ZoneClient) LitByResourceGroup not implemented")
}
func (client mockZonesClient) ListNextResults(lastResults dns.ZoneListResult) (result dns.ZoneListResult, err error) {
	return dns.ZoneListResult{}, errors.New("(ZoneClient) ListNextResults not implemented")
}

type recordSetPropertiesGetter func(value string) *dns.RecordSetProperties

func aRecordSetPropertiesGetter(value string) *dns.RecordSetProperties {
	return &dns.RecordSetProperties{
		ARecords: &[]dns.ARecord{
			dns.ARecord{
				Ipv4Address: stringPointer(value),
			},
		},
	}
}

func cNameRecordSetPropertiesGetter(value string) *dns.RecordSetProperties {
	return &dns.RecordSetProperties{
		CnameRecord: &dns.CnameRecord{
			Cname: stringPointer(value),
		},
	}
}

func txtRecordSetPropertiesGetter(value string) *dns.RecordSetProperties {
	return &dns.RecordSetProperties{
		TxtRecords: &[]dns.TxtRecord{
			dns.TxtRecord{
				Value: &[]string{value},
			},
		},
	}
}

func othersRecordSetPropertiesGetter(value string) *dns.RecordSetProperties {
	return &dns.RecordSetProperties{}
}

func createMockRecordSet(name, recordType, value string) *dns.RecordSet {
	var getterFunc recordSetPropertiesGetter

	switch recordType {
	case "A":
		getterFunc = aRecordSetPropertiesGetter
	case "CNAME":
		getterFunc = cNameRecordSetPropertiesGetter
	case "TXT":
		getterFunc = txtRecordSetPropertiesGetter
	default:
		getterFunc = othersRecordSetPropertiesGetter
	}
	return &dns.RecordSet{
		Name:                stringPointer(name),
		Type:                stringPointer("Microsoft.Network/dnszones/" + recordType),
		RecordSetProperties: getterFunc(value),
	}

}

func (client mockRecordsClient) ListByDNSZone(resourceGroupName string, zoneName string, top *int32) (result dns.RecordSetListResult, err error) {
	result = dns.RecordSetListResult{
		Value: client.mockRecordSet,
	}
	return result, nil
}
func (client mockRecordsClient) Delete(resourceGroupName string, zoneName string, relativeRecordSetName string, recordType dns.RecordType, ifMatch string) (result autorest.Response, err error) {
	return autorest.Response{}, errors.New("(RecordClient) Delete not implemented")
}
func (client mockRecordsClient) CreateOrUpdate(resourceGroupName string, zoneName string, relativeRecordSetName string, recordType dns.RecordType, parameters dns.RecordSet, ifMatch string, ifNoneMatch string) (result dns.RecordSet, err error) {
	return dns.RecordSet{}, errors.New("(RecordClient) CreateOrUpdate not implemented")
}

func newAzureProvider(t *testing.T, domainFilter string, dryRun bool, resourceGroup string, zonesClient ZonesClient, recordsClient RecordsClient) *AzureProvider {
	provider := &AzureProvider{
		domainFilter:  domainFilter,
		dryRun:        dryRun,
		resourceGroup: resourceGroup,
		zonesClient:   zonesClient,
		recordsClient: recordsClient,
	}
	return provider
}

func TestAzureRecord(t *testing.T) {
	zonesClient := mockZonesClient{
		mockZoneListResult: &dns.ZoneListResult{
			Value: &[]dns.Zone{
				*createMockZone("example.com"),
			},
		},
	}

	recordsClient := mockRecordsClient{
		mockRecordSet: &[]dns.RecordSet{
			*createMockRecordSet("@", "NS", "ns1-03.azure-dns.com."),
			*createMockRecordSet("@", "SOA", "Email: azuredns-hostmaster.microsoft.com"),
			*createMockRecordSet("@", "A", "123.123.123.122"),
			*createMockRecordSet("@", "TXT", "heritage=external-dns,external-dns/owner=default"),
			*createMockRecordSet("nginx", "A", "123.123.123.123"),
			*createMockRecordSet("nginx", "TXT", "heritage=external-dns,external-dns/owner=default"),
			*createMockRecordSet("hack", "CNAME", "hack.azurewebsites.net"),
		},
	}

	provider := newAzureProvider(t, "example.com", true, "k8s", &zonesClient, &recordsClient)
	actual, err := provider.Records("")

	if err != nil {
		t.Fatal(err)
	}
	expected := []*endpoint.Endpoint{
		endpoint.NewEndpoint("example.com", "123.123.123.122", "A"),
		endpoint.NewEndpoint("example.com", "heritage=external-dns,external-dns/owner=default", "TXT"),
		endpoint.NewEndpoint("nginx.example.com", "123.123.123.123", "A"),
		endpoint.NewEndpoint("nginx.example.com", "heritage=external-dns,external-dns/owner=default", "TXT"),
		endpoint.NewEndpoint("hack.example.com", "hack.azurewebsites.net", "CNAME"),
	}

	validateEndpoints(t, actual, expected)

}

func TestAzureCreateRecord(t *testing.T) {

}
