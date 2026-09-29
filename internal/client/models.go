package client

// Tag is the infra-gateway representation of a resource tag.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type VPC struct {
	ID        string   `json:"id"`
	URN       string   `json:"urn"`
	Name      string   `json:"name"`
	CIDR      string   `json:"cidr"`
	Status    string   `json:"status"`
	LastError string   `json:"lastError"`
	Subnets   []Subnet `json:"subnets"`
	Tags      []Tag    `json:"tags"`
}

type CreateVPCRequest struct {
	Name    string                `json:"name"`
	Tags    []Tag                 `json:"tags,omitempty"`
	Subnets []CreateSubnetRequest `json:"subnets"`
}

type Subnet struct {
	ID        string `json:"id"`
	URN       string `json:"urn"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	Type      string `json:"type"`
	VPCID     string `json:"vpcID"`
	Status    string `json:"status"`
	LastError string `json:"lastError"`
	Tags      []Tag  `json:"tags"`
}

type CreateSubnetRequest struct {
	Name  string `json:"name"`
	CIDR  string `json:"cidr"`
	VPCID string `json:"vpcID,omitempty"`
	Type  string `json:"type"`
	Tags  []Tag  `json:"tags,omitempty"`
}

type VM struct {
	URN             string    `json:"urn"`
	Name            string    `json:"name"`
	VPCID           string    `json:"vpcID"`
	SKU             SKU       `json:"sku"`
	Status          string    `json:"status"`
	LastError       string    `json:"lastError"`
	Tags            []Tag     `json:"tags"`
	AttachedVolumes []string  `json:"attachedVolumes"`
	OS              OS        `json:"os"`
	EnableLogging   bool      `json:"enableLogging"`
	Addresses       Addresses `json:"addresses"`
}

type Addresses struct {
	InternalIP *string  `json:"internalIP"`
	PublicIPs  []string `json:"publicIPs"`
}

type SKU struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Family      string `json:"family"`
	Threads     uint64 `json:"threads"`
	Cores       uint64 `json:"cores"`
	MemoryInMB  uint64 `json:"memoryInMB"`
	StorageInGB uint64 `json:"storageInGB"`
	StorageType string `json:"storageType"`
	GPUCount    uint64 `json:"GPUCount"`
	GPUType     string `json:"GPUType"`
}

type OS struct {
	ID           string `json:"id"`
	Family       string `json:"family"`
	Distribution string `json:"distribution"`
	Release      string `json:"release"`
	DisplayName  string `json:"displayName"`
}

type CreateVMRequest struct {
	Name          string         `json:"name"`
	SKUID         string         `json:"skuID"`
	VPCID         string         `json:"vpcID"`
	Image         string         `json:"image,omitempty"`
	Tags          []Tag          `json:"tags,omitempty"`
	Customization *Customization `json:"customization,omitempty"`
	EnableLogging bool           `json:"enableLogging"`
}

type Customization struct {
	CloudInit *CloudInitCustomization `json:"cloudInit,omitempty"`
	Ignition  *IgnitionCustomization  `json:"ignition,omitempty"`
}

type CloudInitCustomization struct {
	UserData string `json:"userData,omitempty"`
	MetaData string `json:"metaData,omitempty"`
}

type IgnitionCustomization struct {
	Format string `json:"format"`
	Config string `json:"config"`
}

type IPAllocationRequest struct {
	IP string `json:"ip"`
}

type IPDeallocationRequest struct {
	IP string `json:"ip"`
}

func (v VPC) lifecycle() (string, string) { return v.Status, v.LastError }

func (v VPC) tags() []Tag { return v.Tags }

func (s Subnet) lifecycle() (string, string) { return s.Status, s.LastError }

func (s Subnet) tags() []Tag { return s.Tags }

func (v VM) lifecycle() (string, string) { return v.Status, v.LastError }

func (v VM) tags() []Tag { return v.Tags }
