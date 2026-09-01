package main

// openapiYAML is the CAS API OpenAPI document served at
// /api/cas/v1/openapi.yaml (R-13): it matches the implemented routes.
const openapiYAML = `openapi: 3.0.3
info:
  title: CASK CAS API (example server)
  description: >
    JSON data API of the content-addressed object store (cas-api spec).
    Objects are raw bytes addressed by the hash of their content, immutable
    and deduplicated. All endpoints are rate-limited per caller IP.
  version: 1.0.0
servers:
  - url: /api/cas/v1
paths:
  /objects:
    post:
      summary: Store an object
      description: Computes the hash from the body with ?algo (default sha256); deduplicates.
      parameters:
        - name: algo
          in: query
          schema: { type: string, default: sha256 }
      responses:
        '201':
          description: Stored (deduplicated: true when it already existed)
          content:
            application/json:
              schema:
                type: object
                properties:
                  hash: { type: string }
                  deduplicated: { type: boolean }
        '400': { description: Unknown algorithm or empty body }
        '429': { description: Rate limited }
    get:
      summary: List objects
      parameters:
        - name: algo
          in: query
          schema: { type: string }
        - name: limit
          in: query
          schema: { type: integer, default: 100, minimum: 1, maximum: 1000 }
        - name: offset
          in: query
          schema: { type: integer, default: 0, minimum: 0 }
      responses:
        '200':
          description: Object list
          content:
            application/json:
              schema:
                type: object
                properties:
                  total: { type: integer }
                  objects:
                    type: array
                    items:
                      type: object
                      properties:
                        hash: { type: string }
                        algorithm: { type: string }
                        size: { type: integer }
  /objects/{hash}:
    get:
      summary: Retrieve object bytes
      description: Streams the raw bytes with X-CAS-Algorithm/Size/Type headers.
      parameters:
        - name: hash
          in: path
          required: true
          schema: { type: string, pattern: '^[a-z0-9]+:[0-9a-f]+$' }
      responses:
        '200':
          description: Stored bytes
          content:
            application/octet-stream: { schema: { type: string, format: binary } }
        '400': { description: Malformed hash }
        '404': { description: Not found }
    delete:
      summary: Delete object (admin)
      responses:
        '204': { description: Deleted }
        '403': { description: Forbidden }
  /objects/{hash}/meta:
    get:
      summary: Object metadata
      responses:
        '200':
          description: Metadata
          content:
            application/json:
              schema:
                type: object
                properties:
                  hash: { type: string }
                  algorithm: { type: string }
                  size: { type: integer }
                  type: { type: string }
                  references: { type: array, items: { type: string } }
  /objects/{hash}/verify:
    post:
      summary: Verify object integrity (operator)
      responses:
        '200':
          description: Verification result
          content:
            application/json:
              schema:
                type: object
                properties:
                  hash: { type: string }
                  valid: { type: boolean }
                  recomputed: { type: string }
  /stats:
    get:
      summary: Storage statistics
      responses:
        '200':
          description: Statistics
          content:
            application/json:
              schema:
                type: object
                properties:
                  object_count: { type: integer }
                  total_size: { type: integer }
                  algorithm_counts:
                    type: object
                    additionalProperties: { type: integer }
  /gc:
    post:
      summary: Run mark-and-sweep garbage collection (admin)
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                reachable: { type: array, items: { type: string } }
      responses:
        '200':
          description: GC result
          content:
            application/json:
              schema:
                type: object
                properties:
                  deleted: { type: integer }
  /openapi.yaml:
    get:
      summary: This OpenAPI document
      responses:
        '200': { description: OpenAPI 3.0 document (text/yaml) }
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
security:
  - bearerAuth: []
`
