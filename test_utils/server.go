package test_utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/gurupras/p2pmax/types"
	log "github.com/sirupsen/logrus"
)

type Server struct {
	port                 int
	URL                  string
	server               *http.Server
	upgrader             websocket.Upgrader
	mutex                sync.Mutex
	context              context.Context
	cancel               func()
	DeviceMap            map[string]*websocket.Conn
	onWebSocketConn      func(c *websocket.Conn)
	onWebSocketConnClose func(c *websocket.Conn)
	doneChan             chan struct{}
}

func (s *Server) HandleWebsocket(w http.ResponseWriter, r *http.Request) {
	log.Debugf("Received websocket connection")
	deviceID := r.URL.Query().Get("deviceID")
	if deviceID == "" {
		log.Errorf("Websocket connection with no deviceID")
		return
	}
	c, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade websocket: %v", err)
		return
	}
	s.mutex.Lock()
	s.DeviceMap[deviceID] = c
	s.mutex.Unlock()
	log.Debugf("Updated DeviceMap[%v]", deviceID)
	if s.onWebSocketConn != nil {
		s.onWebSocketConn(c)
	}
	go s.websocketLoop(deviceID, c)
}

func (s *Server) websocketLoop(deviceID string, c *websocket.Conn) {
	defer func() {
		c.Close()
		log.Warnf("[%v] websocket connection closed", deviceID)
		s.mutex.Lock()
		defer s.mutex.Unlock()
		delete(s.DeviceMap, deviceID)
		log.Debugf("Removed DeviceMap[%v]", deviceID)
		if s.onWebSocketConnClose != nil {
			s.onWebSocketConnClose(c)
		}
	}()

	for {
		log.Debugf("Reading message from websocket '%v' ...", deviceID)
		mt, message, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				log.Errorf("[%v]: Failed to read message: %v\n", deviceID, err)
			}
			break
		}
		log.Debugf("Got message from websocket '%v'", deviceID)
		pkt := types.SignalPacket{}
		err = json.Unmarshal(message, &pkt)
		if err != nil {
			log.Errorf("[%v]: Failed to parse JSON from message: %v\n", deviceID, err)
			break
		}
		peer := pkt.Peer
		s.mutex.Lock()
		peerConn, ok := s.DeviceMap[peer]
		s.mutex.Unlock()
		if !ok {
			log.Errorf("[%v]: No connection found for peer: %v\n", deviceID, peer)
			continue
		}
		if deviceID == peer {
			log.Warnf("Forwarding message: %v --> %v (%v)", deviceID, peer, string(message))
		}
		err = peerConn.WriteMessage(mt, message)
		if err != nil {
			log.Errorf("[%v]: write to peer-%v failed: %v\n", deviceID, peer, err)
			break
		}
	}
}

func New(port int) *Server {
	r := mux.NewRouter()

	host := fmt.Sprintf("0.0.0.0:%v", port)
	url := fmt.Sprintf("ws://%v/ws", host)

	s := &http.Server{
		Addr:         host,
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	server := &Server{
		port:      port,
		URL:       url,
		server:    s,
		upgrader:  websocket.Upgrader{},
		mutex:     sync.Mutex{},
		context:   ctx,
		cancel:    cancel,
		DeviceMap: make(map[string]*websocket.Conn),
		doneChan:  make(chan struct{}, 1),
	}

	r.HandleFunc("/ws", server.HandleWebsocket)
	return server
}

func (s *Server) Start() error {
	host := fmt.Sprintf("0.0.0.0:%v", s.port)
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return err
	}
	go func() {
		if err := s.server.Serve(listener); err != nil {
			log.Printf("%v", err)
		}
	}()
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		s.Stop()
	}()
	return nil
}

func (s *Server) Stop() {
	defer s.cancel()
	s.server.Shutdown(s.context)
	s.doneChan <- struct{}{}
}

func (s *Server) Wait() {
	<-s.doneChan
}
