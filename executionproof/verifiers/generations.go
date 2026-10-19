package verifiers

import "embed"

// modules holds the embedded verifier modules, one per registered ere release
// (built by build.sh, see README.md).
//
//go:embed wasm/*.wasm.sz
var modules embed.FS

// generations lists the embedded ere releases, newest first: the order the
// verifiers of a proof's zkVM are tried in.
var generations = []string{
	"v0.19.2", // sp1 v6.6.0, zisk 1.3.1-alpha, openvm v2.1.0-preview
	"v0.17.2", // sp1 v6.4.0, zisk 1.1.0-alpha, openvm v2.1.0-preview (Lighthouse optional-proofs pin)
}

// Generations returns the embedded ere releases, newest first.
func Generations() []string {
	return append([]string(nil), generations...)
}
