package p2pmax

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/gurupras/p2pmax/test_utils"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"
)

func TestSetupFlowControl(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)
	testSetupFlowControl(require, pc1.PeerConnection, pc2.PeerConnection)
}

func TestFlowControlData(t *testing.T) {
	require := require.New(t)

	pc1, pc2 := testSetupP2PConnections(require)
	fc1, fc2 := testSetupFlowControl(require, pc1.PeerConnection, pc2.PeerConnection)

	num := 10

	for idx := 0; idx < num; idx++ {
		size := int64(idx*1024*1024) + 1024
		buf := bytes.NewBuffer(nil)
		writer := io.MultiWriter(buf, fc1)

		wg := sync.WaitGroup{}
		wg.Add(1)

		var gotChecksum string
		go func() {
			defer wg.Done()
			var err error
			buf := bytes.NewBuffer(make([]byte, size))
			n, err := io.ReadFull(fc2, buf.Bytes())
			require.Nil(err)
			require.Equal(int(size), n)

			gotChecksum, err = test_utils.CalculateChecksum(buf)
			require.Nil(err)
		}()

		err := test_utils.WriteRandomDataSize(writer, size)
		require.Nil(err)

		checksum, err := test_utils.CalculateChecksum(buf)
		require.Nil(err)
		wg.Wait()

		require.Equal(checksum, gotChecksum)
	}
}

func testSetupFlowControl(require *require.Assertions, pc1 *webrtc.PeerConnection, pc2 *webrtc.PeerConnection) (*FlowControlledDataChannel, *FlowControlledDataChannel) {
	wg := sync.WaitGroup{}
	wg.Add(1)

	var dc2 *webrtc.DataChannel
	pc2.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "test" {
			wg.Done()
			dc2 = dc
		}
	})
	dc1, err := pc1.CreateDataChannel("test", nil)
	require.Nil(err)

	wg.Wait()

	fc1, err := SetupFlowControl(dc1, MaxBufferedAmount, BufferedAmountLowThreshold)
	require.Nil(err)
	require.NotNil(fc1)

	fc2, err := SetupFlowControl(dc2, MaxBufferedAmount, BufferedAmountLowThreshold)
	require.Nil(err)
	require.NotNil(fc2)

	return fc1, fc2
}
