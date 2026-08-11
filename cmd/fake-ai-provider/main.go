// Command fake-ai-provider runs the deterministic upstream used by Hurl.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/idy/gizway/internal/testfake/aiprovider"
)

func main() {
	server, err := serverFromArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(server.ListenAndServe())
}

func serverFromArgs(args []string) (*http.Server, error) {
	flags := flag.NewFlagSet("fake-ai-provider", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:19000", "listen address")
	callbackSecret := flags.String("callback-secret", "story-ai-callback-secret", "fixture Realtime usage signing secret")
	credential := flags.String("credential", "story-provider-key", "fixture provider bearer credential")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	return &http.Server{Addr: *address, Handler: aiprovider.HandlerWithCredential(*credential, *callbackSecret), ReadHeaderTimeout: 5 * time.Second}, nil
}
