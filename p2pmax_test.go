package p2pmax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/gurupras/p2pmax/test_utils"
	"github.com/gurupras/p2pmax/types"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestP2PMaxNew(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)
	defer pc1.Close()
	defer pc2.Close()

	max1, err := New(pc1.Name, pc1.PeerConnection, pc1.Role)
	require.Nil(err)
	require.NotNil(max1)
}

func TestP2PMaxReadLoop(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)
	defer pc1.Close()
	defer pc2.Close()

	max1, err := New(pc1.Name, pc1.PeerConnection, pc1.Role)
	require.Nil(err)
	require.NotNil(max1)

	buf := bytes.NewBuffer(nil)
	ncp := types.NewConnectionPacket{
		Packet: &types.Packet{
			Type: types.NewConnectionPacketType,
			Data: []byte("\"dummy\""),
		},
		From: "dummy",
	}
	b, err := json.Marshal(ncp)
	require.Nil(err)

	max1.lengthEncoded.Write(buf, b)

	max1.signalDataChannel = buf
	max1.Start()

	wg := sync.WaitGroup{}
	wg.Add(1)
	max1.OnPacket(func(p *types.Packet) {
		wg.Done()
	})
	wg.Wait()
	max1.Close()
}

func TestP2PMaxCreateNewConnection(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)

	max1, err := New(pc1.Name, pc1.PeerConnection, pc1.Role)
	require.Nil(err)
	require.NotNil(max1)
	log.Debugf("Set up max1")
	max1.Start()

	max2, err := New(pc2.Name, pc2.PeerConnection, pc2.Role)
	require.Nil(err)
	require.NotNil(max2)
	log.Debugf("Set up max2")
	max2.Start()

	name := "connection-1"

	log.Debugf("Starting test")
	pc, err := max1.CreateNewConnection(name, Offerer, pc2.Name)
	require.Nil(err)
	require.NotNil(pc)

	wg := sync.WaitGroup{}
	wg.Add(1)

	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		if pcs == webrtc.PeerConnectionStateConnected {
			wg.Done()
		}
	})
	log.Debugf("Waiting for connection ready ...")
	wg.Wait()
	log.Debugf("Connection ready")
	max1.Close()
	max2.Close()
}

func TestP2PMaxSendData(t *testing.T) {
	runTest := func(size int, t *testing.T) {
		require := require.New(t)

		pc1, pc2 := testSetupP2PConnections(require)

		max1, err := New(pc1.Name, pc1.PeerConnection, pc1.Role)
		require.Nil(err)
		require.NotNil(max1)
		log.Debugf("Set up max1")
		max1.Start()

		max2, err := New(pc2.Name, pc2.PeerConnection, pc2.Role)
		require.Nil(err)
		require.NotNil(max2)
		log.Debugf("Set up max2")
		max2.Start()

		log.Debugf("Starting test")

		wg := sync.WaitGroup{}
		wg.Add(1)

		var (
			got      []byte
			expected []byte
		)
		go func() {
			defer wg.Done()
			got = max2.Read()
		}()

		buf := bytes.NewBuffer(nil)
		test_utils.WriteRandomDataSize(buf, int64(size))
		expected = buf.Bytes()

		max1.Write(expected)

		wg.Wait()
		require.Equal(expected, got)
		max1.Close()
		max2.Close()
	}

	t.Run("32K", func(t *testing.T) {
		runTest(32*1024, t)
	})
	t.Run("128K", func(t *testing.T) {
		runTest(128*1024, t)
	})
	t.Run("1M", func(t *testing.T) {
		runTest(1*1024*1024, t)
	})
	t.Run("10M", func(t *testing.T) {
		runTest(10*1024*1024, t)
	})
}

func TestP2PMaxTransferData(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)

	max1, err := New(pc1.Name, pc1.PeerConnection, pc1.Role)
	require.Nil(err)
	require.NotNil(max1)
	log.Debugf("Set up max1")
	max1.Start()

	max2, err := New(pc2.Name, pc2.PeerConnection, pc2.Role)
	require.Nil(err)
	require.NotNil(max2)
	log.Debugf("Set up max2")
	max2.Start()

	numConnections := 10
	wg := sync.WaitGroup{}
	wg.Add(numConnections)
	max2.OnConnection(func(pc *webrtc.PeerConnection) {
		defer wg.Done()
	})
	for idx := 0; idx < numConnections; idx++ {
		name := fmt.Sprintf("conn-%v", idx+1)
		pc, err := max1.CreateNewConnection(name, Offerer, pc2.Name)
		require.Nil(err)
		require.NotNil(pc)
	}
	wg.Wait()

	log.Debugf("Starting transfer")

	// Now that all connections are ready, send data
	size := 1 * 1024 * 1024
	packetCount := 100

	wg = sync.WaitGroup{}
	wg.Add(packetCount)

	go func() {
		for idx := 0; idx < packetCount; idx++ {
			b := max2.Read()
			require.Equal(size, len(b))
			wg.Done()
		}
	}()

	buf := bytes.NewBuffer(nil)
	for idx := 0; idx < packetCount; idx++ {
		test_utils.WriteRandomDataSize(buf, int64(size))
		err := max1.Write(buf.Bytes())
		require.Nil(err)
		buf.Reset()
	}

	wg.Wait()
}
