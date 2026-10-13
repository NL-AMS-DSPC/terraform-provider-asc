package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

// The helpers below build mock gateway bodies without the provider models' JSON tags; the
// keys are the infra-gateway wire names.

func tagsData(tags []Tag) any {
	if tags == nil {
		return nil
	}
	data := make([]map[string]any, len(tags))
	for i, tag := range tags {
		data[i] = map[string]any{"key": tag.Key, "value": tag.Value}
	}
	return data
}

func skuData(sku SKU) map[string]any {
	return map[string]any{
		"id": sku.ID, "name": sku.Name, "family": sku.Family, "threads": sku.Threads, "cores": sku.Cores,
		"memoryInMB": sku.MemoryInMB, "storageInGB": sku.StorageInGB, "storageType": sku.StorageType,
		"GPUCount": sku.GPUCount, "GPUType": sku.GPUType,
	}
}

func subnetData(subnet Subnet) map[string]any {
	return map[string]any{
		"id": subnet.ID, "urn": subnet.URN, "name": subnet.Name, "cidr": subnet.CIDR, "type": subnet.Type,
		"vpcID": subnet.VPCID, "status": subnet.Status, "lastError": subnet.LastError, "tags": tagsData(subnet.Tags),
	}
}

func subnetsData(subnets []Subnet) any {
	if subnets == nil {
		return nil
	}
	data := make([]map[string]any, len(subnets))
	for i, subnet := range subnets {
		data[i] = subnetData(subnet)
	}
	return data
}

func vpcData(vpc VPC) map[string]any {
	return map[string]any{
		"id": vpc.ID, "urn": vpc.URN, "name": vpc.Name, "cidr": vpc.CIDR, "status": vpc.Status,
		"lastError": vpc.LastError, "subnets": subnetsData(vpc.Subnets), "tags": tagsData(vpc.Tags),
	}
}

func vmData(vm VM) map[string]any {
	return map[string]any{
		"urn": vm.URN, "name": vm.Name, "vpcID": vm.VPCID, "sku": skuData(vm.SKU), "status": vm.Status,
		"lastError": vm.LastError, "tags": tagsData(vm.Tags), "attachedVolumes": vm.AttachedVolumes,
		"os": map[string]any{
			"id": vm.OS.ID, "family": vm.OS.Family, "distribution": vm.OS.Distribution,
			"release": vm.OS.Release, "displayName": vm.OS.DisplayName,
		},
		"enableLogging": vm.EnableLogging,
	}
}

func volumeData(volume Volume) map[string]any {
	return map[string]any{
		"urn": volume.URN, "name": volume.Name, "size": volume.Size, "storageClass": volume.StorageClass,
		"accessMode": volume.AccessMode, "volumeMode": volume.VolumeMode, "status": volume.Status,
		"lastError": volume.LastError, "tenantId": volume.TenantID, "attachedToVM": volume.AttachedToVM,
		"tags": tagsData(volume.Tags), "labels": volume.Labels, "annotations": volume.Annotations,
	}
}

func vmGroupData(group VMGroup) map[string]any {
	policy := map[string]any{
		"min_replicas": group.AutoscalingPolicy.MinReplicas, "max_replicas": group.AutoscalingPolicy.MaxReplicas,
	}
	if cron := group.AutoscalingPolicy.Cron; cron != nil {
		policy["cron"] = map[string]any{
			"timezone": cron.Timezone, "start": cron.Start, "end": cron.End, "desiredReplicas": cron.DesiredReplicas,
		}
	}
	return map[string]any{
		"urn": group.URN, "name": group.Name, "sku": skuData(group.SKU), "status": group.Status,
		"lastError": group.LastError, "tags": tagsData(group.Tags), "attachedVolumes": group.AttachedVolumes,
		"autoscalingPolicy": policy,
	}
}

func setTag(key, value string) infra.PatchTag { return infra.PatchTag{Key: key, Value: &value} }

func deleteTag(key string) infra.PatchTag { return setTag(key, "") }

// assertInfraJSON compares JSON documents by value, so key order does not matter.
func assertInfraJSON(t *testing.T, label string, got []byte, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("%s: decode body %s: %v", label, got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("%s: decode expectation: %v", label, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("%s: body = %s, want %s", label, got, want)
	}
}

// Every infra-gateway operation keeps its method and path, escapes each path parameter
// once as a single segment below an escaped endpoint path, and carries the bearer token
// and tenant.
func TestInfraOperationsRouteAndEscape(t *testing.T) {
	t.Parallel()

	for name, escaped := range map[string]string{"a/b c": "a%2Fb%20c", "literal%2Fname": "literal%252Fname"} {
		base := "/gateway%20prefix/api/infra-gateway"
		operations := []struct {
			want string
			call func(context.Context, *Runtime) error
		}{
			{"POST /vpcs", func(ctx context.Context, c *Runtime) error {
				_, err := c.CreateVPC(ctx, CreateVPCRequest{Name: name})
				return err
			}},
			{"GET /vpcs/" + escaped, func(ctx context.Context, c *Runtime) error { _, err := c.GetVPC(ctx, name); return err }},
			{"PATCH /vpcs/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.UpdateVPCTags(ctx, name, nil, []Tag{{Key: "k", Value: "v"}})
				return err
			}},
			{"DELETE /vpcs/" + escaped, func(ctx context.Context, c *Runtime) error { return c.DeleteVPC(ctx, name) }},
			{"POST /vpcs/" + escaped + "/subnets", func(ctx context.Context, c *Runtime) error {
				_, err := c.CreateSubnet(ctx, name, CreateSubnetRequest{Name: name})
				return err
			}},
			{"GET /vpcs/" + escaped + "/subnets", func(ctx context.Context, c *Runtime) error {
				_, err := c.ListSubnets(ctx, name)
				return err
			}},
			{"PATCH /vpcs/" + escaped + "/subnets/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.UpdateSubnetTags(ctx, name, name, nil, []Tag{{Key: "k", Value: "v"}})
				return err
			}},
			{"DELETE /vpcs/" + escaped + "/subnets/" + escaped, func(ctx context.Context, c *Runtime) error {
				return c.DeleteSubnet(ctx, name, name)
			}},
			{"POST /vms", func(ctx context.Context, c *Runtime) error {
				_, err := c.CreateVM(ctx, CreateVMRequest{Name: name})
				return err
			}},
			{"GET /vms/" + escaped, func(ctx context.Context, c *Runtime) error { _, err := c.GetVM(ctx, name); return err }},
			{"PATCH /vms/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.UpdateVMTags(ctx, name, nil, []Tag{{Key: "k", Value: "v"}})
				return err
			}},
			{"DELETE /vms/" + escaped, func(ctx context.Context, c *Runtime) error { return c.DeleteVM(ctx, name) }},
			{"POST /volumes", func(ctx context.Context, c *Runtime) error {
				_, err := c.CreateVolume(ctx, CreateVolumeRequest{Name: name, Size: "1Gi"})
				return err
			}},
			{"GET /volumes/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.GetVolume(ctx, name)
				return err
			}},
			{"POST /volumes/" + escaped + "/resize", func(ctx context.Context, c *Runtime) error {
				_, err := c.ResizeVolume(ctx, name, "2Gi")
				return err
			}},
			{"PATCH /volumes/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.UpdateVolumeTags(ctx, name, nil, []Tag{{Key: "k", Value: "v"}})
				return err
			}},
			{"DELETE /volumes/" + escaped, func(ctx context.Context, c *Runtime) error { return c.DeleteVolume(ctx, name) }},
			{"POST /volumes/" + escaped + "/attach/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.AttachVolume(ctx, name, name)
				return err
			}},
			{"DELETE /volumes/" + escaped + "/attach/" + escaped, func(ctx context.Context, c *Runtime) error {
				return c.DetachVolume(ctx, name, name)
			}},
			{"POST /vm-groups", func(ctx context.Context, c *Runtime) error {
				_, err := c.CreateVMGroup(ctx, CreateVMGroupRequest{Name: name})
				return err
			}},
			{"GET /vm-groups/" + escaped, func(ctx context.Context, c *Runtime) error {
				_, err := c.GetVMGroup(ctx, name)
				return err
			}},
			{"DELETE /vm-groups/" + escaped, func(ctx context.Context, c *Runtime) error { return c.DeleteVMGroup(ctx, name) }},
			{"GET /skus", func(ctx context.Context, c *Runtime) error { _, err := c.ListSKUs(ctx); return err }},
		}

		for _, operation := range operations {
			t.Run(name+" "+operation.want, func(t *testing.T) {
				t.Parallel()
				var mu sync.Mutex
				var requests []string
				runtime := newGeneratedTestRuntime(t, "/gateway%20prefix", func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Tenant") != "tenant" {
						t.Errorf("%s %s: Authorization = %q, X-Tenant = %q", r.Method, r.URL.EscapedPath(),
							r.Header.Get("Authorization"), r.Header.Get("X-Tenant"))
					}
					mu.Lock()
					requests = append(requests, r.Method+" "+strings.TrimPrefix(r.URL.EscapedPath(), base))
					mu.Unlock()
					// Machine reads succeed so attachment calls reach their command; every
					// other read reports absence so waits end or time out quickly.
					switch {
					case r.Method == http.MethodGet && strings.HasPrefix(r.URL.EscapedPath(), base+"/vms/"):
						writeEnvelope(w, map[string]any{"name": name, "status": "active"})
					case r.Method == http.MethodGet:
						w.WriteHeader(http.StatusNotFound)
					default:
						writeEnvelope(w, map[string]any{})
					}
				})
				runtime.pollInterval = time.Millisecond
				runtime.lifecycleTimeout = 50 * time.Millisecond

				_ = operation.call(context.Background(), runtime)

				mu.Lock()
				defer mu.Unlock()
				for _, request := range requests {
					if request == operation.want {
						return
					}
				}
				t.Errorf("requests = %q, want one %q", requests, operation.want)
			})
		}
	}
}

// bodyRecorder captures the body of the first request with the given method.
type bodyRecorder struct {
	mu     sync.Mutex
	method string
	body   []byte
}

func (b *bodyRecorder) record(r *http.Request) {
	if r.Method != b.method {
		return
	}
	body, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.body == nil {
		b.body = body
	}
}

func (b *bodyRecorder) get() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.body
}

// Request bodies serialize to the JSON the handwritten request structs produced, including
// their omitempty rules.
func TestInfraRequestBodiesMatchTheHandwrittenWire(t *testing.T) {
	t.Parallel()

	settled := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			writeEnvelope(w, []map[string]any{{"name": "s"}})
		case strings.Contains(r.URL.Path, "/volumes/"):
			writeEnvelope(w, map[string]any{"name": "data", "size": "20Gi", "status": "detached"})
		default:
			writeEnvelope(w, map[string]any{"name": "x", "status": "active", "tags": []map[string]any{{"key": "env", "value": "prod"}}})
		}
	}

	cloudInit := &Customization{CloudInit: &CloudInitCustomization{UserData: "#cloud-config"}}
	ignition := &Customization{Ignition: &IgnitionCustomization{Format: "butane"}}
	tests := []struct {
		name   string
		method string
		call   func(*Runtime) error
		want   string
	}{
		{"create VPC without tags", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{Name: "v", Tags: []Tag{}, Subnets: []CreateSubnetRequest{
				{Name: "s", CIDR: "10.0.0.0/25", Type: "private"},
			}})
			return err
		}, `{"name":"v","subnets":[{"name":"s","cidr":"10.0.0.0/25","type":"private"}]}`},
		{"create VPC with nil subnets", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{Name: "v"})
			return err
		}, `{"name":"v","subnets":null}`},
		{"create VPC with tags", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{
				Name: "v", Tags: []Tag{{Key: "env", Value: "prod"}}, Subnets: []CreateSubnetRequest{
					{Name: "s", CIDR: "10.0.0.0/25", VPCID: "id", Type: "public", Tags: []Tag{{Key: "a", Value: "b"}}},
				},
			})
			return err
		}, `{"name":"v","tags":[{"key":"env","value":"prod"}],"subnets":[{"name":"s","cidr":"10.0.0.0/25","vpcID":"id","type":"public","tags":[{"key":"a","value":"b"}]}]}`},
		{"create subnet", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateSubnet(context.Background(), "v", CreateSubnetRequest{
				Name: "s", CIDR: "10.0.0.0/25", VPCID: "id", Type: "public", Tags: []Tag{{Key: "env", Value: "prod"}},
			})
			return err
		}, `{"name":"s","cidr":"10.0.0.0/25","vpcID":"id","type":"public","tags":[{"key":"env","value":"prod"}]}`},
		{"create subnet without VPC ID", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateSubnet(context.Background(), "v", CreateSubnetRequest{Name: "s", CIDR: "10.0.0.0/25", Type: "private"})
			return err
		}, `{"name":"s","cidr":"10.0.0.0/25","type":"private"}`},
		{"create VM minimal", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{Name: "x", SKUID: "sku", VPCID: "id"})
			return err
		}, `{"name":"x","skuID":"sku","vpcID":"id","bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu-24.04-lts"},"enableLogging":false}`},
		{"create VM with a configured boot disk size", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{Name: "x", SKUID: "sku", VPCID: "id", BootDiskSizeGB: 64})
			return err
		}, `{"name":"x","skuID":"sku","vpcID":"id","bootDisk":{"attachMode":"create","diskSizeGB":64,"image":"ubuntu-24.04-lts"},"enableLogging":false}`},
		{"create VM with cloud-init", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{
				Name: "x", SKUID: "sku", VPCID: "id", Image: "ubuntu", Tags: []Tag{{Key: "env", Value: "prod"}},
				Customization: cloudInit, EnableLogging: true,
			})
			return err
		}, `{"name":"x","skuID":"sku","vpcID":"id","image":"ubuntu","bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu"},"tags":[{"key":"env","value":"prod"}],"customization":{"cloudInit":{"userData":"#cloud-config"}},"enableLogging":true}`},
		{"create VM with ignition", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{Name: "x", SKUID: "sku", VPCID: "id", Customization: ignition})
			return err
		}, `{"name":"x","skuID":"sku","vpcID":"id","bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu-24.04-lts"},"customization":{"ignition":{"format":"butane","config":""}},"enableLogging":false}`},
		{"create volume", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVolume(context.Background(), CreateVolumeRequest{Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce"})
			return err
		}, `{"name":"data","size":"10Gi","storageClass":"standard","accessMode":"ReadWriteOnce"}`},
		{"create volume with tags", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVolume(context.Background(), CreateVolumeRequest{
				Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce", Tags: []Tag{{Key: "env", Value: "prod"}},
			})
			return err
		}, `{"name":"data","size":"10Gi","storageClass":"standard","accessMode":"ReadWriteOnce","tags":[{"key":"env","value":"prod"}]}`},
		{"create VM group minimal", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVMGroup(context.Background(), CreateVMGroupRequest{
				Name: "g", SKUID: "sku", VPCID: "id", AutoscalingPolicy: AutoscalingPolicy{MinReplicas: 1, MaxReplicas: 2},
			})
			return err
		}, `{"name":"g","skuID":"sku","vpcID":"id","image":"ubuntu-24.04-lts","diskSizeGB":32,"autoscalers":{"min_replicas":1,"max_replicas":2}}`},
		{"create VM group full", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVMGroup(context.Background(), CreateVMGroupRequest{
				Name: "g", SKUID: "sku", VPCID: "id", Image: "ubuntu",
				AutoscalingPolicy: AutoscalingPolicy{MinReplicas: 1, MaxReplicas: 2, Cron: &CronRule{
					Timezone: "UTC", Start: "0 8 * * *", End: "0 18 * * *", DesiredReplicas: 2,
				}},
				Tags: []Tag{{Key: "env", Value: "prod"}}, Customization: cloudInit,
			})
			return err
		}, `{"name":"g","skuID":"sku","vpcID":"id","image":"ubuntu","diskSizeGB":32,"autoscalers":{"min_replicas":1,"max_replicas":2,"cron":{"timezone":"UTC","start":"0 8 * * *","end":"0 18 * * *","desiredReplicas":2}},"tags":[{"key":"env","value":"prod"}],"customization":{"cloudInit":{"userData":"#cloud-config"}}}`},
		{"resize volume", http.MethodPost, func(c *Runtime) error {
			_, err := c.ResizeVolume(context.Background(), "data", "20Gi")
			return err
		}, `{"size":"20Gi"}`},
		{"tag patch", http.MethodPatch, func(c *Runtime) error {
			_, err := c.UpdateVMTags(context.Background(), "x",
				[]Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}, []Tag{{Key: "env", Value: "prod"}})
			return err
		}, `{"tags":[{"key":"env","value":"prod"},{"key":"owner","value":""}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &bodyRecorder{method: test.method}
			runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
				recorder.record(r)
				if r.Method == test.method && r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
				}
				settled(w, r)
			})
			if err := test.call(runtime); err != nil {
				t.Fatalf("call: %v", err)
			}
			assertInfraJSON(t, test.name, recorder.get(), test.want)
		})
	}
}

// Attaching sends no body, as the handwritten client did.
func TestAttachVolumeSendsNoBody(t *testing.T) {
	t.Parallel()
	var body []byte
	var contentType string
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			body, _ = io.ReadAll(r.Body)
			contentType = r.Header.Get("Content-Type")
			writeEnvelope(w, map[string]string{"volumeName": "data", "vmName": "app"})
		case strings.Contains(r.URL.Path, "/vms/"):
			writeVM(w, "app")
		default:
			writeEnvelope(w, volumeData(Volume{Name: "data", Status: "attached", AttachedToVM: "app"}))
		}
	})
	if _, err := runtime.AttachVolume(context.Background(), "data", "app"); err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if len(body) != 0 || contentType != "" {
		t.Errorf("attach body = %q, Content-Type = %q, want neither", body, contentType)
	}
}

// Every provider model field is read from its wire name.
func TestInfraReadsMapEveryField(t *testing.T) {
	t.Parallel()

	sku := SKU{ID: "sku-1", Name: "gp", Family: "general", Threads: 4, Cores: 2, MemoryInMB: 4096,
		StorageInGB: 80, StorageType: "ssd", GPUCount: 1, GPUType: "a100"}
	vm := VM{
		URN: "urn:vm", Name: "vm", VPCID: "vpc-id", SKU: sku, Status: "active", LastError: "none",
		Tags: []Tag{{Key: "env", Value: "prod"}}, AttachedVolumes: []string{"b", "a"},
		OS:            OS{ID: "os", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu"},
		EnableLogging: true,
	}
	subnet := Subnet{ID: "s-id", URN: "urn:s", Name: "s", CIDR: "10.0.0.0/25", Type: "public", VPCID: "vpc-id",
		Status: "active", LastError: "none", Tags: []Tag{}}
	vpc := VPC{ID: "vpc-id", URN: "urn:vpc", Name: "vpc", CIDR: "10.0.0.0/24", Status: "active", LastError: "none",
		Subnets: []Subnet{subnet}, Tags: []Tag{{Key: "env", Value: "prod"}}}
	volume := Volume{URN: "urn:vol", Name: "data", Size: "10Gi", StorageClass: "standard", AccessMode: "ReadWriteOnce",
		VolumeMode: "Filesystem", Status: "attached", LastError: "none", TenantID: "t", AttachedToVM: "vm",
		Tags: []Tag{{Key: "env", Value: "prod"}}, Labels: map[string]string{"l": "1"}, Annotations: map[string]string{"a": "2"}}
	group := VMGroup{URN: "urn:g", Name: "g", SKU: sku, Status: "active", LastError: "none", Tags: nil,
		AttachedVolumes: []string{"z", "y"}, AutoscalingPolicy: AutoscalingPolicy{MinReplicas: 1, MaxReplicas: 3,
			Cron: &CronRule{Timezone: "UTC", Start: "s", End: "e", DesiredReplicas: 2}}}

	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/infra-gateway/vms/vm":
			writeEnvelope(w, vmData(vm))
		case "/api/infra-gateway/vpcs/vpc":
			writeEnvelope(w, vpcData(vpc))
		case "/api/infra-gateway/vpcs/vpc/subnets":
			writeEnvelope(w, subnetsData(vpc.Subnets))
		case "/api/infra-gateway/volumes/data":
			writeEnvelope(w, volumeData(volume))
		case "/api/infra-gateway/vm-groups/g":
			writeEnvelope(w, vmGroupData(group))
		case "/api/infra-gateway/skus":
			writeEnvelope(w, []map[string]any{skuData(sku)})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()

	gotVM, err := runtime.GetVM(ctx, "vm")
	sort.Strings(vm.AttachedVolumes)
	if err != nil || !reflect.DeepEqual(gotVM, vm) {
		t.Errorf("GetVM() = %#v, %v, want %#v", gotVM, err, vm)
	}
	gotVPC, err := runtime.GetVPC(ctx, "vpc")
	if err != nil || !reflect.DeepEqual(gotVPC, vpc) {
		t.Errorf("GetVPC() = %#v, %v, want %#v", gotVPC, err, vpc)
	}
	gotSubnets, err := runtime.ListSubnets(ctx, "vpc")
	if err != nil || !reflect.DeepEqual(gotSubnets, vpc.Subnets) {
		t.Errorf("ListSubnets() = %#v, %v, want %#v", gotSubnets, err, vpc.Subnets)
	}
	gotVolume, err := runtime.GetVolume(ctx, "data")
	if err != nil || !reflect.DeepEqual(gotVolume, volume) {
		t.Errorf("GetVolume() = %#v, %v, want %#v", gotVolume, err, volume)
	}
	gotGroup, err := runtime.GetVMGroup(ctx, "g")
	sort.Strings(group.AttachedVolumes)
	if err != nil || !reflect.DeepEqual(gotGroup, group) {
		t.Errorf("GetVMGroup() = %#v, %v, want %#v", gotGroup, err, group)
	}
	gotSKUs, err := runtime.ListSKUs(ctx)
	if err != nil || !reflect.DeepEqual(gotSKUs, []SKU{sku}) {
		t.Errorf("ListSKUs() = %#v, %v", gotSKUs, err)
	}
}

// Absent or null lists read as nil and present empty lists as empty, as decoding into the
// handwritten models did.
func TestInfraReadsKeepNilAndEmptyLists(t *testing.T) {
	t.Parallel()
	bodies := map[string]string{
		"/api/infra-gateway/vpcs/absent": `{"data":{"name":"absent"}}`,
		"/api/infra-gateway/vpcs/null":   `{"data":{"name":"null","tags":null,"subnets":null}}`,
		"/api/infra-gateway/vpcs/empty":  `{"data":{"name":"empty","tags":[],"subnets":[]}}`,
		"/api/infra-gateway/vms/absent":  `{"data":{"name":"absent"}}`,
		"/api/infra-gateway/vms/empty":   `{"data":{"name":"empty","tags":[],"attachedVolumes":[]}}`,
	}
	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bodies[r.URL.Path]))
	})
	ctx := context.Background()

	for _, name := range []string{"absent", "null"} {
		vpc, err := runtime.GetVPC(ctx, name)
		if err != nil || vpc.Tags != nil || vpc.Subnets != nil {
			t.Errorf("GetVPC(%s) = %#v, %v, want nil lists", name, vpc, err)
		}
	}
	vpc, err := runtime.GetVPC(ctx, "empty")
	if err != nil || vpc.Tags == nil || len(vpc.Tags) != 0 || vpc.Subnets == nil || len(vpc.Subnets) != 0 {
		t.Errorf("GetVPC(empty) = %#v, %v, want empty lists", vpc, err)
	}
	vm, err := runtime.GetVM(ctx, "absent")
	if err != nil || vm.Tags != nil || vm.AttachedVolumes != nil {
		t.Errorf("GetVM(absent) = %#v, %v, want nil lists", vm, err)
	}
	vm, err = runtime.GetVM(ctx, "empty")
	if err != nil || vm.Tags == nil || vm.AttachedVolumes == nil {
		t.Errorf("GetVM(empty) = %#v, %v, want empty lists", vm, err)
	}
}
