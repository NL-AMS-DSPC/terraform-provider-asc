package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testTenantUUID = "00000000-0000-4000-8000-0000000000aa"
	tenantItemPath = "/api/uam/v1/tenants/tenant"
	roleListPath   = "/api/uam/v1/tenants/tenant/roles"
	// Role writes use the namespace; the item read requires the tenant UUID.
	roleWritePath = "/api/uam/v1/tenants/tenant/roles/operator"
	roleReadPath  = "/api/uam/v1/tenants/" + testTenantUUID + "/roles/operator"
	groupListP    = "/api/uam/v1/tenants/groups"
	groupItemP    = "/api/uam/v1/tenants/groups/platform"
)

// uamRuntime fails on unexpected paths instead of returning a misleading decode error.
func uamRuntime(t *testing.T, routes map[string]http.HandlerFunc) *Runtime {
	t.Helper()
	return newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		handler, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	})
}

// writeBare encodes a payload the way UAM does: as itself, with no "data"
// envelope around it.
func writeBare(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// tenantRoute counts namespace-to-UUID lookups to verify caching.
func tenantRoute(calls *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		writeBare(w, http.StatusOK, tenantResponse{ID: testTenantUUID})
	}
}

func TestGetRoleTakesActionsFromTheItemRouteAndSortsThem(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, roleResponse{ID: "operator", Name: "operator", Composites: []string{"vm:write", "vm:read"}})
		},
	})

	role, err := runtime.GetRole(context.Background(), "operator")
	if err != nil {
		t.Fatalf("GetRole() error = %v", err)
	}
	if role.Name != "operator" {
		t.Errorf("role.Name = %q", role.Name)
	}
	if strings.Join(role.Actions, ",") != "vm:read,vm:write" {
		t.Errorf("role.Actions = %v, want them sorted", role.Actions)
	}
}

func TestGetRoleReportsNotFoundFromTheListWithoutReadingTheItemRoute(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000002", Name: "auditor"}})
		},
	})

	if _, err := runtime.GetRole(context.Background(), "operator"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetRole() error = %v, want ErrResourceNotFound", err)
	}
}

func TestGetRoleDoesNotTreat502AsMissing(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
	})

	_, err := runtime.GetRole(context.Background(), "operator")
	if err == nil || errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetRole() error = %v, want a reported failure", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("GetRole() error = %v, want the status reported", err)
	}
}

func TestCreateRoleSendsActionsAndReadsBack(t *testing.T) {
	t.Parallel()
	var created atomic.Bool
	var body map[string]any
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"POST " + roleListPath: func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			created.Store(true)
			writeBare(w, http.StatusCreated, map[string]string{"message": "role created successfully"})
		},
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			if !created.Load() {
				writeBare(w, http.StatusOK, []roleResponse{})
				return
			}
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, roleResponse{ID: "operator", Name: "operator", Composites: []string{"vm:read"}})
		},
	})

	role, err := runtime.CreateRole(context.Background(), "operator", []string{"vm:read"})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if body["name"] != "operator" {
		t.Errorf("create body name = %v", body["name"])
	}
	actions, ok := body["actions"].([]any)
	if !ok || len(actions) != 1 || actions[0] != "vm:read" {
		t.Errorf("create body actions = %#v", body["actions"])
	}
	if len(role.Actions) != 1 || role.Actions[0] != "vm:read" {
		t.Errorf("role.Actions = %v, want the read-back state", role.Actions)
	}
}

func TestUpdateRoleSendsTheUUIDTheListRecords(t *testing.T) {
	t.Parallel()
	const uuid = "00000000-0000-4000-8000-000000000001"
	var body map[string]any
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: uuid, Name: "operator"}})
		},
		"PUT " + roleWritePath: func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			writeBare(w, http.StatusOK, map[string]string{"message": "role updated successfully"})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, roleResponse{ID: "operator", Name: "operator", Composites: []string{"vm:write"}})
		},
	})

	if _, err := runtime.UpdateRole(context.Background(), "operator", []string{"vm:write"}); err != nil {
		t.Fatalf("UpdateRole() error = %v", err)
	}
	if body["id"] != uuid {
		t.Errorf("update body id = %v, want the listed uuid %q", body["id"], uuid)
	}
	if body["name"] != "operator" {
		t.Errorf("update body name = %v", body["name"])
	}
}

func TestDeleteRoleSkipsTheRequestWhenTheRoleIsAbsent(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{})
		},
	})

	if err := runtime.DeleteRole(context.Background(), "operator"); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
}

func TestDeleteRoleAddressesTheRoleByName(t *testing.T) {
	t.Parallel()
	var deleted atomic.Bool
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"DELETE " + roleWritePath: func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})

	if err := runtime.DeleteRole(context.Background(), "operator"); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
	if !deleted.Load() {
		t.Error("DeleteRole() did not reach the name-addressed route")
	}
}

func TestGetGroupTakesIdFromTheListAndMembersFromTheItemRoute(t *testing.T) {
	t.Parallel()
	const uuid = "00000000-0000-4000-8000-000000000003"
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{{ID: uuid, Name: "platform", Description: "platform team"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			// The item route never carries the description and mints no id the
			// list agrees with, so only the members are taken from it.
			writeBare(w, http.StatusOK, groupResponse{ID: uuid, Name: "platform", Members: []groupMemberResponse{
				{Name: "zoe", Enabled: true, Type: "user"},
				{Name: "ci", Enabled: true, Type: "api_client"},
			}})
		},
	})

	group, err := runtime.GetGroup(context.Background(), "platform")
	if err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if group.ID != uuid || group.Description != "platform team" {
		t.Errorf("group = %+v, want the listed id and description", group)
	}
	if len(group.Members) != 2 || group.Members[0].Name != "ci" || group.Members[1].Name != "zoe" {
		t.Errorf("group.Members = %+v, want them sorted by name", group.Members)
	}
}

func TestGetGroupReportsNotFoundFromTheList(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{}})
		},
	})

	if _, err := runtime.GetGroup(context.Background(), "platform"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("GetGroup() error = %v, want ErrResourceNotFound", err)
	}
}

func TestCreateGroupNamesADuplicateBehindThe502(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"POST " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		},
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{{ID: "00000000-0000-4000-8000-000000000003", Name: "platform"}}})
		},
	})

	_, err := runtime.CreateGroup(context.Background(), "platform")
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("CreateGroup() error = %v, want the duplicate name named", err)
	}
}

func TestCreateGroupSendsOnlyTheNameAndReadsBack(t *testing.T) {
	t.Parallel()
	var created atomic.Bool
	var body map[string]any
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"POST " + groupListP: func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			created.Store(true)
			writeBare(w, http.StatusCreated, groupResponse{ID: "00000000-0000-4000-8000-000000000003", Name: "platform"})
		},
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			if !created.Load() {
				writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{}})
				return
			}
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{{ID: "00000000-0000-4000-8000-000000000003", Name: "platform"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupResponse{ID: "00000000-0000-4000-8000-000000000003", Name: "platform"})
		},
	})

	group, err := runtime.CreateGroup(context.Background(), "platform")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if len(body) != 1 || body["groupName"] != "platform" {
		t.Errorf("create body = %#v, want only groupName", body)
	}
	if group.ID != "00000000-0000-4000-8000-000000000003" {
		t.Errorf("group.ID = %q", group.ID)
	}
}

func TestDeleteGroupSkipsTheRequestWhenTheGroupIsAbsent(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{}})
		},
	})

	if err := runtime.DeleteGroup(context.Background(), "platform"); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
}

func TestUAMRequestsCarryTheTenantHeader(t *testing.T) {
	t.Parallel()
	var tenant, authorization atomic.Value
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + groupListP: func(w http.ResponseWriter, r *http.Request) {
			tenant.Store(r.Header.Get("X-Tenant"))
			authorization.Store(r.Header.Get("Authorization"))
			writeBare(w, http.StatusOK, groupListResponse{Groups: []groupResponse{{ID: "id", Name: "platform"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, groupResponse{ID: "id", Name: "platform"})
		},
	})

	if _, err := runtime.GetGroup(context.Background(), "platform"); err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if tenant.Load() != "tenant" {
		t.Errorf("X-Tenant = %v", tenant.Load())
	}
	if authorization.Load() != "Bearer token" {
		t.Errorf("Authorization = %v", authorization.Load())
	}
}

func TestGetRoleReadsTheItemRouteUnderTheTenantUUID(t *testing.T) {
	t.Parallel()
	var read atomic.Bool
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			read.Store(true)
			writeBare(w, http.StatusOK, roleResponse{ID: "operator", Name: "operator", Composites: []string{"vm:read"}})
		},
	})

	role, err := runtime.GetRole(context.Background(), "operator")
	if err != nil {
		t.Fatalf("GetRole() error = %v", err)
	}
	if !read.Load() {
		t.Error("GetRole() did not reach the uuid-addressed item route")
	}
	if len(role.Actions) != 1 || role.Actions[0] != "vm:read" {
		t.Errorf("role.Actions = %v, want the actions the item route serves", role.Actions)
	}
}

func TestTenantLookupIsResolvedOnceAndReused(t *testing.T) {
	t.Parallel()
	var lookups atomic.Int64
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + tenantItemPath: tenantRoute(&lookups),
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, roleResponse{ID: "operator", Name: "operator", Composites: []string{"vm:read"}})
		},
	})

	for range 3 {
		if _, err := runtime.GetRole(context.Background(), "operator"); err != nil {
			t.Fatalf("GetRole() error = %v", err)
		}
	}
	if lookups.Load() != 1 {
		t.Errorf("tenant lookups = %d, want the resolution cached after the first", lookups.Load())
	}
}

func TestGetRoleReportsARefusedTenantLookup(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + tenantItemPath: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []roleResponse{{ID: "00000000-0000-4000-8000-000000000001", Name: "operator"}})
		},
	})

	_, err := runtime.GetRole(context.Background(), "operator")
	if err == nil {
		t.Fatal("GetRole() error = nil, want the refused tenant lookup reported")
	}
	if !strings.Contains(err.Error(), "resolve tenant") || !strings.Contains(err.Error(), "403") {
		t.Errorf("GetRole() error = %v, want it to name the tenant lookup and its status", err)
	}
}
