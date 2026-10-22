// Package model holds what the lookups of the whoice module pass around:
// the query, a registry's raw answer and what a parser reads out of it.
// It follows the model of Whoice (github.com/XMZO/whoice), where the
// parsers come from, keeping the fields those parsers fill.
package model

// QueryType is what a query names; the module looks up domains only, but
// the parsers tell the types apart.
type QueryType string

// The types of query.
const (
	QueryDomain QueryType = "domain"
	QueryIPv4   QueryType = "ipv4"
	QueryIPv6   QueryType = "ipv6"
	QueryASN    QueryType = "asn"
	QueryCIDR   QueryType = "cidr"
)

// ResultStatus is what an answer says of a name.
type ResultStatus string

// What an answer says of a name.
const (
	StatusRegistered   ResultStatus = "registered"
	StatusUnregistered ResultStatus = "unregistered"
	StatusReserved     ResultStatus = "reserved"
	StatusUnknown      ResultStatus = "unknown"
)

// SourceName is where an answer came from.
type SourceName string

// The sources of answers.
const (
	SourceRDAP     SourceName = "rdap"
	SourceWHOIS    SourceName = "whois"
	SourceWHOISWeb SourceName = "whoisWeb"
	SourceDNS      SourceName = "dns"
)

// NormalizedQuery is a domain as looked up: the name in ASCII, its public
// suffix and the domain registered under it.
type NormalizedQuery struct {
	Input            string
	Query            string
	UnicodeQuery     string
	Type             QueryType
	Host             string
	Suffix           string
	RegisteredDomain string
	ASN              uint32
	// PrivateSuffix marks suffixes run by someone other than the TLD
	// registry (the private section of the Public Suffix List: eu.org,
	// github.io). The TLD registry holds no records for names under them.
	PrivateSuffix bool
}

// RawResponse is a registry's answer as it came.
type RawResponse struct {
	Source      SourceName
	Server      string
	Query       string
	Body        string
	ContentType string
	StatusCode  int
	ElapsedMs   int64
}

// DomainInfo is the name an answer is about.
type DomainInfo struct {
	Name             string
	UnicodeName      string
	PunycodeName     string
	Suffix           string
	RegisteredDomain string
	Reserved         bool
	Registered       bool
}

// RegistryInfo is the registry that answered.
type RegistryInfo struct {
	Name        string
	Website     string
	WHOISServer string
	RDAPServer  string
}

// RegistrarInfo is the registrar of the name.
type RegistrarInfo struct {
	Name        string
	URL         string
	IANAID      string
	Country     string
	Email       string
	Phone       string
	Address     string
	WHOISServer string
	RDAPServer  string
	Source      string
}

// DateInfo holds the dates as the answer writes them; dates.go reads them.
type DateInfo struct {
	CreatedAt   string
	UpdatedAt   string
	ExpiresAt   string
	AvailableAt string
}

// DomainStatus is a status code of the name, as clientHold.
type DomainStatus struct {
	Code        string
	Label       string
	Category    string
	Description string
	URL         string
	Source      string
}

// Nameserver is a name server of the name.
type Nameserver struct {
	Host      string
	Addresses []string
}

// DNSSECInfo says whether the name is signed.
type DNSSECInfo struct {
	Signed *bool
	Text   string
}

// RegistrationField is a field of a record the parsers have no place for.
type RegistrationField struct {
	Label  string
	Value  string
	Source string
}

// RegistrantInfo is who registered the name, where the registry tells.
type RegistrantInfo struct {
	Name         string
	Organization string
	Country      string
	Province     string
	City         string
	Address      string
	PostalCode   string
	Email        string
	Phone        string
	Extra        []RegistrationField
	FieldSources map[string][]RegistrationField
	Source       string
}

// NetworkInfo is a network, for answers about addresses.
type NetworkInfo struct {
	CIDR     string
	Range    string
	Name     string
	Type     string
	OriginAS string
	Country  string
}

// RawData holds the answers as they came, by source.
type RawData struct {
	WHOIS    string
	RDAP     string
	WHOISWeb string
	DNS      string
}

// PartialResult is what one answer tells.
type PartialResult struct {
	Source      SourceName
	Status      ResultStatus
	Domain      DomainInfo
	Registry    RegistryInfo
	Registrar   RegistrarInfo
	Dates       DateInfo
	Statuses    []DomainStatus
	Nameservers []Nameserver
	DNSSEC      DNSSECInfo
	Registrant  RegistrantInfo
	Network     NetworkInfo
	Raw         RawData
	Warnings    []string
}
