package client

import (
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
