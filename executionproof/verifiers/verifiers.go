// Package verifiers verifies EIP-8025 execution proofs with the zkVM verifiers
// of ere, embedded as wasm32 modules and run in process. A module is compiled
// on first use (cached on disk) and instantiated per verification, so no
// verifier stays in memory between proofs.
package verifiers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/golang/snappy"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/ethpandaops/dora/executionproof"
)

// Status is the outcome of a verification.
type Status uint8

const (
	// StatusUnknown means the proof was not verified (yet).
	StatusUnknown Status = 0
	// StatusValid means a verifier accepted the proof and its public values.
	StatusValid Status = 1
	// StatusInvalid means every verifier of the proof's zkVM rejected the proof.
	StatusInvalid Status = 2
	// StatusUnverifiable means no embedded verifier handles the proof's zkVM.
	StatusUnverifiable Status = 3
)

// String returns the status name as shown on the page.
func (s Status) String() string {
	switch s {
	case StatusValid:
		return "valid"
	case StatusInvalid:
		return "invalid"
	case StatusUnverifiable:
		return "unverifiable"
	}
	return "unknown"
}

// Result is the outcome of verifying one proof.
type Result struct {
	Status Status
	// Generation is the ere release whose verifier produced the outcome.
	Generation string
	// PublicValues are the public values a valid proof commits to.
	PublicValues []byte
	// Reason tells why a proof is invalid or unverifiable.
	Reason string
}

// the shim's ABI (executionproof/verifiers/shim)
const (
	kindSP1    uint64 = 1
	kindZisk   uint64 = 2
	kindOpenVM uint64 = 3

	errProgramVK    int32 = -1
	errProofDecode  int32 = -2
	errVerification int32 = -3
)

var kinds = map[executionproof.ZkVM]uint64{
	executionproof.ZkVMSP1:    kindSP1,
	executionproof.ZkVMZisk:   kindZisk,
	executionproof.ZkVMOpenVM: kindOpenVM,
}

// Verifiers runs the embedded verifier modules. A module is compiled and
// instantiated for one verification and released afterwards; the compilation
// cache on disk keeps the compile step at a few milliseconds after the first.
type Verifiers struct {
	runtime wazero.Runtime
	// stubbed holds the import modules provided so far: the verifiers' wasm
	// dependencies declare a few host functions (wasm-bindgen glue) that a
	// verification never calls.
	stubbed   map[string]bool
	stubbedMu sync.Mutex
}

// New prepares the verifiers. cacheDir keeps the compiled modules across
// starts; empty for no persistent cache.
func New(ctx context.Context, cacheDir string) (*Verifiers, error) {
	config := wazero.NewRuntimeConfig()
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("verifier cache dir: %w", err)
		}
		cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(cacheDir, "wazero"))
		if err != nil {
			return nil, fmt.Errorf("verifier compilation cache: %w", err)
		}
		config = config.WithCompilationCache(cache)
	}

	return &Verifiers{
		runtime: wazero.NewRuntimeWithConfig(ctx, config),
		stubbed: make(map[string]bool, 2),
	}, nil
}

// Close releases the runtime.
func (v *Verifiers) Close(ctx context.Context) error {
	return v.runtime.Close(ctx)
}

// Verify verifies a proof of the given zkVM against the program verification
// key, trying the embedded generations newest first: a proof verifies with the
// verifier of the release it was made with. The public values of a valid proof
// are returned for the caller to compare with the expected public input.
func (v *Verifiers) Verify(ctx context.Context, zkvm executionproof.ZkVM, programVK []byte, proof []byte) *Result {
	kind, ok := kinds[zkvm]
	if !ok {
		return &Result{Status: StatusUnverifiable, Reason: fmt.Sprintf("no verifier for %s", zkvm)}
	}

	// a proof no generation accepts is reported with the newest one's verdict
	var newest *Result
	for _, generation := range generations {
		result, err := v.verifyWith(ctx, generation, kind, programVK, proof)
		if err != nil {
			return &Result{Status: StatusUnverifiable, Generation: generation, Reason: err.Error()}
		}
		if result.Status == StatusValid {
			return result
		}
		if newest == nil {
			newest = result
		}
	}
	if newest == nil {
		return &Result{Status: StatusUnverifiable, Reason: "no verifier generation embedded"}
	}

	return newest
}

// verifyWith runs one generation's verifier on the proof.
func (v *Verifiers) verifyWith(ctx context.Context, generation string, kind uint64, programVK []byte, proof []byte) (*Result, error) {
	compiled, err := v.compile(ctx, generation)
	if err != nil {
		return nil, err
	}
	defer func() { _ = compiled.Close(ctx) }()
	if err := v.stubImports(ctx, compiled); err != nil {
		return nil, err
	}

	instance, err := v.runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate verifier %s: %w", generation, err)
	}
	defer func() { _ = instance.Close(ctx) }()

	vkPtr, err := write(ctx, instance, programVK)
	if err != nil {
		return nil, err
	}
	proofPtr, err := write(ctx, instance, proof)
	if err != nil {
		return nil, err
	}

	ret, err := instance.ExportedFunction("ere_verify").Call(ctx, kind, vkPtr, uint64(len(programVK)), proofPtr, uint64(len(proof)))
	if err != nil {
		return nil, fmt.Errorf("verifier %s: %w", generation, err)
	}
	code := int32(ret[0])
	result := &Result{Generation: generation}
	switch {
	case code >= 0:
		outPtr, err := instance.ExportedFunction("ere_out_ptr").Call(ctx)
		if err != nil {
			return nil, fmt.Errorf("verifier %s: %w", generation, err)
		}
		values, ok := instance.Memory().Read(uint32(outPtr[0]), uint32(code))
		if !ok {
			return nil, fmt.Errorf("verifier %s: public values out of range", generation)
		}
		result.Status = StatusValid
		result.PublicValues = append([]byte(nil), values...)
	case code == errProgramVK:
		result.Status = StatusInvalid
		result.Reason = "program verification key not accepted"
	case code == errProofDecode:
		result.Status = StatusInvalid
		result.Reason = "proof encoding not accepted"
	case code == errVerification:
		result.Status = StatusInvalid
		result.Reason = "proof rejected"
	default:
		return nil, fmt.Errorf("verifier %s: unexpected result %d", generation, code)
	}

	return result, nil
}

// compile compiles the embedded module of a generation.
func (v *Verifiers) compile(ctx context.Context, generation string) (wazero.CompiledModule, error) {
	packed, err := modules.ReadFile("wasm/ere-" + generation + ".wasm.sz")
	if err != nil {
		return nil, fmt.Errorf("verifier %s: %w", generation, err)
	}
	wasm, err := snappy.Decode(nil, packed)
	if err != nil {
		return nil, fmt.Errorf("verifier %s: %w", generation, err)
	}
	compiled, err := v.runtime.CompileModule(ctx, wasm)
	if err != nil {
		return nil, fmt.Errorf("compile verifier %s: %w", generation, err)
	}

	return compiled, nil
}

// stubImports provides the host functions a module imports. The verifiers never
// call them; a call traps the verification.
func (v *Verifiers) stubImports(ctx context.Context, compiled wazero.CompiledModule) error {
	v.stubbedMu.Lock()
	defer v.stubbedMu.Unlock()

	builders := make(map[string]wazero.HostModuleBuilder, 2)
	for _, imported := range compiled.ImportedFunctions() {
		module, name, _ := imported.Import()
		if v.stubbed[module] {
			continue
		}
		builder := builders[module]
		if builder == nil {
			builder = v.runtime.NewHostModuleBuilder(module)
			builders[module] = builder
		}
		qualified := module + "." + name
		builder.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(context.Context, api.Module, []uint64) {
			panic("verifier called host import " + qualified)
		}), imported.ParamTypes(), imported.ResultTypes()).Export(name)
	}
	for module, builder := range builders {
		if _, err := builder.Instantiate(ctx); err != nil {
			return fmt.Errorf("verifier import module %s: %w", module, err)
		}
		v.stubbed[module] = true
	}

	return nil
}

// write copies data into the module memory and returns its address.
func write(ctx context.Context, instance api.Module, data []byte) (uint64, error) {
	ret, err := instance.ExportedFunction("ere_alloc").Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("verifier alloc: %w", err)
	}
	if !instance.Memory().Write(uint32(ret[0]), data) {
		return 0, fmt.Errorf("verifier alloc out of range")
	}
	return ret[0], nil
}
