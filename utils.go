package p2pmax

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"

	log "github.com/sirupsen/logrus"
)

type LengthEncoded struct {
	maxSize         uint64
	lengthBytesPool sync.Pool
	packetBytesPool sync.Pool
}

type PoolBytes struct {
	b       *[]byte
	len     uint64
	Dispose func()
}

func (p *PoolBytes) Bytes() []byte {
	return (*p.b)[:p.len]
}

func NewLengthEncoded(optMaxSize ...uint64) *LengthEncoded {
	maxSize := uint64(0)
	if len(optMaxSize) > 0 {
		maxSize = optMaxSize[0]
	}
	ret := &LengthEncoded{
		maxSize: maxSize,
		lengthBytesPool: sync.Pool{
			New: func() interface{} {
				b := make([]byte, 8)
				return &b
			},
		},
		packetBytesPool: sync.Pool{
			New: func() interface{} {
				b := make([]byte, maxSize)
				return &b
			},
		},
	}
	return ret
}

func (l *LengthEncoded) Write(writer io.Writer, b []byte) error {
	lenBytes := l.lengthBytesPool.Get().(*[]byte)
	binary.LittleEndian.PutUint64(*lenBytes, uint64(len(b)))

	_, err := writer.Write(*lenBytes)
	l.lengthBytesPool.Put(lenBytes)
	if err != nil {
		return err
	}

	if l.maxSize <= 0 {
		_, err = writer.Write(b)
	} else {
		start := 0
		for start < len(b) {
			end := int(math.Min(float64(start+int(l.maxSize)), float64(len(b))))
			_, err = writer.Write(b[start:end])
			if err != nil {
				return err
			}
			start = end
		}
	}
	return err
}

func (l *LengthEncoded) Read(reader io.Reader) (*PoolBytes, error) {
	var n uint64
	lenBytes := l.lengthBytesPool.Get().(*[]byte)
	defer l.lengthBytesPool.Put(lenBytes)

	_, err := reader.Read(*lenBytes)
	if err != nil {
		return nil, fmt.Errorf("error during length read: %v", err)
	}
	if err = binary.Read(bytes.NewBuffer(*lenBytes), binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("failed to convert bytes to uint64: %v", err)
	}

	log.Debugf("Attempting to read %v bytes", n)
	// OK, now that we have the total packet length, read that many bytes in
	b := l.packetBytesPool.Get().(*[]byte)
	_, err = io.ReadFull(reader, (*b)[:n])
	if err != nil {
		l.packetBytesPool.Put(b)
		return nil, fmt.Errorf("error during bytes read: %v", err)
	}
	return &PoolBytes{
		b:   b,
		len: n,
		Dispose: func() {
			l.packetBytesPool.Put(b)
		},
	}, nil
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
