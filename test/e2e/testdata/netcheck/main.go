// netcheck is the entrypoint of the container image synthesized by buildhost in the
// end-to-end test. It proves the synthesized image is usable for a networked service:
// it loads the CA bundle that buildhost baked into the image and uses it -- and only it
// -- as the trust root for a real outbound HTTPS request. A certless "FROM scratch"
// image (buildhost's old behaviour) would fail at the TLS handshake with
// "x509: certificate signed by unknown authority"; with the embedded Mozilla bundle it
// succeeds.
//
// CA bundle path comes from SSL_CERT_FILE (set in the synthesized image's config) or the
// linux default, so the same binary works run as a container (docker run) or unpacked on
// the host (crane export).
package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	path := os.Getenv("SSL_CERT_FILE")
	if path == "" {
		path = "/etc/ssl/certs/ca-certificates.crt"
	}

	pem, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("FAIL: reading CA bundle %s: %v\n", path, err)
		os.Exit(1)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		fmt.Printf("FAIL: CA bundle %s has no usable certificates\n", path)
		os.Exit(1)
	}
	fmt.Printf("CA bundle OK: %d bytes, parsed from %s\n", len(pem), path)

	// Trust ONLY the image's bundle, so this verifies that bundle specifically.
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	hosts := []string{"https://www.google.com", "https://cloudflare.com", "https://api.github.com"}
	for _, u := range hosts {
		resp, err := client.Get(u)
		if err != nil {
			fmt.Printf("  %s: %v\n", u, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("OK: %s -> HTTP %d (verified against the image's CA bundle)\n", u, resp.StatusCode)
		os.Exit(0)
	}
	fmt.Println("FAIL: no HTTPS endpoint validated against the image's CA bundle")
	os.Exit(1)
}
