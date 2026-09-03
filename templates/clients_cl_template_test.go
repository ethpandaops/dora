package templates

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"github.com/ethpandaops/dora/types/models"
)

// The consensus clients page must stay small regardless of the peer count: the node
// list, the peer map and the custody table are loaded lazily instead of being embedded.
func TestClientsCLTemplateEmbedsNoNodeData(t *testing.T) {
	body, err := Files.ReadFile("clients/clients_cl.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	const externalPeerID = "peer-external-only-known-from-node-list"
	const externalENR = "enr:-external-peer-record"

	data := &models.ClientsCLPageData{
		Clients: []*models.ClientsCLPageDataClient{
			{Index: 1, Name: "client-1", PeerID: "peer-client-one", Status: "online", HeadRoot: []byte{0x01}, SafeRoot: []byte{0x01}},
		},
		ClientCount:            1,
		PeerMap:                &models.ClientCLPageDataPeerMap{},
		ShowSensitivePeerInfos: true,
		ShowPeerDASInfos:       true,
		PeerDASInfos: &models.ClientCLPagePeerDAS{
			NumberOfColumns:              128,
			CustodyRequirement:           4,
			DataColumnSidecarSubnetCount: 128,
			Warnings: models.ClientCLPageDataPeerDASWarnings{
				HasWarnings:            true,
				MissingCGCFromENRPeers: []string{externalPeerID},
			},
		},
		Nodes: []*models.ClientCLPageDataNode{
			{PeerID: "peer-client-one", Alias: "client-1", Type: "internal", PeerDAS: &models.ClientCLPageDataNodePeerDAS{Columns: []uint64{1, 2}}},
			{PeerID: externalPeerID, Alias: externalPeerID, Type: "external", ENR: externalENR, PeerDAS: &models.ClientCLPageDataNodePeerDAS{Columns: []uint64{3}}},
		},
		Sorting: "name",
	}

	var out bytes.Buffer
	for _, name := range []string{"page", "js"} {
		if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatalf("execute %v: %v", name, err)
		}
	}
	got := out.String()

	// the external peer is referenced by the warnings modal, but its ENR must only be
	// reachable through the lazily loaded node list
	if strings.Contains(got, externalENR) {
		t.Errorf("rendered page embeds node data (found ENR of an external peer)")
	}
	if strings.Contains(got, "peerdas-table-template") {
		t.Errorf("rendered page still carries the server-rendered custody table")
	}

	for _, want := range []string{
		`fetchJSON('/clients/consensus/nodes')`,
		`fetchJSON('/clients/consensus/peermap')`,
		`'/clients/consensus/nodes/' + encodeURIComponent(peerId) + '/peers'`,
		`numberOfColumns: 128,`,
		`id="peerdas-table-container"`,
		`id="dasSupernodesModal"`,
		`Total peers: 2`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}
