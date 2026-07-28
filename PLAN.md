# Plan: Full Multimodal and File Support

## Goal

Support OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages image/document inputs across routed providers. Resolve remote URLs and provider-specific file IDs temporarily in memory, without persistent storage, and reject unsupported data explicitly instead of dropping it.

## Scope

- Preserve OpenAI-native image and file payloads for OpenAI providers.
- Convert OpenAI content parts to Anthropic image and document blocks.
- Convert Anthropic image and document blocks to OpenAI-compatible content parts.
- Resolve data URLs, public HTTP(S) URLs, and provider file IDs in memory.
- Add OpenAI-compatible `/v1/files` passthrough for upload, list, retrieve, and delete.
- Keep provider credentials and route failover behavior consistent with existing proxy code.
- Do not persist file bytes, introduce a database, or fabricate cross-provider file IDs.

## Safety and Limits

- Accept only HTTP(S) URLs and supported MIME types.
- Reject loopback, private, link-local, metadata, and otherwise non-public URL destinations.
- Enforce request and downloaded-file size limits and fetch timeouts.
- Validate data URLs and base64 content before conversion.
- Return explicit `invalid_request`, `unsupported_file`, or upstream errors.
- Never log raw image/document bytes.

## Implementation

- [x] Add a small multimodal adapter for content-part normalization and OpenAI/Anthropic conversion.
- [x] Add an in-memory resolver using the existing HTTP client and route authentication.
- [x] Integrate conversion into OpenAI-to-Anthropic and Anthropic-to-OpenAI request paths.
- [x] Add `/v1/files` route and multipart passthrough without local file storage.
- [x] Preserve text, tools, system prompts, streaming, and existing response behavior.

## Tests

- [x] Unit tests for text, image URL, image data URL, PDF/document, and file ID conversion in both directions.
- [x] Tests for malformed data URLs and unsupported references.
- [x] Handler/proxy tests asserting outbound request shapes for OpenAI and Anthropic routes.
- [x] `/v1/files` passthrough tests for model selection and upstream response forwarding.
- [x] Tests confirming text-only requests and streamed responses remain unchanged.

## Verification

- [x] Run `gofmt` on changed Go files.
- [x] Run `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Run `go build ./...`.
- [x] Do not start a server or touch port `1765`.

## Acceptance Boundary

"Full" means all documented content shapes that RouterLLM can access and safely resolve. Provider-specific file IDs are downloaded through the originating provider and converted temporarily; they are not reusable IDs at another provider. If an upstream cannot expose a referenced file or model does not support its MIME type, the request fails explicitly rather than silently losing content.
