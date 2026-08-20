# Entry delegates the public health contract to the static Caddy image. The
# private Go process health endpoints are used by Compose health checks only.

GET {{pay_url}}/healthz

HTTP 200
[Asserts]
body == ""

GET {{way_url}}/healthz

HTTP 200
[Asserts]
body == ""
