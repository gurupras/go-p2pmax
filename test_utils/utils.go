package test_utils

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"math/rand"
	"net/url"
	"time"
)

func WriteRandomDataSize(dst io.Writer, size int64) error {
	// Write some data to this file
	dataReader := io.LimitReader(rand.New(rand.NewSource(time.Now().UnixNano())), size)
	_, err := io.Copy(dst, dataReader)
	return err
}

func CalculateChecksum(src io.Reader) (string, error) {
	algorithm := md5.New()
	_, err := io.Copy(algorithm, src)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(algorithm.Sum(nil)), nil
}

// CreateDeviceIDQuery creates a URL object that contains the passed in deviceID
func CreateDeviceIDQuery(id, urlStr string) string {
	u, _ := url.Parse(urlStr)
	q := u.Query()
	q.Set("deviceID", id)
	u.RawQuery = q.Encode()
	return u.String()
}
