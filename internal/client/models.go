package client

// Tag is a resource tag as the provider models it.
type Tag struct {
	Key   string
	Value string
}

type VPC struct {
	ID        string
	URN       string
	Name      string
	CIDR      string
	Status    string
	LastError string
	Subnets   []Subnet
	Tags      []Tag
}

type CreateVPCRequest struct {
	Name    string
	Tags    []Tag
	Subnets []CreateSubnetRequest
}

type Subnet struct {
	ID        string
	URN       string
	Name      string
	CIDR      string
	Type      string
	VPCID     string
	Status    string
	LastError string
	Tags      []Tag
}

type CreateSubnetRequest struct {
	Name  string
	CIDR  string
	VPCID string
	Type  string
	Tags  []Tag
}

type VM struct {
	URN             string
	Name            string
	VPCID           string
	SKU             SKU
	Status          string
	LastError       string
	Tags            []Tag
	AttachedVolumes []string
	OS              OS
	EnableLogging   bool
}

type SKU struct {
	ID          string
	Name        string
	Family      string
	Threads     uint64
	Cores       uint64
	MemoryInMB  uint64
	StorageInGB uint64
	StorageType string
	GPUCount    uint64
	GPUType     string
}

type OS struct {
	ID           string
	Family       string
	Distribution string
	Release      string
	DisplayName  string
}

type CreateVMRequest struct {
	Name          string
	SKUID         string
	VPCID         string
	Image         string
	Tags          []Tag
	Customization *Customization
	EnableLogging bool
}

type Customization struct {
	CloudInit *CloudInitCustomization
	Ignition  *IgnitionCustomization
}

type CloudInitCustomization struct {
	UserData string
	MetaData string
}

type IgnitionCustomization struct {
	Format string
	Config string
}

func (v VPC) lifecycle() (string, string) { return v.Status, v.LastError }

func (v VPC) tags() []Tag { return v.Tags }

func (s Subnet) lifecycle() (string, string) { return s.Status, s.LastError }

func (s Subnet) tags() []Tag { return s.Tags }

func (v VM) lifecycle() (string, string) { return v.Status, v.LastError }

func (v VM) tags() []Tag { return v.Tags }
