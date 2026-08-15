// Package store retains only the provider execution value shared with the
// embedded Bifrost adapter. Milestone 03 persistence lives in the owning
// GizPay and GizWay packages rather than the removed pre-refactor repository.
package store

type ProviderExecutionTarget struct {
	Provider   string
	Endpoint   string
	Credential string
	Model      string
	RouteKey   string
	Weight     int
}
