package main

import _ "embed"

// openapiYAML is the CAS API OpenAPI document served at
// /api/cas/v1/openapi.yaml (R-13): it matches the implemented routes. It
// lives in a separate file (openapi.yaml), embedded into the binary — never
// an inline Go string (api-design §13).
//
//go:embed openapi.yaml
var openapiYAML []byte
