package tests

import "github.com/nl-ams-dspc/terraform-provider-asc/internal/client"

// wire renders a client model the way infra-gateway spells it on the wire. The client
// models carry no JSON tags since the generated clients took over decoding, so the fake
// gateway must not rely on encoding them directly. Other values pass through unchanged.
func wire(value any) any {
	switch v := value.(type) {
	case client.VPC:
		return map[string]any{
			"id": v.ID, "urn": v.URN, "name": v.Name, "cidr": v.CIDR, "status": v.Status,
			"lastError": v.LastError, "subnets": wireSubnets(v.Subnets), "tags": wireTags(v.Tags),
		}
	case client.Subnet:
		return wireSubnet(v)
	case []client.Subnet:
		return wireSubnets(v)
	case client.VM:
		return map[string]any{
			"urn": v.URN, "name": v.Name, "vpcID": v.VPCID, "sku": wireSKU(v.SKU), "status": v.Status,
			"lastError": v.LastError, "tags": wireTags(v.Tags), "attachedVolumes": v.AttachedVolumes,
			"os": map[string]any{
				"id": v.OS.ID, "family": v.OS.Family, "distribution": v.OS.Distribution,
				"release": v.OS.Release, "displayName": v.OS.DisplayName,
			},
			"enableLogging": v.EnableLogging,
		}
	case client.SKU:
		return wireSKU(v)
	case []client.SKU:
		if v == nil {
			return nil
		}
		skus := make([]map[string]any, len(v))
		for i, sku := range v {
			skus[i] = wireSKU(sku)
		}
		return skus
	case client.Volume:
		return map[string]any{
			"urn": v.URN, "name": v.Name, "size": v.Size, "storageClass": v.StorageClass,
			"accessMode": v.AccessMode, "volumeMode": v.VolumeMode, "status": v.Status,
			"lastError": v.LastError, "tenantId": v.TenantID, "attachedToVM": v.AttachedToVM,
			"tags": wireTags(v.Tags), "labels": v.Labels, "annotations": v.Annotations,
		}
	case client.VMGroup:
		policy := map[string]any{
			"min_replicas": v.AutoscalingPolicy.MinReplicas, "max_replicas": v.AutoscalingPolicy.MaxReplicas,
		}
		if cron := v.AutoscalingPolicy.Cron; cron != nil {
			policy["cron"] = map[string]any{
				"timezone": cron.Timezone, "start": cron.Start, "end": cron.End, "desiredReplicas": cron.DesiredReplicas,
			}
		}
		return map[string]any{
			"urn": v.URN, "name": v.Name, "sku": wireSKU(v.SKU), "status": v.Status, "lastError": v.LastError,
			"tags": wireTags(v.Tags), "attachedVolumes": v.AttachedVolumes, "autoscalingPolicy": policy,
		}
	case []client.Tag:
		return wireTags(v)
	default:
		return value
	}
}

func wireSubnet(v client.Subnet) map[string]any {
	return map[string]any{
		"id": v.ID, "urn": v.URN, "name": v.Name, "cidr": v.CIDR, "type": v.Type, "vpcID": v.VPCID,
		"status": v.Status, "lastError": v.LastError, "tags": wireTags(v.Tags),
	}
}

func wireSubnets(subnets []client.Subnet) any {
	if subnets == nil {
		return nil
	}
	out := make([]map[string]any, len(subnets))
	for i, subnet := range subnets {
		out[i] = wireSubnet(subnet)
	}
	return out
}

func wireSKU(v client.SKU) map[string]any {
	return map[string]any{
		"id": v.ID, "name": v.Name, "family": v.Family, "threads": v.Threads, "cores": v.Cores,
		"memoryInMB": v.MemoryInMB, "storageInGB": v.StorageInGB, "storageType": v.StorageType,
		"GPUCount": v.GPUCount, "GPUType": v.GPUType,
	}
}

func wireTags(tags []client.Tag) any {
	if tags == nil {
		return nil
	}
	out := make([]map[string]string, len(tags))
	for i, tag := range tags {
		out[i] = map[string]string{"key": tag.Key, "value": tag.Value}
	}
	return out
}
