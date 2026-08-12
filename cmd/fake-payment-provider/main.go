package main

import (
	"crypto/tls"
	"crypto/x509"
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
	callbackCA := flags.String("callback-ca", "", "optional PEM CA for HTTPS callback verification")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	callbackClient := http.DefaultClient
	if *callbackCA != "" {
		pem, err := os.ReadFile(*callbackCA)
		if err != nil {
			return nil, fmt.Errorf("read callback CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse callback CA %s", *callbackCA)
		}
		callbackClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}}
	}
	handler := paymentprovider.HandlerWithClockAndClient(*callbackSecret, time.Now, callbackClient)
	if *fixedNow != "" {
		instant, err := time.Parse(time.RFC3339, *fixedNow)
		if err != nil {
			return nil, fmt.Errorf("parse fixed clock: %w", err)
		}
		handler = paymentprovider.HandlerWithClockAndClient(*callbackSecret, func() time.Time { return instant }, callbackClient)
	}
	return &http.Server{Addr: *address, Handler: handler}, nil
}
