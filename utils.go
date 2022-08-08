package p2pmax

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	log "github.com/sirupsen/logrus"
)

func writeLengthEncoded(writer io.Writer, b []byte, optChunkSize ...int) error {
	lenBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(lenBytes, uint64(len(b)))

	_, err := writer.Write(lenBytes)
	if err != nil {
		return err
	}
	if len(optChunkSize) != 0 {
		chunkSize := optChunkSize[0]
		if chunkSize <= 0 {
			return fmt.Errorf("chunk size must be greater than 0")
		}
		start := 0
		for start < len(b) {
			end := int(math.Min(float64(start+chunkSize), float64(len(b))))
			_, err = writer.Write(b[start:end])
			if err != nil {
				return err
			}
			start = end
		}
	} else {
		_, err = writer.Write(b)
	}
	return err
}

func readLengthEncoded(reader io.Reader) ([]byte, error) {
	var n uint64
	lenBytes := make([]byte, 8)
	_, err := reader.Read(lenBytes)
	if err != nil {
		return nil, fmt.Errorf("error during length read: %v", err)
	}
	if err = binary.Read(bytes.NewBuffer(lenBytes), binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("failed to convert bytes to uint64: %v", err)
	}
	log.Debugf("Attempting to read %v bytes", n)
	// OK, now that we have the total packet length, read that many bytes in
	b := make([]byte, n)
	_, err = io.ReadFull(reader, b)
	if err != nil {
		return nil, fmt.Errorf("error during bytes read: %v", err)
	}
	return b, nil
}
