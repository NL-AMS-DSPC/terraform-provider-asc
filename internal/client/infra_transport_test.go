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
		"id": subnet.ID, "urn": subnet.URN, "name": subnet.Name, "cidr": subnet.CIDR,
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
		"vnics": vm.VNICs,
		"os": map[string]any{
			"id": vm.OS.ID, "family": vm.OS.Family, "distribution": vm.OS.Distribution,
			"release": vm.OS.Release, "displayName": vm.OS.DisplayName,
		},
	}
}

func vnicData(vnic VNIC) map[string]any {
	return map[string]any{
		"urn": vnic.URN, "name": vnic.Name, "subnetURN": vnic.SubnetURN, "vmURN": vnic.VMURN,
		"privateIP": vnic.PrivateIP, "publicIP": vnic.PublicIP, "mac": vnic.MAC, "status": vnic.Status,
		"errorReason": vnic.LastError, "tags": tagsData(vnic.Tags),
	}
}

func volumeData(volume Volume) map[string]any {
	return map[string]any{
		"urn": volume.URN, "name": volume.Name, "size": volume.Size,
		"volumeMode": volume.VolumeMode, "status": volume.Status,
		"lastError": volume.LastError, "tenantId": volume.TenantID, "attachedToVM": volume.AttachedToVM,
		"tags": tagsData(volume.Tags), "labels": volume.Labels, "annotations": volume.Annotations,
	}
}

func setTag(key, value string) infra.PatchTagRequest {
	return infra.PatchTagRequest{Key: key, Value: &value}
}

func deleteTag(key string) infra.PatchTagRequest { return setTag(key, "") }

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
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/vnics"):
			w.WriteHeader(http.StatusCreated)
			writeEnvelope(w, map[string]any{"urn": "asc:vnic:id", "status": "pending"})
		case r.Method != http.MethodGet:
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			writeEnvelope(w, []map[string]any{{"name": "s"}})
		case strings.Contains(r.URL.Path, "/volumes/"):
			writeEnvelope(w, map[string]any{"name": "data", "size": "20Gi", "status": "detached"})
		// VNICs read as attached to the machine below, so a VM create finishes.
		case strings.Contains(r.URL.Path, "/vnics/"):
			writeEnvelope(w, map[string]any{
				"name": "x", "publicIP": "203.0.113.5", "status": "attached", "vmURN": "urn:vm:x",
				"tags": []map[string]any{{"key": "env", "value": "prod"}},
			})
		default:
			writeEnvelope(w, map[string]any{"urn": "urn:vm:x", "name": "x", "status": "active", "tags": []map[string]any{{"key": "env", "value": "prod"}}})
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
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{Name: "v", CIDR: "10.0.0.0/24", Tags: []Tag{}, Subnets: []CreateSubnetRequest{
				{Name: "s", CIDR: "10.0.0.0/25"},
			}})
			return err
		}, `{"name":"v","cidr":"10.0.0.0/24","subnets":[{"name":"s","cidr":"10.0.0.0/25"}]}`},
		{"create VPC with nil subnets", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{Name: "v", CIDR: "10.0.0.0/24"})
			return err
		}, `{"name":"v","cidr":"10.0.0.0/24","subnets":null}`},
		{"create VPC with tags", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVPC(context.Background(), CreateVPCRequest{
				Name: "v", CIDR: "10.0.0.0/24", Tags: []Tag{{Key: "env", Value: "prod"}}, Subnets: []CreateSubnetRequest{
					{Name: "s", CIDR: "10.0.0.0/25", VPCID: "id", Tags: []Tag{{Key: "a", Value: "b"}}},
				},
			})
			return err
		}, `{"name":"v","cidr":"10.0.0.0/24","tags":[{"key":"env","value":"prod"}],"subnets":[{"name":"s","cidr":"10.0.0.0/25","tags":[{"key":"a","value":"b"}]}]}`},
		{"create subnet", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateSubnet(context.Background(), "v", CreateSubnetRequest{
				Name: "s", CIDR: "10.0.0.0/25", VPCID: "id", Tags: []Tag{{Key: "env", Value: "prod"}},
			})
			return err
		}, `{"name":"s","cidr":"10.0.0.0/25","vpcID":"id","tags":[{"key":"env","value":"prod"}]}`},
		{"create subnet without VPC ID", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateSubnet(context.Background(), "v", CreateSubnetRequest{Name: "s", CIDR: "10.0.0.0/25"})
			return err
		}, `{"name":"s","cidr":"10.0.0.0/25"}`},
		{"create VM minimal", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{Name: "x", SKUID: "sku", VNICs: []string{"urn:a"}})
			return err
		}, `{"name":"x","skuID":"sku","vnics":["urn:a"],"bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu-24.04-lts"}}`},
		{"create VM with cloud-init", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{
				Name: "x", SKUID: "sku", VNICs: []string{"urn:a", "urn:b"}, Image: "ubuntu", Tags: []Tag{{Key: "env", Value: "prod"}},
				Customization: cloudInit,
			})
			return err
		}, `{"name":"x","skuID":"sku","vnics":["urn:a","urn:b"],"bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu"},"tags":[{"key":"env","value":"prod"}],"customization":{"cloudInit":{"userData":"#cloud-config"}}}`},
		{"create VM with ignition", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVM(context.Background(), CreateVMRequest{Name: "x", SKUID: "sku", VNICs: []string{"urn:a"}, Customization: ignition})
			return err
		}, `{"name":"x","skuID":"sku","vnics":["urn:a"],"bootDisk":{"attachMode":"create","diskSizeGB":32,"image":"ubuntu-24.04-lts"},"customization":{"ignition":{"format":"butane","config":""}}}`},
		{"create VNIC minimal", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVNIC(context.Background(), CreateVNICRequest{SubnetURN: "urn:subnet"})
			return err
		}, `{"subnetURN":"urn:subnet"}`},
		{"create VNIC full", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVNIC(context.Background(), CreateVNICRequest{
				Name: "eth1", SubnetURN: "urn:subnet", Tags: []Tag{{Key: "env", Value: "prod"}},
			})
			return err
		}, `{"name":"eth1","subnetURN":"urn:subnet","tags":[{"key":"env","value":"prod"}]}`},
		{"create volume", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVolume(context.Background(), CreateVolumeRequest{Name: "data", Size: "10Gi"})
			return err
		}, `{"name":"data","size":"10Gi","storageClass":"standard","accessMode":"ReadWriteOnce"}`},
		{"create volume with tags", http.MethodPost, func(c *Runtime) error {
			_, err := c.CreateVolume(context.Background(), CreateVolumeRequest{
				Name: "data", Size: "10Gi", Tags: []Tag{{Key: "env", Value: "prod"}},
			})
			return err
		}, `{"name":"data","size":"10Gi","storageClass":"standard","accessMode":"ReadWriteOnce","tags":[{"key":"env","value":"prod"}]}`},
		{"resize volume", http.MethodPost, func(c *Runtime) error {
			_, err := c.ResizeVolume(context.Background(), "data", "20Gi")
			return err
		}, `{"size":"20Gi"}`},
		{"tag patch", http.MethodPatch, func(c *Runtime) error {
			_, err := c.UpdateVMTags(context.Background(), "x",
				[]Tag{{Key: "env", Value: "test"}, {Key: "owner", Value: "platform"}}, []Tag{{Key: "env", Value: "prod"}})
			return err
		}, `{"tags":[{"key":"env","value":"prod"},{"key":"owner","value":""}]}`},
		{"VNIC tag patch", http.MethodPatch, func(c *Runtime) error {
			_, err := c.UpdateVNICTags(context.Background(), "id", nil, []Tag{{Key: "env", Value: "prod"}})
			return err
		}, `{"tags":[{"key":"env","value":"prod"}]}`},
		// The PUT that renames also sets the public IP, so the current one is sent back.
		{"VNIC rename", http.MethodPut, func(c *Runtime) error {
			_, err := c.RenameVNIC(context.Background(), "id", "x")
			return err
		}, `{"name":"x","publicIP":"203.0.113.5"}`},
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
		OS: OS{ID: "os", Family: "linux", Distribution: "ubuntu", Release: "24.04", DisplayName: "Ubuntu"},
	}
	subnet := Subnet{ID: "s-id", URN: "urn:s", Name: "s", CIDR: "10.0.0.0/25", VPCID: "vpc-id",
		Status: "active", LastError: "none", Tags: []Tag{}}
	vpc := VPC{ID: "vpc-id", URN: "urn:vpc", Name: "vpc", CIDR: "10.0.0.0/24", Status: "active", LastError: "none",
		Subnets: []Subnet{subnet}, Tags: []Tag{{Key: "env", Value: "prod"}}}
	volume := Volume{URN: "urn:vol", Name: "data", Size: "10Gi",
		VolumeMode: "Filesystem", Status: "attached", LastError: "none", TenantID: "t", AttachedToVM: "vm",
		Tags: []Tag{{Key: "env", Value: "prod"}}, Labels: map[string]string{"l": "1"}, Annotations: map[string]string{"a": "2"}}
	vnic := VNIC{ID: "vnic-id", URN: "asc:network:t:vnic:vnic-id", Name: "eth1", SubnetURN: "urn:s", VMURN: "urn:vm",
		PrivateIP: "10.0.0.5", PublicIP: "203.0.113.10", MAC: "00:00:00:00:00:01", Status: "attached", LastError: "none",
		Tags: []Tag{{Key: "env", Value: "prod"}}}

	runtime := newLifecycleRuntime(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/infra-gateway/vnics/vnic-id":
			writeEnvelope(w, vnicData(vnic))
		case "/api/infra-gateway/vms/vm":
			writeEnvelope(w, vmData(vm))
		case "/api/infra-gateway/vpcs/vpc":
			writeEnvelope(w, vpcData(vpc))
		case "/api/infra-gateway/vpcs/vpc/subnets":
			writeEnvelope(w, subnetsData(vpc.Subnets))
		case "/api/infra-gateway/volumes/data":
			writeEnvelope(w, volumeData(volume))
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
	gotVNIC, err := runtime.GetVNIC(ctx, "vnic-id")
	if err != nil || !reflect.DeepEqual(gotVNIC, vnic) {
		t.Errorf("GetVNIC() = %#v, %v, want %#v", gotVNIC, err, vnic)
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
