// Package powersync embeds the reviewed sync-rule and replication-provisioning
// assets so release artifacts cannot omit the data-isolation contract.
package powersync

import _ "embed"

// SyncRules is the account-scoped PowerSync bucket definition.
//
//go:embed sync-rules.yaml
var SyncRules string

// PublicationSQL is applied separately by the replication-role provisioner.
//
//go:embed publication.sql
var PublicationSQL string
