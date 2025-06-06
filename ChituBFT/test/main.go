package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"strconv"
	"chitu/common"
	"chitu/crypto"
)

type NodesSlice struct {
	Nodes []NodeInfo `json:nodes`
}

type NodeInfo struct {
	Id          int    `json:"id"`
	Host        string `json:"host"`
	PrivateAddr string `json:"privaddr"`
}

func main() {
	var ns NodesSlice
	jsonFile, err := os.Open("./nodes.json")
	if err != nil {
		panic(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &ns)

	var dep common.Devp
	depFile, err := os.Open("./devip.json")
	if err != nil {
		panic(err)
	}
	defer depFile.Close()
	byteValue2, _ := ioutil.ReadAll(depFile)
	json.Unmarshal(byteValue2, &dep)

	var n, f int
	var wrong int
	flag.IntVar(&n, "n", 4, "number of nodes")
	flag.IntVar(&f, "f", 1, "number of byzantines")
	flag.IntVar(&wrong, "w", 0, "number of crash nodes with no config files")
	flag.Parse()

	crypto.Init()
	priKeyVec, pubKeyVec, masterPk := crypto.Generate(n, n-f)

	num := n - wrong
	// selected node can be add
	cfgs := make([]common.Config, num)
	peers := make([]common.Peer, num)

	for i := 0; i < num; i++ {
		// encodeBytes, _ := crypto.GenerateEcdsaKey()
		pubKey, priKey, _ := crypto.GenKeyPair()
		port1 := 5000 + i + 1
		port2 := 6000 + i + 1
		port3 := 7000 + i + 1
		cfgs[i] = common.Config{
			ID:           uint32(i + 1),
			N:            uint32(n),
			F:            uint32(f),
			PubKey:       pubKey,
			PrivKey:      priKey,
			MasterPK:     masterPk,
			ThresholdSK:  priKeyVec[i],
			ThresholdPK:  pubKeyVec[i],
			Addr:         ns.Nodes[i].PrivateAddr + ":" + strconv.Itoa(port1),
			ClientServer: ns.Nodes[i].PrivateAddr + ":" + strconv.Itoa(port2),
			RpcServer:    ns.Nodes[i].PrivateAddr + ":" + strconv.Itoa(port3),
			MaxBatchSize: 30000, // requsets
			PayloadSize:  600,   // bytes/req
			MaxWaitTime:  200,   // ms
			Coordinator:  dep.PublicIp + ":9000",
			Time:         30, // s
		}
		peers[i] = common.Peer{
			ID:              uint32(i + 1),
			Addr:            ns.Nodes[i].Host + ":" + strconv.Itoa(port1),
			PublicKey:       pubKey,
			ThresholdPubKey: pubKeyVec[i],
		}
	}

	conFile := make([]common.ConfigFile, num)
	for i := 0; i < num; i++ {
		conFile[i].Cfg = cfgs[i]
		conFile[i].Peers = peers
		b, _ := json.MarshalIndent(conFile[i], "", "  ")
		ioutil.WriteFile("../conf/node"+strconv.Itoa(i+1)+".json", b, 0777)
	}
}
