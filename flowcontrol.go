package p2pmax

import (
	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

var MaxBufferedAmount = uint64(1 * 1024 * 1024)
var BufferedAmountLowThreshold = uint64(128 * 1024)

type FlowControlledDataChannel struct {
	DataChannel  *webrtc.DataChannel
	Raw          datachannel.ReadWriteCloser
	maxBuffered  uint64
	lowThreshold uint64
	sendMoreChan chan struct{}
}

func (f *FlowControlledDataChannel) Write(b []byte) (n int, err error) {
	l := len(b)
	if f.DataChannel.BufferedAmount()+uint64(l) > MaxBufferedAmount {
		// log.Debugf("Datachannel buffer full: %v\n", b.CombinedDataChannel.dc.Label())
		// Wait until the bufferedAmount becomes lower than the threshold
		<-f.sendMoreChan
		// log.Debugf("Datachannel buffer low: %v\n", b.CombinedDataChannel.dc.Label())
	}
	return f.Raw.Write(b)
}

func (f *FlowControlledDataChannel) Read(b []byte) (int, error) {
	return f.Raw.Read(b)
}

func (f *FlowControlledDataChannel) Close() error {
	return f.Raw.Close()
}

func SetupFlowControl(dc *webrtc.DataChannel, maxBuffered, lowThreshold uint64) (*FlowControlledDataChannel, error) {
	raw, err := dc.Detach()
	if err != nil {
		return nil, err
	}
	ret := &FlowControlledDataChannel{
		DataChannel:  dc,
		Raw:          raw,
		maxBuffered:  maxBuffered,
		lowThreshold: lowThreshold,
		sendMoreChan: make(chan struct{}, 1),
	}

	dc.SetBufferedAmountLowThreshold(lowThreshold)

	dc.OnBufferedAmountLow(func() {
		log.Debugf("Writing to sendMoreChan")
		ret.sendMoreChan <- struct{}{}
		log.Debugf("Wrote to sendMoreChan")
	})
	return ret, nil
}
