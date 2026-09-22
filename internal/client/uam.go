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

// Role writes and collection reads resolve the tenant client ID, which is the configured
// namespace.
func (c *Runtime) tenantRolesPath() string {
	return tenantsPath + "/" + escapedPathSegment(c.config.Namespace) + "/roles"
}

// roleReadPath uses the tenant UUID because UAM GetTenantRole forwards that segment
// unchanged to authorization. Other role routes translate the namespace to a UUID. Revisit
// this workaround if GetTenantRole starts translating it too.
func (c *Runtime) roleReadPath(ctx context.Context, name string) (string, error) {
	tenant, err := c.tenantUUID(ctx)
	if err != nil {
		return "", err
	}
	return tenantsPath + "/" + escapedPathSegment(tenant) + "/roles/" + escapedPathSegment(name), nil
}

// tenantUUID caches the namespace lookup for this provider instance. The credentials need
// uam:GetTenant.
func (c *Runtime) tenantUUID(ctx context.Context) (string, error) {
	c.tenantMu.Lock()
	defer c.tenantMu.Unlock()
	if c.tenantID != "" {
		return c.tenantID, nil
	}

	var tenant tenantResponse
	if err := c.getForSvc(ctx, svcUAM, tenantsPath+"/"+escapedPathSegment(c.config.Namespace), &tenant); err != nil {
		return "", fmt.Errorf("resolve tenant %q: %w", c.config.Namespace, err)
	}
	if tenant.ID == "" {
		return "", fmt.Errorf("resolve tenant %q: UAM reported no tenant id", c.config.Namespace)
	}

	c.tenantID = tenant.ID
	return c.tenantID, nil
}

// Role is keyed by name. UAM reports incompatible IDs on its list and item routes, so no
// ID is exposed here.
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

// tenantResponse carries the Keycloak client UUID required by the role item route.
type tenantResponse struct {
	ID string `json:"id"`
}

// Role writes use actions; item reads use composites. List responses omit both.
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

// GetRole checks existence through the list, then reads actions from the item route.
// Missing item reads return an ambiguous 502.
func (c *Runtime) GetRole(ctx context.Context, name string) (Role, error) {
	if _, err := c.roleID(ctx, name); err != nil {
		return Role{}, err
	}

	path, err := c.roleReadPath(ctx, name)
	if err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	var detail roleResponse
	if err := c.getForSvc(ctx, svcUAM, path, &detail); err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	actions := slices.Clone(detail.Composites)
	slices.Sort(actions)
	return Role{URN: detail.URN, Name: name, Actions: actions}, nil
}

// CreateRole reads back the role because creation returns only an acknowledgement.
func (c *Runtime) CreateRole(ctx context.Context, name string, actions []string) (Role, error) {
	if err := c.postForSvc(ctx, svcUAM, c.tenantRolesPath(), createRoleRequest{Name: name, Actions: actions}); err != nil {
		return Role{}, fmt.Errorf("create role %q: %w", name, err)
	}
	return c.GetRole(ctx, name)
}

// UpdateRole resolves the current database UUID before replacing actions. UAM upserts by
// UUID; inventing one would insert a duplicate. Renaming requires replacement.
func (c *Runtime) UpdateRole(ctx context.Context, name string, actions []string) (Role, error) {
	id, err := c.roleID(ctx, name)
	if err != nil {
		return Role{}, err
	}

	request := updateRoleRequest{ID: id, Name: name, Actions: actions}
	if err := c.putForSvc(ctx, svcUAM, c.tenantRolesPath()+"/"+escapedPathSegment(name), request); err != nil {
		return Role{}, fmt.Errorf("update role %q: %w", name, err)
	}
	return c.GetRole(ctx, name)
}

// DeleteRole addresses the role by name despite the route parameter being called roleID. A
// list lookup avoids deleting an already absent role.
func (c *Runtime) DeleteRole(ctx context.Context, name string) error {
	switch _, err := c.roleID(ctx, name); {
	case errors.Is(err, ErrResourceNotFound):
		return nil
	case err != nil:
		return err
	}

	if err := c.deleteForSvc(ctx, svcUAM, c.tenantRolesPath()+"/"+escapedPathSegment(name)); err != nil {
		return fmt.Errorf("delete role %q: %w", name, err)
	}
	return nil
}

// roleID returns the database uuid the list route records for a role name, and
// ErrResourceNotFound when the tenant has no such role.
func (c *Runtime) roleID(ctx context.Context, name string) (string, error) {
	var roles []roleResponse
	if err := c.getForSvc(ctx, svcUAM, c.tenantRolesPath(), &roles); err != nil {
		return "", fmt.Errorf("list roles: %w", err)
	}
	for _, role := range roles {
		if role.Name == name {
			return role.ID, nil
		}
	}
	return "", ErrResourceNotFound
}

// GetGroup takes identity and description from the list, and members from the item read.
func (c *Runtime) GetGroup(ctx context.Context, name string) (Group, error) {
	summary, err := c.groupSummary(ctx, name)
	if err != nil {
		return Group{}, err
	}

	var detail groupResponse
	if err := c.getForSvc(ctx, svcUAM, tenantGroupsPath+"/"+escapedPathSegment(name), &detail); err != nil {
		return Group{}, fmt.Errorf("read group %q: %w", name, err)
	}

	members := make([]GroupMember, 0, len(detail.Members))
	for _, member := range detail.Members {
		members = append(members, GroupMember(member))
	}
	// UAM returns users and API clients in directory order; sort for stable Terraform state.
	slices.SortFunc(members, func(a, b GroupMember) int { return strings.Compare(a.Name, b.Name) })

	return Group{ID: summary.ID, URN: detail.URN, Name: name, Description: summary.Description, Members: members}, nil
}

// CreateGroup checks the list after a failure because UAM reports duplicate names as an
// untyped 502.
func (c *Runtime) CreateGroup(ctx context.Context, name string) (Group, error) {
	if err := c.postForSvc(ctx, svcUAM, tenantGroupsPath, createGroupRequest{GroupName: name}); err != nil {
		if _, lookupErr := c.groupSummary(ctx, name); lookupErr == nil {
			return Group{}, fmt.Errorf("create group %q: the name is already in use for this tenant: %w", name, err)
		}
		return Group{}, fmt.Errorf("create group %q: %w", name, err)
	}
	return c.GetGroup(ctx, name)
}

// DeleteGroup uses the group name despite the route parameter being called groupID.
func (c *Runtime) DeleteGroup(ctx context.Context, name string) error {
	switch _, err := c.groupSummary(ctx, name); {
	case errors.Is(err, ErrResourceNotFound):
		return nil
	case err != nil:
		return err
	}

	if err := c.deleteForSvc(ctx, svcUAM, tenantGroupsPath+"/"+escapedPathSegment(name)); err != nil {
		return fmt.Errorf("delete group %q: %w", name, err)
	}
	return nil
}

// groupSummary resolves existence from the collection read.
func (c *Runtime) groupSummary(ctx context.Context, name string) (groupResponse, error) {
	var list groupListResponse
	if err := c.getForSvc(ctx, svcUAM, tenantGroupsPath, &list); err != nil {
		return groupResponse{}, fmt.Errorf("list groups: %w", err)
	}
	for _, group := range list.Groups {
		if group.Name == name {
			return group, nil
		}
	}
	return groupResponse{}, ErrResourceNotFound
}
