package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	tenantsPath      = "/v1/tenants"
	tenantGroupsPath = "/v1/tenants/groups"
)

// tenantRolesPath addresses the configured tenant's roles.
//
// UAM resolves the path segment through getTenantIDByClientID on every write,
// so the segment is the tenant's client id: the same value the provider already
// sends as X-Tenant. Reads forward the segment to the authorization service
// unchanged, so the two must agree or reads and writes address different
// tenants.
func (c *Runtime) tenantRolesPath() string {
	return tenantsPath + "/" + escapedPathSegment(c.config.Namespace) + "/roles"
}

// roleReadPath addresses a role on the single-item route, which is the only
// one that serves its actions.
//
// That route is the one place where the namespace is the wrong segment. Every
// other role route resolves its {tenantId} through getTenantIDByClientID and
// calls the authorization service with the tenant's uuid, but GetTenantRole
// forwards the segment unchanged as the tenant header, and the authorization
// service keys a role as "<tenant>_<name>". A role created under the namespace
// is therefore stored against the tenant's uuid, and reading it back by the
// namespace looks up a key that was never written and answers 502. The item
// read addresses the uuid; the writes keep the namespace, which is the only
// value they accept.
//
// This mirrors a UAM defect rather than its intended contract. If UAM ever
// translates the segment in GetTenantRole, this must go back to the namespace
// on the same day, because the fixed service would then reject the uuid.
func (c *Runtime) roleReadPath(ctx context.Context, name string) (string, error) {
	tenant, err := c.tenantUUID(ctx)
	if err != nil {
		return "", err
	}
	return tenantsPath + "/" + escapedPathSegment(tenant) + "/roles/" + escapedPathSegment(name), nil
}

// tenantUUID returns the id UAM records for the configured namespace, which is
// the tenant's Keycloak client uuid.
//
// It is resolved once and cached for the life of the provider: a tenant's uuid
// cannot change under its client id, and every role refresh would otherwise
// pay for the lookup again. Reading it needs the uam:GetTenant action on the
// provider's credentials.
func (c *Runtime) tenantUUID(ctx context.Context) (string, error) {
	c.tenantMu.Lock()
	defer c.tenantMu.Unlock()
	if c.tenantID != "" {
		return c.tenantID, nil
	}

	var tenant tenantResponse
	if err := c.uamGet(ctx, tenantsPath+"/"+escapedPathSegment(c.config.Namespace), &tenant); err != nil {
		return "", fmt.Errorf("resolve tenant %q: %w", c.config.Namespace, err)
	}
	if tenant.ID == "" {
		return "", fmt.Errorf("resolve tenant %q: UAM reported no tenant id", c.config.Namespace)
	}

	c.tenantID = tenant.ID
	return c.tenantID, nil
}

// Role is a UAM tenant role as the provider models it.
//
// UAM's own "id" is deliberately absent. It is not one identity: the
// single-item read sets it to the role's name while the list route sets it to a
// database uuid, so a role is keyed by name and the uuid is resolved only for
// the one request that needs it.
type Role struct {
	URN     string
	Name    string
	Actions []string
}

// Group is a UAM tenant group. Its id is stable across reads, but it is not the
// addressing key: every route resolves a group by name.
type Group struct {
	ID          string
	URN         string
	Name        string
	Description string
	Members     []GroupMember
}

// GroupMember is a user or API client that belongs to a group. Membership is
// read-only because UAM exposes no route that adds or removes a member.
type GroupMember struct {
	Name    string
	Enabled bool
	Type    string
}

// tenantResponse is UAM's TenantResponse. Its "id" is the tenant's Keycloak
// client uuid, which is the value the role item route has to be addressed by.
type tenantResponse struct {
	ID string `json:"id"`
}

// roleResponse is UAM's RoleResponse. A role's actions are sent on create as
// "actions" and served back on the single-item read as "composites"; the list
// route serves neither.
type roleResponse struct {
	ID          string   `json:"id"`
	URN         string   `json:"urn"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Composites  []string `json:"composites"`
}

type createRoleRequest struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// updateRoleRequest carries the role's database uuid because UAM validates it
// as required and upserts on it.
type updateRoleRequest struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type groupResponse struct {
	ID          string                `json:"id"`
	URN         string                `json:"urn"`
	Name        string                `json:"name"`
	TenantID    string                `json:"tenantId"`
	Description string                `json:"description"`
	Members     []groupMemberResponse `json:"members"`
}

type groupMemberResponse struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
}

// groupListResponse is the only UAM collection that is not a bare array.
type groupListResponse struct {
	Groups []groupResponse `json:"groups"`
}

type createGroupRequest struct {
	GroupName string `json:"groupName"`
}

// GetRole reads one role by name.
//
// Existence is decided from the list route, never from the single-item read. A
// role that no longer exists answers the single-item read with 502, because UAM
// turns every non-2xx from the authorization service into an untyped error and
// maps only its own not-found sentinels to 404. The single-item read is then
// still needed, because it is the only route that serves the role's actions,
// and it is addressed by the tenant's uuid rather than the namespace. See
// roleReadPath.
func (c *Runtime) GetRole(ctx context.Context, name string) (Role, error) {
	if _, err := c.roleID(ctx, name); err != nil {
		return Role{}, err
	}

	path, err := c.roleReadPath(ctx, name)
	if err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	var detail roleResponse
	if err := c.uamGet(ctx, path, &detail); err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	actions := slices.Clone(detail.Composites)
	slices.Sort(actions)
	return Role{URN: detail.URN, Name: name, Actions: actions}, nil
}

// CreateRole creates a role and reads it back. The create answers with a
// message and no representation, so every attribute in state comes from the
// read.
func (c *Runtime) CreateRole(ctx context.Context, name string, actions []string) (Role, error) {
	if err := c.uamPost(ctx, c.tenantRolesPath(), createRoleRequest{Name: name, Actions: actions}, nil); err != nil {
		return Role{}, fmt.Errorf("create role %q: %w", name, err)
	}
	return c.GetRole(ctx, name)
}

// UpdateRole replaces a role's actions.
//
// The body carries the role's database uuid, which only the list route serves,
// and UAM's upsert conflicts on that uuid rather than on the name: a request
// carrying an invented id inserts a second row for the same role. The uuid is
// therefore resolved from the list on every update instead of being kept in
// state, where it would go stale against a role recreated outside Terraform.
//
// The name cannot change here. UAM addresses the downstream update by the name
// in the body, so a renamed role would be written nowhere; the resource
// requires replacement for a new name.
func (c *Runtime) UpdateRole(ctx context.Context, name string, actions []string) (Role, error) {
	id, err := c.roleID(ctx, name)
	if err != nil {
		return Role{}, err
	}

	request := updateRoleRequest{ID: id, Name: name, Actions: actions}
	if err := c.uamPut(ctx, c.tenantRolesPath()+"/"+escapedPathSegment(name), request); err != nil {
		return Role{}, fmt.Errorf("update role %q: %w", name, err)
	}
	return c.GetRole(ctx, name)
}

// DeleteRole deletes a role by name.
//
// The route spells its segment {roleID}, but the service resolves it with
// repo.GetByName and forwards the same segment downstream, so the segment is
// the name. The list is consulted first so that deleting an already absent role
// succeeds instead of surfacing UAM's untyped 502.
func (c *Runtime) DeleteRole(ctx context.Context, name string) error {
	switch _, err := c.roleID(ctx, name); {
	case errors.Is(err, ErrResourceNotFound):
		return nil
	case err != nil:
		return err
	}

	if err := c.uamDelete(ctx, c.tenantRolesPath()+"/"+escapedPathSegment(name)); err != nil {
		return fmt.Errorf("delete role %q: %w", name, err)
	}
	return nil
}

// roleID returns the database uuid the list route records for a role name, and
// ErrResourceNotFound when the tenant has no such role.
func (c *Runtime) roleID(ctx context.Context, name string) (string, error) {
	var roles []roleResponse
	if err := c.uamGet(ctx, c.tenantRolesPath(), &roles); err != nil {
		return "", fmt.Errorf("list roles: %w", err)
	}
	for _, role := range roles {
		if role.Name == name {
			return role.ID, nil
		}
	}
	return "", ErrResourceNotFound
}

// GetGroup reads one group by name.
//
// As with roles, the list route decides existence and carries the group's id
// and description, while the single-item read is the only source of its
// members.
func (c *Runtime) GetGroup(ctx context.Context, name string) (Group, error) {
	summary, err := c.groupSummary(ctx, name)
	if err != nil {
		return Group{}, err
	}

	var detail groupResponse
	if err := c.uamGet(ctx, tenantGroupsPath+"/"+escapedPathSegment(name), &detail); err != nil {
		return Group{}, fmt.Errorf("read group %q: %w", name, err)
	}

	// groupMemberResponse and GroupMember carry the same fields; the two types
	// exist so the wire tags stay out of the exported model.
	members := make([]GroupMember, 0, len(detail.Members))
	for _, member := range detail.Members {
		members = append(members, GroupMember(member))
	}
	// The members are assembled from a user lookup followed by an API client
	// lookup, so their order tracks the upstream directories rather than the
	// group, and an unsorted list would surface as a diff no configuration can
	// settle.
	slices.SortFunc(members, func(a, b GroupMember) int { return strings.Compare(a.Name, b.Name) })

	return Group{ID: summary.ID, URN: detail.URN, Name: name, Description: summary.Description, Members: members}, nil
}

// CreateGroup creates a group and reads it back.
//
// UAM reports a duplicate group name as a plain error rather than its
// already-exists sentinel, so the conflict arrives as 502 with no way to tell
// it from a genuine upstream failure. The list is consulted after a failure to
// name the cause rather than repeat the status.
func (c *Runtime) CreateGroup(ctx context.Context, name string) (Group, error) {
	if err := c.uamPost(ctx, tenantGroupsPath, createGroupRequest{GroupName: name}, nil); err != nil {
		if _, lookupErr := c.groupSummary(ctx, name); lookupErr == nil {
			return Group{}, fmt.Errorf("create group %q: the name is already in use for this tenant: %w", name, err)
		}
		return Group{}, fmt.Errorf("create group %q: %w", name, err)
	}
	return c.GetGroup(ctx, name)
}

// DeleteGroup deletes a group by name. The route spells its segment {groupID},
// but the service resolves it with GetGroupByNameAndTenant, so the segment is
// the name.
func (c *Runtime) DeleteGroup(ctx context.Context, name string) error {
	switch _, err := c.groupSummary(ctx, name); {
	case errors.Is(err, ErrResourceNotFound):
		return nil
	case err != nil:
		return err
	}

	if err := c.uamDelete(ctx, tenantGroupsPath+"/"+escapedPathSegment(name)); err != nil {
		return fmt.Errorf("delete group %q: %w", name, err)
	}
	return nil
}

// groupSummary returns the list route's record of a group, which is the only
// reliable proof that it exists.
func (c *Runtime) groupSummary(ctx context.Context, name string) (groupResponse, error) {
	var list groupListResponse
	if err := c.uamGet(ctx, tenantGroupsPath, &list); err != nil {
		return groupResponse{}, fmt.Errorf("list groups: %w", err)
	}
	for _, group := range list.Groups {
		if group.Name == name {
			return group, nil
		}
	}
	return groupResponse{}, ErrResourceNotFound
}
