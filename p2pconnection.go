package p2pmax

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gurupras/p2pmax/types"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type Role int

const (
	Offerer  Role = iota
	Answerer Role = iota
)

type P2PConnection struct {
	Name string
	Role
	sync.Mutex
	Peer string
	*webrtc.PeerConnection
	pendingCandidates []*webrtc.ICECandidate
	onError           func(error)
	connWG            sync.WaitGroup
	signal            func(pkt *types.SignalPacket) error
}

func (p *P2PConnection) sendCandidates(peer string) error {
	p.Lock()
	defer p.Unlock()
	for _, c := range p.pendingCandidates {
		if err := p.sendCandidateLocked(peer, c); err != nil {
			return err
		}
	}
	return nil
}

func (p *P2PConnection) sendCandidateLocked(peer string, c *webrtc.ICECandidate) error {
	b, _ := json.Marshal(c.ToJSON().Candidate)
	pkt := &types.SignalPacket{
		Packet: &types.Packet{
			Type: types.CandidatePacketType,
			Data: b,
		},
		From: p.Name,
		Peer: peer,
	}
	return p.signal(pkt)
}

func (p *P2PConnection) SendSDP(peer string, sdp *webrtc.SessionDescription) error {
	p.Lock()
	defer p.Unlock()
	return p.sendSDPLocked(peer, sdp)
}

func (p *P2PConnection) sendSDPLocked(peer string, sdp *webrtc.SessionDescription) error {
	b, _ := json.Marshal(sdp)
	pkt := &types.SignalPacket{
		Packet: &types.Packet{
			Type: types.SDPPacketType,
			Data: b,
		},
		From: p.Name,
		Peer: peer,
	}
	return p.signal(pkt)
}

func (p *P2PConnection) OnError(f func(error)) {
	p.onError = f
}

func (p *P2PConnection) Offer(peer string) error {
	p.Lock()
	defer p.Unlock()
	offer, err := p.CreateOffer(nil)
	if err != nil {
		log.Errorf("Failed to create offer: %v", err)
		return err
	}
	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	if err = p.SetLocalDescription(offer); err != nil {
		log.Errorf("Failed to set local description: %v", err)
		return err
	}
	if err = p.sendSDPLocked(peer, &offer); err != nil {
		log.Errorf("Failed to send offer: %v", err)
		return err
	}
	log.Debugf("[%v] Offer --> %v", p.Name, peer)
	return nil
}

func (p *P2PConnection) Close() error {
	err := p.PeerConnection.Close()
	return err
}

func (p *P2PConnection) OnSignalPacket(pkt *types.SignalPacket) error {
	switch pkt.Type {
	case types.CandidatePacketType:
		var candidate string
		if err := json.Unmarshal(pkt.Data, &candidate); err != nil {
			log.Errorf("Failed to unmarshal candidate: %v", err)
			return err
		}
		err := p.AddICECandidate(webrtc.ICECandidateInit{
			Candidate: candidate,
		})
		if err != nil {
			log.Errorf("Failed to add ICE candidate: %v", err)
			return err
		}
		log.Debugf("[%v]: Added ICE candidate received from peer: %v", p.Name, pkt.From)
	case types.SDPPacketType:
		var sdp webrtc.SessionDescription
		if err := json.Unmarshal(pkt.Data, &sdp); err != nil {
			log.Errorf("Failed to parse SDP: %v", err)
			return err
		}
		if err := p.SetRemoteDescription(sdp); err != nil {
			log.Errorf("Failed to set remote description: %v", err)
			break
		}
		if p.Role == Answerer {
			answer, err := p.CreateAnswer(nil)
			if err != nil {
				log.Errorf("Failed to create answer: %v", err)
				return err
			}
			if err := p.SendSDP(pkt.From, &answer); err != nil {
				log.Errorf("Failed to send answer: %v", err)
				return err
			}
			log.Debugf("[%v] Answer --> %v", p.Name, pkt.From)
			err = p.SetLocalDescription(answer)
			if err != nil {
				log.Errorf("Failed to set local description: %v", err)
				return err
			}
		}
		if err := p.sendCandidates(pkt.From); err != nil {
			log.Errorf("Failed to send candidate: %v", err)
			return err
		}
	}
	return nil
}

func CreateNewP2PConnection(config webrtc.Configuration, signal func(pkt *types.SignalPacket) error, name string, role Role, peer string) (*P2PConnection, error) {
	// Create a SettingEngine and enable Detach
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))
	// Create a new RTCPeerConnection
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		fmt.Printf("Could not create new peer connection: %v", err)
		return nil, err
	}

	p2pConn := &P2PConnection{
		Name:              name,
		Role:              role,
		Peer:              peer,
		PeerConnection:    pc,
		pendingCandidates: make([]*webrtc.ICECandidate, 0),
		onError:           nil,
		connWG:            sync.WaitGroup{},
		signal:            signal,
	}

	p2pConn.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		p2pConn.Lock()
		defer p2pConn.Unlock()
		desc := p2pConn.RemoteDescription()
		if desc == nil {
			p2pConn.pendingCandidates = append(p2pConn.pendingCandidates, i)
			log.Debugf("[%v]: Got (pending) ICE candidate: %v", name, i)
		} else {
			if err := p2pConn.sendCandidateLocked(p2pConn.Peer, i); err != nil {
				log.Errorf("[%v]: Could not send candidate info to peer (%v): %v", name, p2pConn.Peer, err)
				return
			}
			log.Debugf("[%v]: Notified peer (%v) about ICE candidate: %v", name, p2pConn.Peer, i)
		}
	})

	p2pConn.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Debugf("[%v]: Peer Connection State has changed: %s", name, s.String())

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			log.Errorf("[%v]: Peer Connection has gone to failed..", name)
		}
	})
	return p2pConn, nil
}
