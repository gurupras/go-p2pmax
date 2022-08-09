package p2pmax

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/gurupras/p2pmax/types"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

const MaxPacketSize = 65535

type P2PMax struct {
	Name              string
	pc                *webrtc.PeerConnection
	signalDataChannel io.ReadWriter
	mutex             sync.Mutex
	ConnectionMap     map[string]*RawP2PConnection
	onPacket          func(*types.Packet)
	onConnection      func(*webrtc.PeerConnection)
	outgoingChan      chan []byte
	incomingChan      chan []byte
	readyWG           sync.WaitGroup
	lengthEncoded     *LengthEncoded
}

type RawP2PConnection struct {
	*P2PConnection
	dc            io.ReadWriteCloser
	readyWG       sync.WaitGroup
	lengthEncoded *LengthEncoded
}

func New(name string, pc *webrtc.PeerConnection, role Role) (*P2PMax, error) {
	signalChannelName := "p2pmax-signal"
	dataChannelName := "p2pmax-data"

	ret := &P2PMax{
		Name:              name,
		pc:                pc,
		signalDataChannel: nil,
		mutex:             sync.Mutex{},
		onPacket:          nil,
		ConnectionMap:     make(map[string]*RawP2PConnection),
		incomingChan:      make(chan []byte),
		outgoingChan:      make(chan []byte),
		readyWG:           sync.WaitGroup{},
		lengthEncoded:     NewLengthEncoded(MaxPacketSize),
	}
	ret.readyWG.Add(1) // Wait for signalDataChannel

	startRawConn := func(rawDC io.ReadWriteCloser) {
		rawConn := &RawP2PConnection{
			P2PConnection: &P2PConnection{
				Name:           name,
				Role:           role,
				Peer:           "unknown",
				PeerConnection: pc,
				onError:        nil,
			},
			dc:            rawDC,
			readyWG:       sync.WaitGroup{},
			lengthEncoded: ret.lengthEncoded,
		}
		go rawConn.ReadLoop(ret.incomingChan)
		go rawConn.WriteLoop(ret.outgoingChan)
		ret.mutex.Lock()
		defer ret.mutex.Unlock()
		ret.ConnectionMap[name] = rawConn
	}

	if role == Offerer {
		getRawDataChannel := func(channelName string, flowControl bool) (io.ReadWriteCloser, error) {
			var ret io.ReadWriteCloser
			var err error
			wg := sync.WaitGroup{}
			wg.Add(1)
			ordered := true
			maxRetransmits := uint16(100)
			dc, err := pc.CreateDataChannel(channelName, &webrtc.DataChannelInit{
				Ordered:        &ordered,
				MaxRetransmits: &maxRetransmits,
			})
			if err != nil {
				log.Errorf("Failed to create data channel: %v", err)
			}
			dc.OnOpen(func() {
				defer wg.Done()
				if flowControl {
					ret, err = SetupFlowControl(dc, MaxBufferedAmount, BufferedAmountLowThreshold)
				} else {
					ret, err = dc.Detach()
				}
			})
			wg.Wait()
			return ret, err
		}

		signalDataChannel, err := getRawDataChannel(signalChannelName, true)
		if err != nil {
			return nil, fmt.Errorf("failed to start signal dataChannel: %v", err)
		}
		ret.signalDataChannel = signalDataChannel
		ret.readyWG.Done()

		dataChannel, err := getRawDataChannel(dataChannelName, true)
		if err != nil {
			return nil, fmt.Errorf("failed to start data dataChannel: %v", err)
		}
		startRawConn(dataChannel)
	} else {
		var sigDC io.ReadWriteCloser
		var dataDC io.ReadWriteCloser
		var err error

		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnOpen(func() {
				if dc.Label() == signalChannelName {
					ret.mutex.Lock()
					defer ret.mutex.Unlock()
					sigDC, err = SetupFlowControl(dc, MaxBufferedAmount, BufferedAmountLowThreshold)
					if err != nil {
						log.Fatalf("[%v]: Failed to set up signal dataChanel: %v", ret.Name, err)
					}
					ret.signalDataChannel = sigDC
					ret.readyWG.Done() // We have the signal data channel
				} else if dc.Label() == dataChannelName {
					ret.mutex.Lock()
					dataDC, err = SetupFlowControl(dc, MaxBufferedAmount, BufferedAmountLowThreshold)
					if err != nil {
						log.Fatalf("[%v]: Failed to set up data dataChanel: %v", ret.Name, err)
					}
					ret.mutex.Unlock()
					startRawConn(dataDC)
				}
			})
		})
	}
	return ret, nil
}

func (p *P2PMax) Start() {
	go p.signalLoop()
}

func (p *P2PMax) AutoScale() {
}

func (p *P2PMax) OnPacket(cb func(*types.Packet)) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.onPacket = cb
}

func (p *P2PMax) OnConnection(cb func(*webrtc.PeerConnection)) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.onConnection = cb
}

func (p *P2PMax) CreateNewConnection(name string, role Role, peer string) (*RawP2PConnection, error) {
	existingConfig := p.pc.GetConfiguration()
	config := webrtc.Configuration{
		ICEServers: existingConfig.ICEServers,
	}
	connName := fmt.Sprintf("%v-%v", p.Name, name)
	peerConnName := fmt.Sprintf("%v-%v", peer, name)
	signalFn := func(pkt *types.SignalPacket) error {
		// First, write length of pkt, then pkt
		b, err := json.Marshal(pkt)
		if err != nil {
			log.Errorf("[%v]: Failed to marshal signal packet: %v", connName, err)
			return err
		}
		p.mutex.Lock()
		defer p.mutex.Unlock()
		log.Debugf("[%v]: Writing %v bytes (length-encoded) on signalDataChannel", connName, len(b))
		err = p.lengthEncoded.Write(p.signalDataChannel, b)
		if err != nil {
			return err
		}
		log.Debugf("[%v]: Wrote %v bytes (length-encoded) on signalDataChannel", connName, len(b))
		return nil
	}
	pc, err := CreateNewP2PConnection(config, signalFn, connName, role, peerConnName)
	if err != nil {
		return nil, err
	}
	if p.Name == "pc-2" {
		log.Debugf("[%v]: Created new P2PConnection object: %v", p.Name, connName)
	}
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		log.Debugf("[%v]: Connection state changed to: %v", connName, pcs)
		if pcs == webrtc.PeerConnectionStateClosed || pcs == webrtc.PeerConnectionStateDisconnected {
			p.mutex.Lock()
			defer p.mutex.Unlock()
			delete(p.ConnectionMap, connName)
		}
	})

	ret := &RawP2PConnection{
		P2PConnection: pc,
		readyWG:       sync.WaitGroup{},
		lengthEncoded: p.lengthEncoded,
	}
	ret.readyWG.Add(1)
	go ret.ReadLoop(p.incomingChan)
	go ret.WriteLoop(p.outgoingChan)

	p.mutex.Lock()
	p.ConnectionMap[connName] = ret
	p.mutex.Unlock()

	onDataChannelOpen := func(dc *webrtc.DataChannel) {
		fcdc, err := SetupFlowControl(dc, MaxBufferedAmount, BufferedAmountLowThreshold)
		if err != nil {
			log.Errorf("[%v]: Failed to set up flow control: %v", connName, err)
		}
		ret.dc = fcdc
		ret.readyWG.Done()
	}

	if role == Offerer {
		// Send packet to the other end telling them to create a connection
		ncp := types.NewConnectionPacket{
			Packet: &types.Packet{
				Type: types.NewConnectionPacketType,
				Data: []byte(fmt.Sprintf("\"%v\"", name)),
			},
			From: p.Name,
		}
		b, _ := json.Marshal(ncp)
		p.mutex.Lock()
		if err = p.lengthEncoded.Write(p.signalDataChannel, b); err != nil {
			return nil, fmt.Errorf("failed to signal creation of new connection: %v", err)
		}
		p.mutex.Unlock()
		// We need to kickstart the connection
		ordered := true
		maxRetransmits := uint16(100)
		dc, err := pc.CreateDataChannel("p2pmax-data", &webrtc.DataChannelInit{
			Ordered:        &ordered,
			MaxRetransmits: &maxRetransmits,
		})
		if err != nil {
			return nil, err
		}
		dc.OnOpen(func() {
			onDataChannelOpen(dc)
		})

		err = pc.Offer(peerConnName)
		if err != nil {
			return nil, fmt.Errorf("failed to send offer for new connection: %v", err)
		}
	} else {
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnOpen(func() {
				onDataChannelOpen(dc)
			})
		})
	}
	return ret, nil
}

func (p *P2PMax) signalLoop() {
	p.readyWG.Wait()

	handleMessage := func(poolBytes *PoolBytes) error {
		var err error
		var pkt types.Packet
		defer poolBytes.Dispose()

		b := poolBytes.Bytes()
		if err = json.Unmarshal(b, &pkt); err != nil {
			log.Errorf("[%v]: Failed to unmarshal bytes into Packet: %v", p.Name, err)
			return err
		}
		if p.onPacket != nil {
			p.onPacket(&pkt)
		}
		log.Debugf("[%v]: Received packet: %v", p.Name, pkt.Type)
		switch pkt.Type {
		case types.NewConnectionPacketType:
			var pkt types.NewConnectionPacket
			if err = json.Unmarshal(b, &pkt); err != nil {
				log.Errorf("[%v]: Failed to unmarshal bytes into NewConnectionPacket: %v", p.Name, err)
				return err
			}

			var newConnectionName string
			if err = json.Unmarshal(pkt.Data, &newConnectionName); err != nil {
				log.Errorf("[%v]: Failed to unmarshal name of new connection: %v", p.Name, err)
				return err
			}
			pc, err := p.CreateNewConnection(newConnectionName, Answerer, pkt.From)
			if err != nil {
				log.Errorf("[%v]: Failed to set up a new peer connection %v <-> %v", p.Name, newConnectionName, pkt.From)
			}
			if p.onConnection != nil {
				p.onConnection(pc.PeerConnection)
			}
		case types.CandidatePacketType:
			fallthrough
		case types.SDPPacketType:
			var data types.SignalPacket
			if err = json.Unmarshal(b, &data); err != nil {
				log.Errorf("[%v]: Failed to unmarshal bytes into SignalPacket: %v", p.Name, err)
				return err
			}
			p.mutex.Lock()
			pc, ok := p.ConnectionMap[data.Peer]
			if !ok {
				log.Errorf("[%v]: Did not find connection '%v'", p.Name, data.Peer)
				return err
			}
			p.mutex.Unlock()
			pc.OnSignalPacket(&data)
		}
		return nil
	}
	for {
		poolBytes, err := p.lengthEncoded.Read(p.signalDataChannel)
		if err != nil {
			log.Errorf("[%v]: %v", p.Name, err)
			return
		}
		if err := handleMessage(poolBytes); err != nil {
			return
		}
	}
}

func (p *P2PMax) Write(b []byte) error {
	p.outgoingChan <- b
	return nil
}

func (p *P2PMax) Read() []byte {
	return <-p.incomingChan
}

func (p *P2PMax) Close() error {
	// TODO: Stop autoscaling
	// TODO: mutex
	log.Debugf("[%v]: Closing all connections", p.Name)
	for _, pc := range p.ConnectionMap {
		if pc.Role == Offerer {
			pc.Close()
		}
	}
	// TODO: We should close p.pc too, but this hangs when we do it on the answerer
	log.Debugf("[%v]: Closed all connections", p.Name)
	return nil
}

func (r *RawP2PConnection) ReadLoop(outChan chan<- []byte) {
	r.readyWG.Wait()
	log.Debugf("[%v]: Starting read loop", r.Name)
	for {
		b, err := readLengthEncoded(r.dc)
		if err != nil {
			log.Errorf("[%v]: %v", r.Name, err)
			return
		}
		_ = b
		outChan <- b
	}
}

func (r *RawP2PConnection) WriteLoop(inChan <-chan []byte) {
	r.readyWG.Wait()
	for b := range inChan {
		log.Debugf("[%v]: Attempting to send %v bytes", r.Name, len(b))
		err := r.lengthEncoded.Write(r.dc, b)
		if err != nil {
			log.Errorf("Failed to write data to channel: %v", err)
			return
		}
		log.Debugf("[%v]: Sent %v bytes", r.Name, len(b))
	}
}
