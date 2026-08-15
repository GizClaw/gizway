// Command fake-credit-spy runs the transparent GizPay request counter used by
// Milestone 03 Hurl and E2E contracts.
package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/idy/gizway/internal/testfake/creditspy"
)

func main() {
	flags := flag.NewFlagSet("fake-credit-spy", flag.ExitOnError)
	address := flags.String("address", "127.0.0.1:19400", "listen address")
	upstreamValue := flags.String("upstream", "http://127.0.0.1:18081", "GizPay upstream URL")
	_ = flags.Parse(os.Args[1:])
	upstream, err := url.Parse(*upstreamValue)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr: *address, Handler: creditspy.New(upstream, nil), ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
