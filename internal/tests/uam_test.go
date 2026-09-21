package tests

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	// The role item route uses the tenant UUID; other routes use the namespace.
	uamTenantUUID = "00000000-0000-4000-8000-0000000000aa"
	uamTenant     = "/api/uam/v1/tenants/test-tenant"
	uamRoleList   = "/api/uam/v1/tenants/test-tenant/roles"
	uamRoleWrite  = "/api/uam/v1/tenants/test-tenant/roles/operator"
	uamRoleRead   = "/api/uam/v1/tenants/" + uamTenantUUID + "/roles/operator"
	uamGroupList  = "/api/uam/v1/tenants/groups"
	uamGroupItem  = "/api/uam/v1/tenants/groups/platform"
)

// writeBare mirrors UAM responses without a data envelope.
func writeBare(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// roleStore returns UUIDs from the list and names plus composites from the item route.
type roleStore struct {
	mu      sync.Mutex
	actions map[string][]string
	ids     map[string]string
}

func newRoleStore() *roleStore {
	return &roleStore{actions: map[string][]string{}, ids: map[string]string{}}
}

func (s *roleStore) handlers(t *testing.T, updates *atomic.Int64) map[string]http.HandlerFunc {
	t.Helper()
	return map[string]http.HandlerFunc{
		"GET " + uamTenant: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": uamTenantUUID, "name": "test-tenant"})
		},
		// Mirror the UAM defect: a namespace-addressed item read cannot find the role.
		"GET " + uamRoleWrite: func(w http.ResponseWriter, _ *http.Request) {
			t.Errorf("provider read the role item route under the namespace, which UAM answers with 502")
			w.WriteHeader(http.StatusBadGateway)
		},
		"GET " + uamRoleList: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			listed := make([]map[string]any, 0, len(s.actions))
			for name := range s.actions {
				listed = append(listed, map[string]any{"id": s.ids[name], "name": name, "description": ""})
			}
			writeBare(w, http.StatusOK, listed)
		},
		"POST " + uamRoleList: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name    string   `json:"name"`
				Actions []string `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create role body: %v", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.actions[body.Name] = body.Actions
			s.ids[body.Name] = "00000000-0000-4000-8000-000000000010"
			writeBare(w, http.StatusCreated, map[string]string{"message": "role created successfully"})
		},
		"GET " + uamRoleRead: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			actions, ok := s.actions["operator"]
			if !ok {
				// UAM answers a missing role this way, which is exactly why the
				// provider must not read existence from this route.
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			writeBare(w, http.StatusOK, map[string]any{
				"id": "operator", "urn": "asc:asc:iam:test-tenant:role:operator",
				"name": "operator", "composites": actions,
			})
		},
		"PUT " + uamRoleWrite: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Actions []string `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update role body: %v", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			// UAM upserts by UUID; a different ID would create a duplicate.
			if body.ID != s.ids[body.Name] {
				t.Errorf("update role id = %q, want the listed uuid %q", body.ID, s.ids[body.Name])
			}
			s.actions[body.Name] = body.Actions
			updates.Add(1)
			writeBare(w, http.StatusOK, map[string]string{"message": "role updated successfully"})
		},
		"DELETE " + uamRoleWrite: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.actions, "operator")
			delete(s.ids, "operator")
			w.WriteHeader(http.StatusNoContent)
		},
	}
}

func TestRoleProvisioning(t *testing.T) {
	var updates atomic.Int64
	store := newRoleStore()
	environment := newTestEnvironment(t, store.handlers(t, &updates))

	config := func(actions string) string {
		return environment.providerConfig() + `
resource "asc_role" "test" {
  name    = "operator"
  actions = ` + actions + `
}`
	}

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config(`["vm:read"]`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_role.test", "name", "operator"),
				resource.TestCheckResourceAttr("asc_role.test", "urn", "asc:asc:iam:test-tenant:role:operator"),
				resource.TestCheckResourceAttr("asc_role.test", "actions.#", "1"),
				resource.TestCheckTypeSetElemAttr("asc_role.test", "actions.*", "vm:read"),
			),
		},
		{
			Config:           config(`["vm:read", "vm:write"]`),
			ConfigPlanChecks: inPlace("asc_role.test"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_role.test", "actions.#", "2"),
				resource.TestCheckTypeSetElemAttr("asc_role.test", "actions.*", "vm:write"),
			),
		},
		{ResourceName: "asc_role.test", ImportState: true, ImportStateId: "operator", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
	}})

	if updates.Load() == 0 {
		t.Error("the action change never reached the update route")
	}
}

func TestRoleRefreshDropsARoleDeletedOutsideTerraform(t *testing.T) {
	var updates atomic.Int64
	store := newRoleStore()
	environment := newTestEnvironment(t, store.handlers(t, &updates))

	config := environment.providerConfig() + `
resource "asc_role" "test" {
  name    = "operator"
  actions = ["vm:read"]
}`

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{Config: config, Check: resource.TestCheckResourceAttr("asc_role.test", "name", "operator")},
		{
			Config: config,
			PreConfig: func() {
				store.mu.Lock()
				defer store.mu.Unlock()
				delete(store.actions, "operator")
				delete(store.ids, "operator")
			},
			ExpectNonEmptyPlan: false,
			Check:              resource.TestCheckResourceAttr("asc_role.test", "name", "operator"),
		},
	}})
}

func TestRoleDataSourceReadsActions(t *testing.T) {
	var updates atomic.Int64
	store := newRoleStore()
	store.actions["operator"] = []string{"vm:write", "vm:read"}
	store.ids["operator"] = "00000000-0000-4000-8000-000000000010"
	environment := newTestEnvironment(t, store.handlers(t, &updates))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_role" "test" {
  name = "operator"
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_role.test", "actions.#", "2"),
			resource.TestCheckTypeSetElemAttr("data.asc_role.test", "actions.*", "vm:read"),
			resource.TestCheckResourceAttr("data.asc_role.test", "urn", "asc:asc:iam:test-tenant:role:operator"),
		),
	}}})
}

// groupStore mirrors UAM's split: the list route carries the id and the
// description, the item route carries the members and neither of the others.
type groupStore struct {
	mu      sync.Mutex
	present bool
}

func (s *groupStore) handlers(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()
	const id = "00000000-0000-4000-8000-000000000020"
	return map[string]http.HandlerFunc{
		"GET " + uamGroupList: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			groups := []map[string]any{}
			if s.present {
				groups = append(groups, map[string]any{"id": id, "name": "platform", "description": "platform team"})
			}
			writeBare(w, http.StatusOK, map[string]any{"groups": groups})
		},
		"POST " + uamGroupList: func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create group body: %v", err)
			}
			if len(body) != 1 || body["groupName"] != "platform" {
				t.Errorf("create group body = %#v, want only groupName", body)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.present = true
			writeBare(w, http.StatusCreated, map[string]any{"id": id, "name": "platform"})
		},
		"GET " + uamGroupItem: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if !s.present {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			writeBare(w, http.StatusOK, map[string]any{
				"id": id, "urn": "asc:asc:iam:test-tenant:group:platform", "name": "platform",
				"tenantId": "test-tenant", "description": "",
				// Served user-first then API-client-first, the order UAM
				// assembles them in rather than one the group controls.
				"members": []map[string]any{
					{"name": "zoe", "enabled": true, "type": "user"},
					{"name": "ci", "enabled": true, "type": "api_client"},
				},
			})
		},
		"DELETE " + uamGroupItem: func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.present = false
			w.WriteHeader(http.StatusOK)
		},
	}
}

func TestGroupProvisioning(t *testing.T) {
	store := &groupStore{}
	environment := newTestEnvironment(t, store.handlers(t))

	config := environment.providerConfig() + `
resource "asc_group" "test" {
  name = "platform"
}`

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{
		{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("asc_group.test", "name", "platform"),
				resource.TestCheckResourceAttr("asc_group.test", "id", "00000000-0000-4000-8000-000000000020"),
				// The description comes from the list route, which is the only
				// route that carries it.
				resource.TestCheckResourceAttr("asc_group.test", "description", "platform team"),
				// The members come from the item route, sorted by the provider.
				resource.TestCheckResourceAttr("asc_group.test", "members.#", "2"),
				resource.TestCheckResourceAttr("asc_group.test", "members.0.name", "ci"),
				resource.TestCheckResourceAttr("asc_group.test", "members.0.type", "api_client"),
				resource.TestCheckResourceAttr("asc_group.test", "members.1.name", "zoe"),
			),
		},
		{ResourceName: "asc_group.test", ImportState: true, ImportStateId: "platform", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
	}})
}

func TestGroupDataSourceReadsMembers(t *testing.T) {
	store := &groupStore{present: true}
	environment := newTestEnvironment(t, store.handlers(t))

	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: providerFactories, Steps: []resource.TestStep{{
		Config: environment.providerConfig() + `
data "asc_group" "test" {
  name = "platform"
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.asc_group.test", "id", "00000000-0000-4000-8000-000000000020"),
			resource.TestCheckResourceAttr("data.asc_group.test", "description", "platform team"),
			resource.TestCheckResourceAttr("data.asc_group.test", "members.#", "2"),
			resource.TestCheckResourceAttr("data.asc_group.test", "members.0.name", "ci"),
		),
	}}})
}
