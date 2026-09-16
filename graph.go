package main

import (
	"time"
)

const (
	costUDP = 3
	costWS  = 6
)

type RecordVertex struct {
	ID uint32 `json:"id"`
	Vertex
	Ingress bool `json:"ingress"`
	Egress  bool `json:"egress"`
}

type Observation struct {
	From uint32 `json:"from"`
	Seen Vertex `json:"seen"`
}

type GraphRecord struct {
	Owner    uint16         `json:"owner"`
	Version  uint64         `json:"version"`
	Exit     bool           `json:"exit,omitempty"`
	Vertices []RecordVertex `json:"vertices"`
	Links    []Edge         `json:"links"`
	Observed []Observation  `json:"observed"`

	packet  []byte
	body    []byte
	applied time.Time
}

func recordFlags(record *GraphRecord) byte {
	if record.Exit {
		return recordExit
	}

	return 0
}

type Distance struct {
	cost, hops int
}
