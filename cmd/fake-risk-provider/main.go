// Command fake-risk-provider runs the deterministic external risk fixture.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/idy/gizway/internal/testfake/riskprovider"
)

func main() {
	server, err := serverFromArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(server.ListenAndServe())
}

func serverFromArgs(args []string) (*http.Server, error) {
	flags := flag.NewFlagSet("fake-risk-provider", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:19300", "listen address")
	credential := flags.String("credential", "story-risk-key", "fixture risk bearer credential")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	return &http.Server{Addr: *address, Handler: riskprovider.Handler(*credential), ReadHeaderTimeout: 5 * time.Second}, nil
}
