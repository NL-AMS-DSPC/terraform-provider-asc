package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/uam"
)

// tenantUUID caches the namespace lookup for this provider instance. The credentials need
// uam:GetTenant.
func (c *Runtime) tenantUUID(ctx context.Context) (string, error) {
	c.tenantMu.Lock()
	defer c.tenantMu.Unlock()
	if c.tenantID != "" {
		return c.tenantID, nil
	}

	var tenant uam.TenantResponse
	if err := callUAM(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(c.config.Namespace); err != nil {
			return nil, err
		}
		return c.uamAPI().GetTenant(ctx, c.config.Namespace, c.uamEditor())
	}, &tenant); err != nil {
		return "", fmt.Errorf("resolve tenant %q: %w", c.config.Namespace, err)
	}
	id := value(tenant.ID)
	if id == "" {
		return "", fmt.Errorf("resolve tenant %q: UAM reported no tenant id", c.config.Namespace)
	}

	c.tenantID = id
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

// GetRole checks existence through the list, then reads actions from the item route.
// Missing item reads return an ambiguous 502.
func (c *Runtime) GetRole(ctx context.Context, name string) (Role, error) {
	if _, err := c.roleID(ctx, name); err != nil {
		return Role{}, err
	}

	// The item read uses the tenant UUID because UAM GetTenantRole forwards that segment
	// unchanged to authorization. Other role routes translate the namespace to a UUID.
	// Revisit this workaround if GetTenantRole starts translating it too.
	tenant, err := c.tenantUUID(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	// Role writes use actions; item reads use composites. List responses omit both.
	var detail uam.RoleResponse
	if err := callUAM(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(tenant, name); err != nil {
			return nil, err
		}
		return c.uamAPI().GetTenantRole(ctx, tenant, name, c.uamEditor())
	}, &detail); err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", name, err)
	}

	actions := slices.Clone(value(detail.Composites))
	slices.Sort(actions)
	return Role{URN: value(detail.Urn), Name: name, Actions: actions}, nil
}

// CreateRole reads back the role because creation returns only an acknowledgement.
func (c *Runtime) CreateRole(ctx context.Context, name string, actions []string) (Role, error) {
	// Actions is always set so the wire body keeps "actions" even when it is empty or null.
	request := uam.CreateTenantRoleJSONRequestBody{Name: name, Actions: &actions}
	if err := callUAM[struct{}](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(c.config.Namespace); err != nil {
			return nil, err
		}
		return c.uamAPI().CreateTenantRole(ctx, c.config.Namespace, request, c.uamEditor())
	}, nil); err != nil {
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

	// The body carries the role's database uuid because UAM validates it as required and
	// upserts on it. Name and actions are always set; group_id is never sent.
	request := uam.UpdateTenantRoleJSONRequestBody{ID: id, Name: &name, Actions: &actions}
	if err := callUAM[struct{}](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(c.config.Namespace, name); err != nil {
			return nil, err
		}
		return c.uamAPI().UpdateTenantRole(ctx, c.config.Namespace, name, request, c.uamEditor())
	}, nil); err != nil {
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

	if err := callUAM[struct{}](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(c.config.Namespace, name); err != nil {
			return nil, err
		}
		return c.uamAPI().DeleteTenantRole(ctx, c.config.Namespace, name, c.uamEditor())
	}, nil); err != nil {
		return fmt.Errorf("delete role %q: %w", name, err)
	}
	return nil
}

// roleID returns the database uuid the list route records for a role name, and
// ErrResourceNotFound when the tenant has no such role. No list parameters are sent.
func (c *Runtime) roleID(ctx context.Context, name string) (string, error) {
	var roles []uam.RoleResponse
	if err := callUAM(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(c.config.Namespace); err != nil {
			return nil, err
		}
		return c.uamAPI().ListTenantRoles(ctx, c.config.Namespace, nil, c.uamEditor())
	}, &roles); err != nil {
		return "", fmt.Errorf("list roles: %w", err)
	}
	for _, role := range roles {
		if value(role.Name) == name {
			return value(role.ID), nil
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

	var detail uam.GroupResponse
	if err := callUAM(ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(name); err != nil {
			return nil, err
		}
		return c.uamAPI().GetTenantGroup(ctx, name, c.uamEditor())
	}, &detail); err != nil {
		return Group{}, fmt.Errorf("read group %q: %w", name, err)
	}

	listed := value(detail.Members)
	members := make([]GroupMember, 0, len(listed))
	for _, member := range listed {
		members = append(members, GroupMember{Name: value(member.Name), Enabled: value(member.Enabled), Type: value(member.Type)})
	}
	// UAM returns users and API clients in directory order; sort for stable Terraform state.
	slices.SortFunc(members, func(a, b GroupMember) int { return strings.Compare(a.Name, b.Name) })

	return Group{
		ID:          value(summary.ID),
		URN:         value(detail.Urn),
		Name:        name,
		Description: value(summary.Description),
		Members:     members,
	}, nil
}

// CreateGroup checks the list after a failure because UAM reports duplicate names as an
// untyped 502.
func (c *Runtime) CreateGroup(ctx context.Context, name string) (Group, error) {
	request := uam.CreateTenantGroupJSONRequestBody{GroupName: name}
	if err := callUAM[struct{}](ctx, func(ctx context.Context) (*http.Response, error) {
		return c.uamAPI().CreateTenantGroup(ctx, request, c.uamEditor())
	}, nil); err != nil {
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

	if err := callUAM[struct{}](ctx, func(ctx context.Context) (*http.Response, error) {
		if err := checkSegments(name); err != nil {
			return nil, err
		}
		return c.uamAPI().DeleteTenantGroup(ctx, name, c.uamEditor())
	}, nil); err != nil {
		return fmt.Errorf("delete group %q: %w", name, err)
	}
	return nil
}

// groupSummary resolves existence from the collection read. The group list is the only
// UAM collection that is not a bare array.
func (c *Runtime) groupSummary(ctx context.Context, name string) (uam.GroupResponse, error) {
	var list uam.GroupListResponse
	if err := callUAM(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.uamAPI().ListTenantGroups(ctx, c.uamEditor())
	}, &list); err != nil {
		return uam.GroupResponse{}, fmt.Errorf("list groups: %w", err)
	}
	for _, group := range value(list.Groups) {
		if value(group.Name) == name {
			return group, nil
		}
	}
	return uam.GroupResponse{}, ErrResourceNotFound
}
