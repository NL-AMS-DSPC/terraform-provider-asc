package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/nl-ams-dspc/terraform-provider-asc/internal/client"
)

const (
	vnicTestID       = "00000000-0000-4000-8000-000000000031"
	vnicTestURN      = "asc:atos:network:test-tenant:vnic:" + vnicTestID
	vnicTestSubnet   = "asc:atos:network:test-tenant:subnet:00000000-0000-4000-8000-000000000002"
	vnicTestPublicIP = "203.0.113.5"
)

// vnicGateway serves one VNIC that settles from pending after its first read, with a
// public IP. A PUT renames it and, like the API, sets the public IP it carries,
// clearing it when left out. A PATCH applies tag entries, an empty value deleting its key.
type vnicGateway struct {
	mu      sync.Mutex
	vnic    *client.VNIC
	reads   int
	posts   int
	deletes int
}

func (g *vnicGateway) handlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"POST /api/infra-gateway/vnics": func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name      string       `json:"name"`
				SubnetURN string       `json:"subnetURN"`
				Tags      []client.Tag `json:"tags"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			defer g.mu.Unlock()
			g.posts++
			g.reads = 0
			name := body.Name
			if name == "" {
				name = "eth0"
			}
			g.vnic = &client.VNIC{URN: vnicTestURN, Name: name, SubnetURN: body.SubnetURN, Status: "pending", Tags: body.Tags}
			writeData(w, http.StatusCreated, *g.vnic)
		},
		"GET /api/infra-gateway/vnics/" + vnicTestID: func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.vnic == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			g.reads++
			if g.reads > 1 && g.vnic.Status == "pending" {
				g.vnic.Status, g.vnic.PrivateIP, g.vnic.MAC = "detached", "10.0.0.5", "00:00:00:00:00:05"
				g.vnic.PublicIP = vnicTestPublicIP
			}
			writeData(w, http.StatusOK, *g.vnic)
		},
		"PUT /api/infra-gateway/vnics/" + vnicTestID: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name     *string `json:"name"`
				PublicIP string  `json:"publicIP"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			defer g.mu.Unlock()
			if body.Name != nil {
				g.vnic.Name = *body.Name
			}
			g.vnic.PublicIP = body.PublicIP
			writeData(w, http.StatusAccepted, *g.vnic)
		},
		"PATCH /api/infra-gateway/vnics/" + vnicTestID: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Tags []client.Tag `json:"tags"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			defer g.mu.Unlock()
			for _, tag := range body.Tags {
				g.vnic.Tags = withTag(g.vnic.Tags, tag)
			}
			writeData(w, http.StatusOK, *g.vnic)
		},
		"DELETE /api/infra-gateway/vnics/" + vnicTestID: func(w http.ResponseWriter, _ *http.Request) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.deletes++
			g.vnic = nil
			writeData(w, http.StatusAccepted, map[string]string{"deleted": vnicTestID})
		},
	}
}

// withTag sets tag, or removes its key when the value is empty.
func withTag(tags []client.Tag, tag client.Tag) []client.Tag {
	result := []client.Tag{}
	for _, existing := range tags {
		if existing.Key != tag.Key {
			result = append(result, existing)
		}
	}
	if tag.Value != "" {
		result = append(result, tag)
	}
	return result
}

func TestVNICProvisioning(t *testing.T) {
	gateway := &vnicGateway{}
	environment := newTestEnvironment(t, gateway.handlers())

	config := func(body string) string {
		return environment.providerConfig() + fmt.Sprintf(`
resource "asc_vnic" "test" {
  subnet_urn = %q
%s
}`, vnicTestSubnet, body)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config(`  tags = { env = "test", owner = "platform" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vnic.test", "id", vnicTestID),
				resource.TestCheckResourceAttr("asc_vnic.test", "urn", vnicTestURN),
				// An omitted name is chosen by ASC.
				resource.TestCheckResourceAttr("asc_vnic.test", "name", "eth0"),
				resource.TestCheckResourceAttr("asc_vnic.test", "status", "detached"),
				resource.TestCheckResourceAttr("asc_vnic.test", "private_ip", "10.0.0.5"),
				resource.TestCheckResourceAttr("asc_vnic.test", "vm_urn", ""),
			),
		},
		{
			Config:           config(`  name = "eth1"` + "\n" + `  tags = { env = "prod" }`),
			ConfigPlanChecks: inPlace("asc_vnic.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_vnic.test", "name", "eth1"),
				// The rename must not drop the public IP.
				resource.TestCheckResourceAttr("asc_vnic.test", "public_ip", vnicTestPublicIP),
				resource.TestCheckResourceAttr("asc_vnic.test", "tags.env", "prod"),
				resource.TestCheckNoResourceAttr("asc_vnic.test", "tags.owner"),
				resource.TestCheckResourceAttr("asc_vnic.test", "private_ip", "10.0.0.5"),
			),
		},
		{ResourceName: "asc_vnic.test", ImportState: true, ImportStateId: vnicTestID, ImportStateVerify: true},
		{
			Config: environment.providerConfig() + `
resource "asc_vnic" "test" {
  subnet_urn = "asc:atos:network:test-tenant:subnet:00000000-0000-4000-8000-000000000003"
  name       = "eth1"
  tags       = { env = "prod" }
}`,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("asc_vnic.test", plancheck.ResourceActionDestroyBeforeCreate),
			}},
		},
	}})

	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.posts != 2 || gateway.deletes != 2 {
		t.Fatalf("POST count = %d, DELETE count = %d, want 2 each (create, replacement)", gateway.posts, gateway.deletes)
	}
}
