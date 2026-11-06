package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// happRoutingLinkPrefix also activates the profile: Happ turns routing on after the geo files load.
const happRoutingLinkPrefix = "happ://routing/onadd/"

// happProfileName is stable because Happ updates a profile with the same name instead of adding another.
const happProfileName = "giocondo"

// happProfile is a Happ routing profile. Happ fills omitted fields, DNS included, from its default profile.
// GlobalProxy is the string "true", as in the Happ documentation examples.
type happProfile struct {
	Name           string   `json:"Name"`
	GlobalProxy    string   `json:"GlobalProxy"`
	RouteOrder     string   `json:"RouteOrder"`
	DomainStrategy string   `json:"DomainStrategy"`
	GeoIPURL       string   `json:"Geoipurl"`
	GeoSiteURL     string   `json:"Geositeurl"`
	ProxySites     []string `json:"ProxySites"`
	ProxyIP        []string `json:"ProxyIp"`
	DirectSites    []string `json:"DirectSites"`
	DirectIP       []string `json:"DirectIp"`
}

// HappRoutingLink returns the value of the routing response header that delivers the split routing to Happ.
func HappRoutingLink() (string, error) {
	directSites := make([]string, 0, len(directSuffixes)+2)
	for _, s := range directSuffixes {
		directSites = append(directSites, "domain:"+s)
	}

	directSites = append(directSites, "geosite:category-ru", "geosite:ru-available-only-inside")

	data, err := json.Marshal(happProfile{
		Name:           happProfileName,
		GlobalProxy:    "true",
		RouteOrder:     "block-proxy-direct",
		DomainStrategy: "IPIfNonMatch",
		GeoIPURL:       geoipDatURL,
		GeoSiteURL:     geositeDatURL,
		ProxySites:     []string{"geosite:ru-blocked"},
		ProxyIP:        []string{"geoip:ru-blocked"},
		DirectSites:    directSites,
		DirectIP:       append(append([]string{}, privateCIDRs...), "geoip:ru"),
	})
	if err != nil {
		return "", fmt.Errorf("encode happ routing profile: %w", err)
	}

	return happRoutingLinkPrefix + base64.StdEncoding.EncodeToString(data), nil
}

// IsHapp reports whether the subscription client is Happ. The routing header is sent only to it:
// v2RayTun reads a header with the same name in another format.
func IsHapp(userAgent string) bool {
	return strings.HasPrefix(strings.ToLower(userAgent), "happ/")
}
