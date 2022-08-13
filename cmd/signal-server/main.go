package main

import (
	"github.com/alecthomas/kingpin"
	"github.com/gurupras/p2pmax/test_utils"
	log "github.com/sirupsen/logrus"
)

var (
	verbose = kingpin.Flag("verbose", "Verbose logs").Short('v').Bool()
)

func main() {
	kingpin.Parse()

	if *verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	server := test_utils.New(3333)
	server.Start()
	server.Wait()
}
