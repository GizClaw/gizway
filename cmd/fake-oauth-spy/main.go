// Command fake-oauth-spy runs the transparent ZITADEL token observer used by
// Milestone 03 Hurl and E2E contracts.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/idy/gizway/internal/testfake/oauthspy"
)

func main() {
	flags := flag.NewFlagSet("fake-oauth-spy", flag.ExitOnError)
	address := flags.String("address", "127.0.0.1:19500", "listen address")
	upstreamValue := flags.String("upstream", "http://127.0.0.1:18082", "ZITADEL upstream URL")
	_ = flags.Parse(os.Args[1:])
	upstream, err := url.Parse(*upstreamValue)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr: *address, Handler: oauthspy.New(upstream, nil), ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
