# Regional Entry is API-only. The removed Caddy image no longer owns a public
# health route; private process health remains internal to Compose.

GET {{pay_url}}/healthz

HTTP 404

GET {{way_url}}/healthz

HTTP 404
