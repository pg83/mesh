package main

type LinkStatus struct {
	Edge
	Idle int `json:"idle"`
}

type ChannelStatus struct {
	Transport string `json:"transport"`
	Edge
	Outgoing bool          `json:"outgoing"`
	ID       uint64        `json:"id"`
	Wire     SocketAddress `json:"wire"`
}

type Status struct {
	Registry   []RegistryRecord    `json:"registry"`
	Channels   []ChannelStatus     `json:"channels"`
	Dialing    int                 `json:"dialing"`
	Addresses  map[uint32]Vertex   `json:"addresses"`
	Index      uint16              `json:"index"`
	Subnet     string              `json:"subnet"`
	Tun        string              `json:"tun"`
	Links      []LinkStatus        `json:"links"`
	Alive      []uint16            `json:"alive"`
	Graph      []Edge              `json:"graph"`
	Records    []*GraphRecord      `json:"records"`
	Vectors    []*Vector           `json:"vectors"`
	Bundles    int                 `json:"bundles"`
	Vertices   []uint32            `json:"vertices"`
	Routes     map[string][]Edge   `json:"routes"`
	DnsRecords map[string][]string `json:"dns_records,omitempty"`
}
