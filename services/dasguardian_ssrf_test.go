package services

import (
	"context"
	"crypto/ecdsa"
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/p2p/enode"
	dasguardian "github.com/ethpandaops/eth-das-guardian"
	gdapi "github.com/ethpandaops/eth-das-guardian/api"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

// TestValidateScanTarget_RejectsNonRoutableAddresses covers every non-routable class an
// attacker-supplied ENR could point at, including 169.254.169.254 - the cloud metadata
// endpoint on AWS/GCP/Azure - and confirms an ordinary public address is still accepted.
func TestValidateScanTarget_RejectsNonRoutableAddresses(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := key.Public().(*ecdsa.PublicKey)

	cases := []struct {
		name      string
		ip        string
		wantError bool
	}{
		{"loopback", "127.0.0.1", true},
		{"loopback IPv6", "::1", true},
		{"private 10/8", "10.1.2.3", true},
		{"private 172.16/12", "172.16.0.1", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link-local incl. cloud metadata", "169.254.169.254", true},
		{"unspecified", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"ordinary public address", "8.8.8.8", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := enode.NewV4(pub, net.ParseIP(tc.ip), 30303, 30303)
			err := validateScanTarget(node)
			if tc.wantError && err == nil {
				t.Fatalf("expected %s to be rejected, got nil error", tc.ip)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected %s to be accepted, got error: %v", tc.ip, err)
			}
		})
	}
}

// fakeLocalBeaconAPI supplies only what DASGuardian.init() needs to derive the scanner's
// own local identity/status - unrelated to the target-validation fix under test here.
type fakeLocalBeaconAPI struct{}

func (f *fakeLocalBeaconAPI) Init(ctx context.Context) error { return nil }
func (f *fakeLocalBeaconAPI) GetStateVersion() string        { return "electra" }
func (f *fakeLocalBeaconAPI) GetForkDigest(slot uint64) ([]byte, error) {
	return []byte{0, 0, 0, 0}, nil
}
func (f *fakeLocalBeaconAPI) GetFinalizedCheckpoint() *phase0.Checkpoint {
	return &phase0.Checkpoint{}
}
func (f *fakeLocalBeaconAPI) GetLatestBlockHeader() *phase0.BeaconBlockHeader {
	return &phase0.BeaconBlockHeader{}
}
func (f *fakeLocalBeaconAPI) GetFuluForkEpoch() uint64  { return 0 }
func (f *fakeLocalBeaconAPI) GetGloasForkEpoch() uint64 { return 0 }
func (f *fakeLocalBeaconAPI) GetNodeIdentity(ctx context.Context) (*gdapi.NodeIdentity, error) {
	return &gdapi.NodeIdentity{}, nil
}
func (f *fakeLocalBeaconAPI) GetBeaconBlock(ctx context.Context, slot any) (*spec.VersionedSignedBeaconBlock, error) {
	return nil, nil
}
func (f *fakeLocalBeaconAPI) ReadSpecParameter(key string) (any, bool) { return nil, false }

// TestScanNode_RejectsLoopbackTargetBeforeDialing is the end-to-end regression test for
// the SSRF finding: it drives the real, fixed services.DasGuardian.ScanNode (not just the
// validator function in isolation) against an ENR pointing at a local victim listener, and
// asserts no connection ever reaches it. Before the fix this is exactly the PoC that
// proved dora's server would dial the attacker-chosen address.
func TestScanNode_RejectsLoopbackTargetBeforeDialing(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start victim listener: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	accepted := make(chan net.Addr, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn.RemoteAddr()
		conn.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	guardian, err := dasguardian.NewDASGuardian(ctx, &dasguardian.DasGuardianConfig{
		Logger:            logrus.StandardLogger(),
		Libp2pHost:        "127.0.0.1",
		BeaconAPI:         &fakeLocalBeaconAPI{},
		ConnectionRetries: 1,
		ConnectionTimeout: 3 * time.Second,
		InitTimeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewDASGuardian failed (local-identity stub insufficient): %v", err)
	}
	defer guardian.Close()

	d := &DasGuardian{guardian: guardian}

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := key.Public().(*ecdsa.PublicKey)
	node := enode.NewV4(pub, net.ParseIP("127.0.0.1"), port, port)

	_, scanErr := d.ScanNode(ctx, node.String(), nil)
	if scanErr == nil {
		t.Fatalf("expected ScanNode to reject a loopback target, got nil error")
	}
	t.Logf("ScanNode correctly rejected the target: %v", scanErr)

	select {
	case remote := <-accepted:
		t.Fatalf("BUG: victim listener accepted a connection from %s - ScanNode dialed the loopback target instead of rejecting it", remote.String())
	case <-time.After(2 * time.Second):
		// no connection arrived - correct
	}
}
