// Package protocol implements the kicker module wire protocol v1 (spec 01):
// line framing, message types for both directions, strict decoding with
// warnings for unknown fields, the capability catalog, sequence-number
// deduplication and JSON Schema generation for third-party module builders.
//
// It is the single source of truth for module messages. The schemas in
// /catalog are generated from these types:
//
//go:generate go run ./gen -out ../../../catalog
package protocol

// Version is the protocol version this implementation speaks (spec 01).
const Version = 1

// MaxLine is the maximum length of one line in bytes, including the newline (spec 01 §2).
const MaxLine = 1024

// MaxCapabilities is the maximum number of capability instances one module may announce (spec 01 §5).
const MaxCapabilities = 16
