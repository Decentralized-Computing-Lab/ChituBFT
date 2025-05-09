package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"strconv"
	"chitu/common"
	"chitu/consensus"
	"chitu/logger"
)

func main() {
	configFile := flag.String("c", "", "config file")
	payloadCon := flag.Int("n", 1, "payload connection num")
	identity := flag.Int("b", 0, "if byzantine node")
	attackSim := flag.Int("a", 0, "if under attack simulation")
	flag.Parse()
	jsonFile, err := os.Open(*configFile)
	if err != nil {
		panic(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	c := new(common.ConfigFile)
	json.Unmarshal(byteValue, c)
	peers := make(map[uint32]*common.Peer)
	for _, p := range c.Peers {
		peers[p.ID] = &common.Peer{
			ID:              p.ID,
			Addr:            p.Addr,
			PublicKey:       p.PublicKey,
			ThresholdPubKey: p.ThresholdPubKey,
			// EcdsaKey:        crypto.LoadKey(p.PublicKey),
		}
	}
	// c.Cfg.EcdsaKey = crypto.LoadKey(c.Cfg.PrivKey)
	var iden, sim bool
	if uint32(*identity) == 0 {
		iden = false
	} else {
		iden = true
	}
	if uint32(*attackSim) == 0 {
		sim = false
	} else {
		sim = true
	}
	node := consensus.NewNode(&c.Cfg, peers, logger.NewZeroLogger("./node"+strconv.FormatUint(uint64(c.Cfg.ID), 10)+".log"), uint32(*payloadCon), iden, sim)
	node.Run()
}
