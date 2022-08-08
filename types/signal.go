package types

import "encoding/json"

type Packet struct {
	Type PacketType      `json:"type"`
	Data json.RawMessage `json:"data"`
}

type SignalPacket struct {
	*Packet
	From string `json:"from"`
	Peer string `json:"peer"`
}

type NewConnectionPacket struct {
	*Packet
	From string `json:"from"`
}

type PacketType string

const (
	NewConnectionPacketType PacketType = "new-connection"
	CandidatePacketType     PacketType = "candidate"
	SDPPacketType           PacketType = "sdp"
)
