package p2pmax

import (
	"sync"
	"testing"

	"github.com/gurupras/p2pmax/types"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestP2PConnectionCreate(t *testing.T) {
	require := require.New(t)

	testSetupP2PConnections(require)
}

func TestP2PConnectionClose(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)

	wg := sync.WaitGroup{}
	wg.Add(2)

	onDone := func(pcs webrtc.PeerConnectionState) {
		if pcs == webrtc.PeerConnectionStateClosed || pcs == webrtc.PeerConnectionStateDisconnected {
			wg.Done()
		}
	}
	pc1.OnConnectionStateChange(onDone)
	pc2.OnConnectionStateChange(onDone)

	pc1.Close()
	pc2.Close()

	wg.Wait()
}

func testSetupP2PConnections(require *require.Assertions) (*P2PConnection, *P2PConnection) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	pc1Name := "pc-1"
	pc2Name := "pc-2"

	var (
		pc1 *P2PConnection
		pc2 *P2PConnection
		err error
	)
	pc1, err = CreateNewP2PConnection(config, func(pkt *types.SignalPacket) error {
		go func(pkt *types.SignalPacket) {
			if err = pc2.OnSignalPacket(pkt); err != nil {
				log.Errorf("%v", err)
			}
		}(pkt)
		return nil
	}, pc1Name, Offerer, pc2Name)
	require.Nil(err)
	require.NotNil(pc1)

	_, err = pc1.CreateDataChannel("data", nil)
	require.Nil(err)

	pc2, err = CreateNewP2PConnection(config, func(pkt *types.SignalPacket) error {
		go func(pkt *types.SignalPacket) {
			if err = pc1.OnSignalPacket(pkt); err != nil {
				log.Errorf("%v", err)
			}
		}(pkt)
		return nil
	}, pc2Name, Answerer, pc1Name)
	require.Nil(err)
	require.NotNil(pc2)

	wg := sync.WaitGroup{}
	wg.Add(2)

	onConnectionStateChange := func(pcs webrtc.PeerConnectionState) {
		if pcs == webrtc.PeerConnectionStateConnected {
			wg.Done()
		}
	}

	pc1.OnConnectionStateChange(onConnectionStateChange)
	pc2.OnConnectionStateChange(onConnectionStateChange)

	err = pc1.Offer(pc2Name)
	require.Nil(err)

	wg.Wait()

	pc1.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		log.Debugf("[%v]: Connection state changed to %v", pc1.Name, pcs)
	})
	pc2.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		log.Debugf("[%v]: Connection state changed to %v", pc2.Name, pcs)
	})

	return pc1, pc2
}
