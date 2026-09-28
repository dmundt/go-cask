// Package landing owns the four decisions a landing makes: the claim on the
// server-side lane, the slot this clone holds while it gates, the stamp the
// pre-push hook reads, and the receipt CI reuses in place of a repeated suite.
//
// Each part states its own rules: claim.go (the lane), lane.go (the slot),
// stamp.go (the stamp), receipt.go (the receipt).
package landing
