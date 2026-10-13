package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/nl-ams-dspc/terraform-provider-asc/internal/asc/infra"
)

// Mapping between the generated infra-gateway wire types and the provider models.
// Optional generated fields are pointers; a nil pointer maps to the zero value, and a
// present but empty list stays an empty, non-nil slice so reads keep the distinction the
// JSON made.

// optionalString sends a string only when it is set, matching an omitempty field.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func infraTags(tags []Tag) *[]infra.Tag {
	if len(tags) == 0 {
		return nil
	}
	wire := make([]infra.Tag, len(tags))
	for i, tag := range tags {
		wire[i] = infra.Tag{Key: tag.Key, Value: tag.Value}
	}
	return &wire
}

func tagsFromInfra(wire *[]infra.Tag) []Tag {
	if wire == nil {
		return nil
	}
	tags := make([]Tag, len(*wire))
	for i, tag := range *wire {
		tags[i] = Tag{Key: tag.Key, Value: tag.Value}
	}
	return tags
}

// sortedStrings copies an optional list and sorts it; the gateway builds attached volume
// lists by ranging over a map.
func sortedStrings(wire *[]string) []string {
	if wire == nil {
		return nil
	}
	values := slices.Clone(*wire)
	if values == nil {
		values = []string{}
	}
	slices.Sort(values)
	return values
}

func stringMap(wire *map[string]string) map[string]string {
	if wire == nil {
		return nil
	}
	return *wire
}

func skuFromInfra(wire *infra.SKUResponse) SKU {
	if wire == nil {
		return SKU{}
	}
	return SKU{
		ID:          value(wire.ID),
		Name:        value(wire.Name),
		Family:      value(wire.Family),
		Threads:     uint64(value(wire.Threads)),
		Cores:       uint64(value(wire.Cores)),
		MemoryInMB:  uint64(value(wire.MemoryInMB)),
		StorageInGB: uint64(value(wire.StorageInGB)),
		StorageType: value(wire.StorageType),
		GPUCount:    uint64(value(wire.GPUCount)),
		GPUType:     value(wire.GPUType),
	}
}

func osFromInfra(wire *infra.OSDetails) OS {
	if wire == nil {
		return OS{}
	}
	return OS{
		ID:           value(wire.ID),
		Family:       value(wire.Family),
		Distribution: value(wire.Distribution),
		Release:      value(wire.Release),
		DisplayName:  value(wire.DisplayName),
	}
}

// infraCustomization keeps the handwritten wire shape: cloud-init fields are omitted when
// empty, ignition always sends format and config.
func infraCustomization(customization *Customization) *infra.Customization {
	if customization == nil {
		return nil
	}
	wire := &infra.Customization{}
	if cloudInit := customization.CloudInit; cloudInit != nil {
		wire.CloudInit = &infra.CloudInitCustomization{
			UserData: optionalString(cloudInit.UserData),
			MetaData: optionalString(cloudInit.MetaData),
		}
	}
	if ignition := customization.Ignition; ignition != nil {
		format := infra.IgnitionFormat(ignition.Format)
		config := ignition.Config
		wire.Ignition = &infra.IgnitionCustomization{Format: &format, Config: &config}
	}
	return wire
}

// DefaultImage is the catalog default from the infra-gateway README. The gateway requires
// an image on create now, so an omitted one is filled in here.
const DefaultImage = "ubuntu-24.04-lts"

// DefaultBootDiskSizeGB matches the size ASC's own system tests create.
const DefaultBootDiskSizeGB = 32

func orDefaultImage(image string) string {
	if image == "" {
		return DefaultImage
	}
	return image
}

func orDefaultBootDiskSize(sizeGB int) int {
	if sizeGB <= 0 {
		return DefaultBootDiskSizeGB
	}
	return sizeGB
}

// createVMWire is the VM create body. Current gateways read bootDisk and reject a create
// without it, while gateways built before bootDisk existed read only the top-level image
// and ignore unknown fields. The generated type has only bootDisk, so the image is sent
// in both places, the top-level one only when the user configured it.
type createVMWire struct {
	infra.CreateVMJSONRequestBody
	Image *string `json:"image,omitempty"`
}

func createVMBody(request CreateVMRequest) (io.Reader, error) {
	enableLogging := request.EnableLogging
	image := orDefaultImage(request.Image)
	size := orDefaultBootDiskSize(request.BootDiskSizeGB)
	wire := createVMWire{
		CreateVMJSONRequestBody: infra.CreateVMJSONRequestBody{
			Name:  request.Name,
			SkuID: request.SKUID,
			VpcID: request.VPCID,
			BootDisk: infra.BootDisk{
				AttachMode: infra.Create,
				DiskSizeGB: &size,
				Image:      &image,
			},
			Tags:          infraTags(request.Tags),
			Customization: infraCustomization(request.Customization),
			// The handwritten request always sent enableLogging, including false.
			EnableLogging: &enableLogging,
		},
		Image: optionalString(request.Image),
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode VM create request: %w", err)
	}
	return bytes.NewReader(encoded), nil
}
