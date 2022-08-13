package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/alecthomas/units"
	"github.com/gorilla/websocket"
	"github.com/gurupras/p2pmax"
	"github.com/gurupras/p2pmax/test_utils"
	"github.com/gurupras/p2pmax/types"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

var (
	server         = kingpin.Flag("server", "server URL").Default("ws://localhost:3333/ws").String()
	id             = kingpin.Flag("id", "This node's ID").Default("receiver").String()
	peer           = kingpin.Flag("peer", "Peer ID").Default("sender").String()
	verbose        = kingpin.Flag("verbose", "Verbose logs").Short('v').Bool()
	cpuprofile     = kingpin.Flag("cpu-profile", "Grab a CPU profile").Default("").String()
	cpuprofileRate = kingpin.Flag("cpu-profile-rate", "CPU profile rate").Int()
	offerer        = kingpin.Flag("offerer", "Act as offerer").Default("false").Bool()
	numConnections = kingpin.Flag("num-connections", "Number of connections to set up").Default("0").Int()
)

func main() {
	kingpin.Parse()

	if *verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if cpuprofileRate != nil {
			runtime.SetCPUProfileRate(*cpuprofileRate)
		}
		pprof.StartCPUProfile(f)

		c := make(chan os.Signal, 2)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM) // subscribe to system signals
		onKill := func(c chan os.Signal) {
			<-c
			log.Warnf("Received termination signal.. writing profile")
			pprof.StopCPUProfile()
			f.Close()
			os.Exit(0)
		}

		// try to handle os interrupt(signal terminated)
		go onKill(c)
	}

	var role p2pmax.Role
	if *offerer {
		role = p2pmax.Offerer
	} else {
		role = p2pmax.Answerer
	}

	wsURL := test_utils.CreateDeviceIDQuery(*id, *server)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("Failed to set up websocket connection: %v", err)
	}

	log.Debugf("Websocket ready")

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	}

	pc, err := p2pmax.CreateNewP2PConnection(config, func(pkt *types.SignalPacket) error {
		log.Debugf("Attempting to send signal packet to signal server")
		err := ws.WriteJSON(pkt)
		if err != nil {
			log.Fatalf("Failed to send signal packet to signal server")
		}
		log.Debugf("Sent signal packet to signal server")
		return nil
	}, *id, role, *peer)
	if err != nil {
		log.Fatalf("Failed to set up peer connection: %v", err)
	}

	go func() {
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Errorf("Failed to read message: %v", err)
				return
			}
			var m types.SignalPacket
			err = json.Unmarshal(message, &m)
			if err != nil {
				log.Errorf("Failed to unmarshal into signal packet: %v", err)
				return
			}
			pc.OnSignalPacket(&m)
		}
	}()

	if role == p2pmax.Offerer {
		pc.CreateDataChannel("dummy", nil)
		pc.Offer(*peer)
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		if pcs == webrtc.PeerConnectionStateConnected {
			wg.Done()
		}
	})
	wg.Wait()

	pcMax, err := p2pmax.New(*id, pc.PeerConnection, role)
	if err != nil {
		log.Errorf("Failed to set up p2pmax connection")
		return
	}
	pcMax.Start()

	wg = sync.WaitGroup{}
	wg.Add(*numConnections)
	for idx := 0; idx < *numConnections; idx++ {
		pc, err := pcMax.CreateNewConnection(fmt.Sprintf("conn-%v", idx+1), role, *peer)
		if err != nil {
			log.Fatalf("Failed to set up connection: %v", err)
		}
		pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
			if pcs == webrtc.PeerConnectionStateConnected {
				wg.Done()
			}
		})
	}
	wg.Wait()

	log.Infof("Ready to transfer")
	// Perform a transfer
	var (
		start time.Time
		end   time.Time
	)

	xfer := 0

	if role == p2pmax.Offerer {
		size := 64*1024 - 8
		buf := bytes.NewBuffer(make([]byte, size))

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pcMax.Read()
		}()
		loopStart := time.Now()
		start = loopStart
		for {
			now := time.Now()
			if now.Sub(loopStart).Seconds() >= 10 {
				break
			}
			buf.Reset()
			test_utils.WriteRandomDataSize(buf, int64(size))
			xfer += size
			pcMax.Write(buf.Bytes())
		}
		pcMax.Write([]byte("Done"))
		wg.Wait()
		end = time.Now()
	} else {
		start = time.Now()
		for {
			b := pcMax.Read()
			if len(b) == 4 && string(b) == "Done" {
				break
			}
			xfer += len(b)
		}
		pcMax.Write([]byte("Done"))
		end = time.Now()
		time.Sleep(time.Millisecond * 100)
	}

	xferMB := float64(xfer) / float64(units.MiB)
	log.Infof("Transferred %.2fMB in %.1fs", xferMB, (end.Sub(start).Seconds()))
}
