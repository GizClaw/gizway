package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/idy/gizway/internal/testfake/paymentprovider"
)

func main() {
	server, err := serverFromArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(server.ListenAndServe())
}

func serverFromArgs(args []string) (*http.Server, error) {
	flags := flag.NewFlagSet("fake-payment-provider", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:19100", "listen address")
	callbackSecret := flags.String("callback-secret", "story-callback-secret", "fixture callback signing secret")
	fixedNow := flags.String("fixed-now", "", "optional RFC3339 fixture clock")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	handler := paymentprovider.Handler(*callbackSecret)
	if *fixedNow != "" {
		instant, err := time.Parse(time.RFC3339, *fixedNow)
		if err != nil {
			return nil, fmt.Errorf("parse fixed clock: %w", err)
		}
		handler = paymentprovider.HandlerWithClock(*callbackSecret, func() time.Time { return instant })
	}
	return &http.Server{Addr: *address, Handler: handler}, nil
}
