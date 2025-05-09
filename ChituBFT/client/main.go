package main

import (
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"chitu/common"
	"time"
)

type Client struct {
	publicAddr  string
	privateAddr string
	interval    int
	reqId       int32
	nodeId      uint32
	startChan   chan struct{}
	infoChan    chan uint32
	zeroNum     uint32
	batch       int
	payload     int

	startTime          []uint64
	consensusLatencies []uint64
	executionLatencies []uint64
	clientLatencies    []uint64
	finishNum          uint64
	allTime            uint64
	blockNum           int
	badCoin            uint32
	exeStates          []int
	prodRound          uint32
}

func main() {
	client := &Client{
		publicAddr:         os.Args[1],
		privateAddr:        os.Args[2],
		reqId:              1,
		startChan:          make(chan struct{}, 1),
		infoChan:           make(chan uint32, 1024),
		startTime:          make([]uint64, 0),
		consensusLatencies: make([]uint64, 0),
		executionLatencies: make([]uint64, 0),
		clientLatencies:    make([]uint64, 0),
		zeroNum:            0,
		finishNum:          0,
		allTime:            0,
		blockNum:           0,
		badCoin:            0,
		exeStates:          make([]int, 0),
		prodRound:          0,
	}
	startRpcServer(client)

	cli, err := rpc.DialHTTP("tcp", client.publicAddr)
	if err != nil {
		panic(err)
	}

	<-client.startChan
	payload := make([]byte, client.payload*client.interval)
	fmt.Printf("once send large: %+v\n", client.payload*client.interval)
	fmt.Printf("start send req\n")
	// client.infoChan <- 1

	for {
		req := &common.ClientReq{
			StartId: client.reqId,
			ReqNum:  int32(client.interval),
			Payload: payload,
		}
		// client.reqId += 1
		client.reqId += int32(client.interval)
		var resp common.ClientResp
		// r := <-client.infoChan
		// req.Round = r
		go cli.Call("Node.Request", req, &resp)
		client.startTime = append(client.startTime, uint64(time.Now().UnixNano()/1000000))
		
		time.Sleep(time.Millisecond * 50) // WAN

	}
}

func startRpcServer(server *Client) {
	rpc.Register(server)
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", server.privateAddr)
	if err != nil {
		panic(err)
	}
	go http.Serve(listener, nil)
}

func (cl *Client) OnStart(msg *common.CoorStart, resp *common.Response) error {
	fmt.Printf("receive coor\n")
	cl.batch = msg.Batch
	cl.payload = msg.Payload
	cl.interval = msg.Interval
	cl.startChan <- struct{}{}
	return nil
}

func (cl *Client) InfoStart(msg *common.InfoStart, resp *common.Response) error {
	cl.infoChan <- msg.Round
	return nil
}

func (cl *Client) NodeFinish(msg *common.NodeBack, resp *common.Response) error {
	if msg.NodeID == 0 {
		if !msg.Steady {
			return nil
		}
		cl.blockNum++                      // all finished block
		cl.finishNum += uint64(msg.ReqNum) // all finished requests
		nowTime := uint64(time.Now().UnixNano() / 1000000)
		thisLatency := uint64(0)
		// cl.allTime += nowTime - cl.startTime[msg.StartID-1] // sum of all finished block latencies
		// thisLatency = nowTime - cl.startTime[msg.StartID-1] // latency of one block
		for i := 0; i < int(msg.ReqNum)/cl.interval; i++ {
			cl.allTime += ((nowTime - cl.startTime[msg.StartID/uint32(cl.interval)+uint32(i)]) * uint64(cl.interval))
			thisLatency += ((nowTime - cl.startTime[msg.StartID/uint32(cl.interval)+uint32(i)]) * uint64(cl.interval))
		}
		// if msg.ReqNum == 0 {
		// 	fmt.Printf("zero ReqNum block\n")
		// } else {
		if msg.ReqNum > 0 {
			cl.clientLatencies = append(cl.clientLatencies, thisLatency/uint64(msg.ReqNum))
		}
	} else {
		cl.nodeId = msg.NodeID
		cl.exeStates = msg.States
		// cl.zeroNum = msg.Zero - uint32(cl.blockNum)
		// cl.badCoin = msg.BadCoin
		cl.prodRound = msg.ReqNum
		cl.Stop(msg.Addr)
	}
	return nil
}

func (cl *Client) Stop(addr string) {
	conn, err := rpc.DialHTTP("tcp", addr)
	if err != nil {
		panic(err)
	}
	st := &common.CoorStatistics{
		// Zero:            cl.zeroNum,
		// States:          cl.exeStates,
		// BadCoin:         cl.badCoin,
		States:          cl.exeStates,
		ConsensusNumber: uint64(cl.blockNum),
		ExecutionNumber: cl.finishNum,
		ID:              uint32(cl.nodeId),
		LatencyMap:      cl.clientLatencies,
		Zero:            cl.prodRound, // produce round num
	}
	if cl.finishNum == 0 {
		st.ExecutionLatency = 0
	} else {
		st.ExecutionLatency = cl.allTime
	}
	var resp common.Response
	conn.Call("Coordinator.Finish", st, &resp)
	fmt.Printf("call back\n")
}
