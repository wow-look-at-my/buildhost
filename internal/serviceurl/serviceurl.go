// Package serviceurl derives a buildhost service's subdomain base URL from the
// registry's root server URL. Every buildhost service is reached on its own
// subdomain (sites.<domain>, dl.<domain>, ...), never on an apex path prefix.
package serviceurl

import (
	"fmt"
	"net/url"
)

// Base returns the base URL of a buildhost service's subdomain, derived from the
// registry's root server URL: Base("https://pazer.build", "sites") yields
// "https://sites.pazer.build". It errors when server lacks a scheme or host.
func Base(server, service string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q (need scheme://host)", server)
	}
	u.Host = service + "." + u.Host
	return u.Scheme + "://" + u.Host, nil
}
