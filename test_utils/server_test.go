package test_utils

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/gurupras/p2pmax/types"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	log.Debugf("Returning port: %v", port)
	return port, nil
}

func setupServer() (*Server, error) {
	var err error
	var port int
	port, err = getFreePort()
	if err != nil {
		return nil, err
	}

	server := New(port)
	server.Start()
	return server, nil
}

func TestWebSocketServerSetupServer(t *testing.T) {
	require := require.New(t)

	server, err := setupServer()
	require.Nil(err)
	require.NotNil(server)
}

func TestWebSocketServerStop(t *testing.T) {
	require := require.New(t)

	server, err := setupServer()
	require.Nil(err)
	require.NotNil(server)

	server.Stop()

	u1 := CreateDeviceIDQuery("device-1", server.URL)
	_, _, err = websocket.DefaultDialer.Dial(u1, nil)
	require.NotNil(err)
}

func TestWebSocketServerUpdatesDeviceMapOnConnection(t *testing.T) {
	require := require.New(t)

	server, err := setupServer()
	require.Nil(err)
	require.NotNil(server)

	wg := sync.WaitGroup{}
	wg.Add(1)

	server.onWebSocketConn = func(c *websocket.Conn) {
		defer wg.Done()
	}

	d1 := "device-1"
	u1 := CreateDeviceIDQuery(d1, server.URL)
	ws, _, err := websocket.DefaultDialer.Dial(u1, nil)
	require.Nil(err)
	require.NotNil(ws)

	wg.Wait()

	c := server.DeviceMap[d1]
	require.NotNil(c)
}

func TestWebSocketServerClosingConnectionCleansDeviceMap(t *testing.T) {
	require := require.New(t)

	server, err := setupServer()
	require.Nil(err)
	require.NotNil(server)

	wg := sync.WaitGroup{}
	wg.Add(1)

	server.onWebSocketConn = func(c *websocket.Conn) {
		defer wg.Done()
	}

	d1 := "device-1"
	u1 := CreateDeviceIDQuery(d1, server.URL)
	ws, _, err := websocket.DefaultDialer.Dial(u1, nil)
	require.Nil(err)
	require.NotNil(ws)

	wg.Wait()

	c := server.DeviceMap[d1]
	require.NotNil(c)

	wg = sync.WaitGroup{}
	wg.Add(1)

	server.onWebSocketConnClose = func(c *websocket.Conn) {
		defer wg.Done()
	}

	ws.Close()
	wg.Wait()

	_, ok := server.DeviceMap[d1]
	require.False(ok)
}

func TestWebSocketServerReturnsErrorOnNoDeviceID(t *testing.T) {
	require := require.New(t)

	server, err := setupServer()
	require.Nil(err)
	require.NotNil(server)

	_, _, err = websocket.DefaultDialer.Dial(fmt.Sprintf("ws://0.0.0.0:%v/ws", server.port), nil)
	require.NotNil(err)
}

func TestWebSocketServerForward(t *testing.T) {
	require := require.New(t)

	server, _ := setupServer()

	d1 := "device-1"
	d2 := "device-2"
	data := "{}"

	u1 := CreateDeviceIDQuery(d1, server.URL)
	u2 := CreateDeviceIDQuery(d2, server.URL)

	log.Debugf("u1: %v", u1)
	log.Debugf("u2: %v", u2)

	ws1, _, err := websocket.DefaultDialer.Dial(u1, nil)
	require.Nil(err)

	ws2, _, err := websocket.DefaultDialer.Dial(u2, nil)
	require.Nil(err)

	// Send a packet on ws1 and expect it to arrive on ws2
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, message, err := ws2.ReadMessage()
		require.Nil(err)
		var m types.SignalPacket
		err = json.Unmarshal(message, &m)
		require.Nil(err)
		require.Equal(m.Peer, d2)
		require.Equal(m.Data, json.RawMessage([]byte(data)))
	}()
	pkt := types.SignalPacket{
		Packet: &types.Packet{
			Type: types.CandidatePacketType,
			Data: []byte(data),
		},
		From: d1,
		Peer: d2,
	}
	err = ws1.WriteJSON(pkt)
	require.Nil(err)
	wg.Wait()
	server.Stop()
}
