package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/uam"
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
		writeBare(w, http.StatusOK, map[string]any{"id": testTenantUUID})
	}
}

func TestGetRoleTakesActionsFromTheItemRouteAndSortsThem(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "operator", "name": "operator", "composites": []string{"vm:write", "vm:read"}})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000002", "name": "auditor"}})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
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
				writeBare(w, http.StatusOK, []any{})
				return
			}
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "operator", "name": "operator", "composites": []string{"vm:read"}})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": uuid, "name": "operator"}})
		},
		"PUT " + roleWritePath: func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			writeBare(w, http.StatusOK, map[string]string{"message": "role updated successfully"})
		},
		"GET " + tenantItemPath: tenantRoute(nil),
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "operator", "name": "operator", "composites": []string{"vm:write"}})
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
			writeBare(w, http.StatusOK, []any{})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
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
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": uuid, "name": "platform", "description": "platform team"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			// The item route never carries the description and mints no id the
			// list agrees with, so only the members are taken from it.
			writeBare(w, http.StatusOK, map[string]any{"id": uuid, "name": "platform", "members": []map[string]any{
				{"name": "zoe", "enabled": true, "type": "user"},
				{"name": "ci", "enabled": true, "type": "api_client"},
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
			writeBare(w, http.StatusOK, map[string]any{"groups": []any{}})
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
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "00000000-0000-4000-8000-000000000003", "name": "platform"}}})
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
			writeBare(w, http.StatusCreated, map[string]any{"id": "00000000-0000-4000-8000-000000000003", "name": "platform"})
		},
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			if !created.Load() {
				writeBare(w, http.StatusOK, map[string]any{"groups": []any{}})
				return
			}
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "00000000-0000-4000-8000-000000000003", "name": "platform"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "00000000-0000-4000-8000-000000000003", "name": "platform"})
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
			writeBare(w, http.StatusOK, map[string]any{"groups": []any{}})
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
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "id", "name": "platform"}}})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "id", "name": "platform"})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
		},
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			read.Store(true)
			writeBare(w, http.StatusOK, map[string]any{"id": "operator", "name": "operator", "composites": []string{"vm:read"}})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
		},
		"GET " + roleReadPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"id": "operator", "name": "operator", "composites": []string{"vm:read"}})
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
			writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
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

// recordBody keeps the raw request body so the exact wire JSON can be compared.
func recordBody(t *testing.T, r *http.Request, into *atomic.Value) {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
	}
	into.Store(string(raw))
}

// assertJSON compares a wire body with the expected JSON by content, including which keys
// are present and whether a value is null or empty.
func assertJSON(t *testing.T, label string, got any, want string) {
	t.Helper()
	raw, _ := got.(string)
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(raw), &gotValue); err != nil {
		t.Fatalf("%s body %q: %v", label, raw, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("%s want %q: %v", label, want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("%s body = %s, want %s", label, raw, want)
	}
}

func roleReadBackRoutes(routes map[string]http.HandlerFunc) map[string]http.HandlerFunc {
	routes["GET "+roleListPath] = func(w http.ResponseWriter, r *http.Request) {
		writeBare(w, http.StatusOK, []map[string]any{{"id": "00000000-0000-4000-8000-000000000001", "name": "operator"}})
	}
	routes["GET "+tenantItemPath] = tenantRoute(nil)
	routes["GET "+roleReadPath] = func(w http.ResponseWriter, _ *http.Request) {
		writeBare(w, http.StatusOK, map[string]any{"name": "operator", "composites": []string{}})
	}
	return routes
}

// The create body is exactly {"name","actions"}; nil actions stay an explicit null and an
// empty list stays [] as they did with the handwritten request struct.
func TestCreateRoleSendsTheExactBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		actions []string
		want    string
	}{
		{[]string{"vm:read", "vm:write"}, `{"name":"operator","actions":["vm:read","vm:write"]}`},
		{[]string{}, `{"name":"operator","actions":[]}`},
		{nil, `{"name":"operator","actions":null}`},
	} {
		var body, contentType atomic.Value
		runtime := uamRuntime(t, roleReadBackRoutes(map[string]http.HandlerFunc{
			"POST " + roleListPath: func(w http.ResponseWriter, r *http.Request) {
				contentType.Store(r.Header.Get("Content-Type"))
				recordBody(t, r, &body)
				writeBare(w, http.StatusCreated, map[string]string{"message": "role created successfully"})
			},
		}))
		if _, err := runtime.CreateRole(context.Background(), "operator", tc.actions); err != nil {
			t.Fatalf("CreateRole(%v) error = %v", tc.actions, err)
		}
		assertJSON(t, "create role", body.Load(), tc.want)
		if contentType.Load() != "application/json" {
			t.Errorf("Content-Type = %v", contentType.Load())
		}
	}
}

// The update body is exactly {"id","name","actions"}; the generated group_id is never sent.
func TestUpdateRoleSendsTheExactBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		actions []string
		want    string
	}{
		{[]string{"vm:write"}, `{"id":"00000000-0000-4000-8000-000000000001","name":"operator","actions":["vm:write"]}`},
		{[]string{}, `{"id":"00000000-0000-4000-8000-000000000001","name":"operator","actions":[]}`},
		{nil, `{"id":"00000000-0000-4000-8000-000000000001","name":"operator","actions":null}`},
	} {
		var body atomic.Value
		runtime := uamRuntime(t, roleReadBackRoutes(map[string]http.HandlerFunc{
			"PUT " + roleWritePath: func(w http.ResponseWriter, r *http.Request) {
				recordBody(t, r, &body)
				writeBare(w, http.StatusOK, map[string]string{"message": "role updated successfully"})
			},
		}))
		if _, err := runtime.UpdateRole(context.Background(), "operator", tc.actions); err != nil {
			t.Fatalf("UpdateRole(%v) error = %v", tc.actions, err)
		}
		assertJSON(t, "update role", body.Load(), tc.want)
	}
}

func TestCreateGroupSendsTheExactBody(t *testing.T) {
	t.Parallel()
	var body atomic.Value
	var created atomic.Bool
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"POST " + groupListP: func(w http.ResponseWriter, r *http.Request) {
			recordBody(t, r, &body)
			created.Store(true)
			writeBare(w, http.StatusCreated, map[string]any{"id": "id", "name": "platform"})
		},
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			groups := []uam.GroupResponse{}
			if created.Load() {
				id, name := "id", "platform"
				groups = append(groups, uam.GroupResponse{ID: &id, Name: &name})
			}
			writeBare(w, http.StatusOK, uam.GroupListResponse{Groups: &groups})
		},
		"GET " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"name": "platform"})
		},
	})

	if _, err := runtime.CreateGroup(context.Background(), "platform"); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	assertJSON(t, "create group", body.Load(), `{"groupName":"platform"}`)
}

// The role list is read without query parameters, so UAM applies its own defaults.
func TestRoleListSendsNoQueryString(t *testing.T) {
	t.Parallel()
	var query atomic.Value
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, r *http.Request) {
			query.Store(r.URL.RawQuery)
			writeBare(w, http.StatusOK, []any{})
		},
	})

	if err := runtime.DeleteRole(context.Background(), "operator"); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
	if query.Load() != "" {
		t.Errorf("role list query = %q, want none", query.Load())
	}
}

// Every UAM route escapes its path parameters exactly once, as a single segment, and every
// request carries the bearer token and the tenant header.
func TestUAMRoutesEscapePathSegmentsAndAuthorizeEveryRequest(t *testing.T) {
	t.Parallel()
	const (
		namespace = "team a/b"
		ns        = "team%20a%2Fb"
		uuid      = "uuid/1"
		role      = "ops/admin x"
		roleSeg   = "ops%2Fadmin%20x"
		group     = "plat/form%"
		groupSeg  = "plat%2Fform%25"
	)
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/realm/protocol/openid-connect/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		request := r.Method + " " + r.URL.EscapedPath()
		mu.Lock()
		seen = append(seen, request)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Tenant") != namespace {
			t.Errorf("%s: Authorization = %q, X-Tenant = %q", request, r.Header.Get("Authorization"), r.Header.Get("X-Tenant"))
		}
		switch request {
		case "GET /api/uam/v1/tenants/" + ns:
			writeBare(w, http.StatusOK, map[string]any{"id": uuid})
		case "GET /api/uam/v1/tenants/" + ns + "/roles":
			writeBare(w, http.StatusOK, []map[string]any{{"id": "db-id", "name": role}})
		case "GET /api/uam/v1/tenants/uuid%2F1/roles/" + roleSeg:
			writeBare(w, http.StatusOK, map[string]any{"name": role, "composites": []string{"b", "a"}})
		case "POST /api/uam/v1/tenants/" + ns + "/roles",
			"PUT /api/uam/v1/tenants/" + ns + "/roles/" + roleSeg,
			"DELETE /api/uam/v1/tenants/" + ns + "/roles/" + roleSeg,
			"POST /api/uam/v1/tenants/groups",
			"DELETE /api/uam/v1/tenants/groups/" + groupSeg:
			writeBare(w, http.StatusOK, map[string]any{"message": "ok"})
		case "GET /api/uam/v1/tenants/groups":
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "gid", "name": group}}})
		case "GET /api/uam/v1/tenants/groups/" + groupSeg:
			writeBare(w, http.StatusOK, map[string]any{"name": group})
		default:
			t.Errorf("unexpected request %s", request)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	config, err := NewConfig(server.URL, namespace, server.URL, "realm", "client", "secret", 0)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	runtime := NewRuntime(config)
	ctx := context.Background()

	if _, err := runtime.CreateRole(ctx, role, []string{"a"}); err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	updated, err := runtime.UpdateRole(ctx, role, []string{"a", "b"})
	if err != nil {
		t.Fatalf("UpdateRole() error = %v", err)
	}
	if strings.Join(updated.Actions, ",") != "a,b" {
		t.Errorf("role.Actions = %v, want them sorted", updated.Actions)
	}
	if err := runtime.DeleteRole(ctx, role); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
	if _, err := runtime.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if err := runtime.DeleteGroup(ctx, group); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}

	for _, want := range []string{
		"GET /api/uam/v1/tenants/" + ns,
		"GET /api/uam/v1/tenants/uuid%2F1/roles/" + roleSeg,
		"POST /api/uam/v1/tenants/" + ns + "/roles",
		"PUT /api/uam/v1/tenants/" + ns + "/roles/" + roleSeg,
		"DELETE /api/uam/v1/tenants/" + ns + "/roles/" + roleSeg,
		"POST /api/uam/v1/tenants/groups",
		"GET /api/uam/v1/tenants/groups/" + groupSeg,
		"DELETE /api/uam/v1/tenants/groups/" + groupSeg,
	} {
		if !slices.Contains(seen, want) {
			t.Errorf("missing request %s; saw %v", want, seen)
		}
	}
}

func TestDeleteGroupAddressesTheGroupByName(t *testing.T) {
	t.Parallel()
	var deleted atomic.Bool
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "id", "name": "platform"}}})
		},
		"DELETE " + groupItemP: func(w http.ResponseWriter, _ *http.Request) {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})

	if err := runtime.DeleteGroup(context.Background(), "platform"); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	if !deleted.Load() {
		t.Error("DeleteGroup() did not reach the name-addressed route")
	}
}

// A 502 from a list read is a failure, never proof of absence, so delete must not no-op.
func TestDeleteDoesNotTreatA502ListAsAbsence(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"GET " + groupListP:   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	})

	err := runtime.DeleteRole(context.Background(), "operator")
	if err == nil || errors.Is(err, ErrResourceNotFound) || err.Error() != "list roles: uam returned HTTP 502" {
		t.Errorf("DeleteRole() error = %v, want the list failure", err)
	}
	err = runtime.DeleteGroup(context.Background(), "platform")
	if err == nil || errors.Is(err, ErrResourceNotFound) || err.Error() != "list groups: uam returned HTTP 502" {
		t.Errorf("DeleteGroup() error = %v, want the list failure", err)
	}
}

func TestUAMWriteErrorsKeepTheirStatusAndWording(t *testing.T) {
	t.Parallel()
	runtime := uamRuntime(t, map[string]http.HandlerFunc{
		"GET " + roleListPath: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, []map[string]any{{"id": "id", "name": "operator"}})
		},
		"PUT " + roleWritePath: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) },
		"POST " + roleListPath: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"DELETE " + groupItemP: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"GET " + groupListP: func(w http.ResponseWriter, _ *http.Request) {
			writeBare(w, http.StatusOK, map[string]any{"groups": []map[string]any{{"id": "id", "name": "platform"}}})
		},
	})
	ctx := context.Background()

	if _, err := runtime.UpdateRole(ctx, "operator", nil); !errors.Is(err, ErrResourceConflict) || !strings.HasPrefix(err.Error(), `update role "operator": `) {
		t.Errorf("UpdateRole() error = %v, want a wrapped conflict", err)
	}
	if _, err := runtime.CreateRole(ctx, "operator", nil); err == nil || err.Error() != `create role "operator": uam returned HTTP 502` {
		t.Errorf("CreateRole() error = %v", err)
	}
	if err := runtime.DeleteGroup(ctx, "platform"); !errors.Is(err, ErrResourceNotFound) || !strings.HasPrefix(err.Error(), `delete group "platform": `) {
		t.Errorf("DeleteGroup() error = %v, want a wrapped not-found", err)
	}
}
