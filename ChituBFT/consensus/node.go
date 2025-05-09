package consensus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"chitu/common"
	"chitu/crypto"
	"chitu/logger"
	"chitu/network"
	"chitu/utils"

	"net"
	"net/http"
	"net/rpc"
	"time"

	"github.com/gogo/protobuf/proto"
)

type pathState uint8

const (
	INIT pathState = iota
	FASTPATH
	COINPATH
)

type Node struct {
	cfg            *common.Config
	network        network.NetWork
	networkPayList []network.NetWork
	peers          map[uint32]*common.Peer
	logger         logger.Logger
	stop           bool
	identity       bool // true: byzantine; false: normal
	attackSim      bool

	currRound   uint32
	FirstRound  int32
	SecondRound int32

	committedVertex uint32
	// execState map[uint32]executionState // map[round]state
	// nodeState map[uint32]map[uint8]int  // map[round][sender]state; 0: 2f+1 nodes not connect; 1: 2f+1 nodes connect

	// round -> sender -> from -> signature
	certs        map[uint32]map[uint32]*common.QC // certs buffer
	weakCerts    map[uint32]map[uint32]*common.QC
	DAG          map[uint32]map[uint32]*common.Vertex
	vertexBuffer map[uint32]map[uint32]*common.Vertex // Vertices buffer
	hasInitRound map[uint32]bool
	hasGcRound   map[uint32]bool
	//通过fastpath和coinpath 确定状态
	//通过nodeState来确定每个节点decide的状态, all decide, 触发tryCommit
	nodeState map[uint32]map[uint32]uint8 // 0: not vertex; 1: exist a vertex; 2: decide 0; 3: decide 1
	//通过执行来确定每个节点decide的状态
	decideState2 map[uint32]map[uint32]uint8 // nil: not decide; 1 decide: 0, 2: decide 1
	//判断每一轮的path状态
	decidePath2 map[uint32]pathState
	//通过每个节点decide状态确定每一轮decide的状态
	execState       map[uint32]map[uint32]uint8           // 0: not execute; 1 execute
	directSupport   map[uint32]map[uint32]map[uint32]bool // 直接后续顶点连接
	directReject    map[uint32]map[uint32]map[uint32]bool // 直接后续顶点不连接
	inDirectSupport map[uint32]map[uint32]map[uint32]bool // 非直接后续顶点连接
	inDirectReject  map[uint32]map[uint32]map[uint32]bool // 非直接后续顶点不连接

	// 用来提前判定fast 1
	// 虚拟顶点
	valDAG              map[uint32]map[uint32]struct{}
	hasTent             map[uint32]map[uint32]struct{}
	virtualVertexBuffer map[uint32]map[uint32]*common.Vertex // Vertices buffer
	// 直接相连：超过2f+1个连接 fast decide 1
	virtualSupport map[uint32]map[uint32]map[uint32]bool // 虚拟连接
	// 间隔不相连: 超过2f+1个不连接 fast decide 0
	virtualReject map[uint32]map[uint32]map[uint32]bool // 虚拟连接

	tentativeVirtualSupport map[uint32]map[uint32]map[uint32]bool
	tentativeSupport        map[uint32]map[uint32]map[uint32]bool
	tentativeReject         map[uint32]map[uint32]map[uint32]bool

	// weak edge
	weakWait *utils.PriorityQueue
	hasWeak  map[uint32]map[uint32]struct{}
	hasConn  map[uint32]struct{}

	HRBCTerminated map[uint32]map[uint32]struct{}

	// store coin message
	coinMsgs    map[uint32]map[uint32][]byte // 存储Coin信息
	coinsBuffer map[uint32]uint32            // coin leader 暂存
	coins       map[uint32]uint32            // 存储前序所有round的leader都收到的leader
	coinRound   uint32                       // 存储上一个加入coins的leader的round，从2开始

	payloads map[uint32]map[uint32]struct{} // map[round][sender]payload
	myBlocks []string                       // my payload hash
	mergeMsg map[uint32]map[uint32]common.PayloadIds
	sliceNum uint32
	vals     map[uint32]map[uint32]*common.Message
	echos    map[uint32]map[uint32]map[uint32][]byte // round - sender - from - signature
	echoed   map[uint32]map[uint32]map[uint32]struct{}
	selfEcho map[uint32]map[uint32]struct{}
	verified map[uint32]map[uint32]map[uint32]struct{}
	verify   map[uint32]map[uint32]*ValVerify

	msgChan          chan *common.Message
	timeoutChan      chan uint32
	proposeChan      chan *common.BlockInfo
	startChan        chan struct{}
	startProposeChan chan struct{}
	tryProposeChan   chan *common.NewRoundInfo

	// statistics
	startTime          map[uint32]uint64
	executionLatencies map[uint32]uint64
	fastPassStates     map[pathState]int
	badCoin            int
	startSystemTime    uint64

	// client
	currBatch   *common.Batch
	clientChan  chan *common.ClientReq
	cli         *rpc.Client
	reqNum      int
	timeflag    bool
	proposeflag bool
	interval    int //not use
	startId     int32
	blockInfos  map[uint32]*common.BlockInfo
	execNum     int
}

func NewNode(cfg *common.Config, peers map[uint32]*common.Peer, logger logger.Logger, conn uint32, identity bool, attackSim bool) *Node {
	crypto.Init()

	node := &Node{
		cfg:            cfg,
		network:        nil,
		networkPayList: nil,
		peers:          peers,
		logger:         logger,
		stop:           false,
		identity:       identity,
		attackSim:      attackSim,

		currRound:       1,
		FirstRound:      -1,
		SecondRound:     0,
		committedVertex: 0,
		// roundState:     make(map[uint32]uint8),

		certs:           make(map[uint32]map[uint32]*common.QC),
		weakCerts:       make(map[uint32]map[uint32]*common.QC),
		DAG:             make(map[uint32]map[uint32]*common.Vertex),
		hasInitRound:    make(map[uint32]bool),
		hasGcRound:      make(map[uint32]bool),
		nodeState:       make(map[uint32]map[uint32]uint8),
		decideState2:    make(map[uint32]map[uint32]uint8),
		decidePath2:     make(map[uint32]pathState),
		execState:       make(map[uint32]map[uint32]uint8),
		directSupport:   make(map[uint32]map[uint32]map[uint32]bool),
		directReject:    make(map[uint32]map[uint32]map[uint32]bool),
		inDirectSupport: make(map[uint32]map[uint32]map[uint32]bool),
		inDirectReject:  make(map[uint32]map[uint32]map[uint32]bool),

		HRBCTerminated: make(map[uint32]map[uint32]struct{}),

		hasWeak: make(map[uint32]map[uint32]struct{}),
		hasConn: make(map[uint32]struct{}),

		valDAG:         make(map[uint32]map[uint32]struct{}),
		hasTent:        make(map[uint32]map[uint32]struct{}),
		virtualSupport: make(map[uint32]map[uint32]map[uint32]bool),
		virtualReject:  make(map[uint32]map[uint32]map[uint32]bool),

		tentativeVirtualSupport: make(map[uint32]map[uint32]map[uint32]bool),
		tentativeSupport:        make(map[uint32]map[uint32]map[uint32]bool),
		tentativeReject:         make(map[uint32]map[uint32]map[uint32]bool),

		coinMsgs:    make(map[uint32]map[uint32][]byte),
		coinsBuffer: make(map[uint32]uint32),
		coins:       make(map[uint32]uint32),
		coinRound:   2,

		payloads: make(map[uint32]map[uint32]struct{}),
		myBlocks: nil,
		mergeMsg: make(map[uint32]map[uint32]common.PayloadIds),
		sliceNum: conn,
		vals:     make(map[uint32]map[uint32]*common.Message),
		echos:    make(map[uint32]map[uint32]map[uint32][]byte),
		echoed:   make(map[uint32]map[uint32]map[uint32]struct{}),
		selfEcho: make(map[uint32]map[uint32]struct{}),
		verified: make(map[uint32]map[uint32]map[uint32]struct{}),
		verify:   make(map[uint32]map[uint32]*ValVerify),

		msgChan:          make(chan *common.Message, 1000000),
		timeoutChan:      make(chan uint32, 10000),
		proposeChan:      make(chan *common.BlockInfo, 1024),
		startChan:        make(chan struct{}, 1),
		startProposeChan: make(chan struct{}, 1),
		tryProposeChan:   make(chan *common.NewRoundInfo, 1024),

		startTime:          make(map[uint32]uint64),
		executionLatencies: make(map[uint32]uint64),
		fastPassStates:     make(map[pathState]int),
		badCoin:            0,

		currBatch:   new(common.Batch),
		clientChan:  make(chan *common.ClientReq, 10000),
		reqNum:      0,
		timeflag:    true,
		proposeflag: true,
		startId:     1,
		blockInfos:  make(map[uint32]*common.BlockInfo),
		execNum:     0,
	}
	node.vertexBuffer = make(map[uint32]map[uint32]*common.Vertex)
	node.virtualVertexBuffer = make(map[uint32]map[uint32]*common.Vertex)
	node.network = network.NewNoiseNetWork(node.cfg.ID, node.cfg.F, node.cfg.Addr, node.cfg.MaxBatchSize, node.peers, node.msgChan, node.logger, node.cfg.PrivKey, false, 0, node.identity)
	for i := uint32(0); i < conn; i++ {
		node.networkPayList = append(node.networkPayList, network.NewNoiseNetWork(node.cfg.ID, node.cfg.F, node.cfg.Addr, node.cfg.MaxBatchSize, node.peers, node.msgChan, node.logger, node.cfg.PrivKey, true, i+1, node.identity))
	}
	node.weakWait = utils.NewPriorityQueue()
	return node
}

func (n *Node) Run() {
	//time.Sleep(10 * time.Second)
	n.startRpcServer()
	n.network.Start()
	for _, networkPay := range n.networkPayList {
		networkPay.Start()
	}
	go n.proposeLoop()
	n.mainLoop()
}

func (n *Node) startRpcServer() {
	rpc.Register(n)
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", n.cfg.RpcServer)
	if err != nil {
		panic(err)
	}
	go http.Serve(listener, nil)
}

func (n *Node) OnStart(msg *common.CoorStart, resp *common.Response) error {
	n.logger.Infoln("start")
	n.startChan <- struct{}{}
	return nil
}

func (n *Node) Request(req *common.ClientReq, resp *common.ClientResp) error {
	if req.StartId == 1 {
		n.interval = int(req.ReqNum)
		n.logger.Infoln("start")
		n.startChan <- struct{}{}
		n.startProposeChan <- struct{}{}
	}
	// reqBytes, _ := proto.Marshal(req)
	n.clientChan <- req
	return nil
}

func (n *Node) mainLoop() {
	<-n.startChan

	conn, err := rpc.DialHTTP("tcp", n.cfg.ClientServer)
	if err != nil {
		panic(err)
	}
	n.cli = conn
	terminated := false
	n.startSystemTime = uint64(time.Now().UnixNano() / 1000000)
	timer := time.NewTimer(time.Second * time.Duration(10+n.cfg.Time)) // warm-up: 10 sec; steady: test time
	n.logger.Debugf("start mainloop")
	for !terminated {
		select {
		case msg := <-n.msgChan:
			n.handleMessage(msg)
		case blockinfo := <-n.proposeChan:
			n.propose(blockinfo)
		case <-timer.C:
			n.StopClient()
			terminated = true
		}
	}
}

func (n *Node) proposeLoop() {
	<-n.startProposeChan

	timer := time.NewTimer(time.Millisecond * time.Duration(n.cfg.MaxWaitTime))
	// WAN
	for {
		select {
		case <-timer.C:
			if n.proposeflag {
				n.logger.Debugf("time propose")
				n.getBatch(uint32(1))
				n.proposeflag = false
				n.timeflag = false
			} else {
				n.timeflag = true
			}
		case req := <-n.clientChan:
			if !n.identity {
				n.currBatch.Reqs = append(n.currBatch.Reqs, req)
				n.reqNum += n.interval
			}
			// n.getBatch(req)
		case roundInfo := <-n.tryProposeChan:
			// go n.InfoClient(r)
			// if roundInfo.Wait {
			// 	rand.Seed(time.Now().UnixNano())
			// 	time.Sleep(time.Millisecond * time.Duration(rand.Intn(51)))
			// }
			n.getBatch(roundInfo.Round)
			n.proposeflag = false
		}
	}
}

func (n *Node) InfoClient(r uint32) {
	req := &common.InfoStart{
		Round: r,
	}
	var resp common.Response
	err := n.cli.Call("Client.InfoStart", req, &resp)
	if err != nil {
		n.logger.Debugf(err.Error())
	}
}

// func (n *Node) getBatch(req *common.ClientReq) {

// 	reqBytes, _ := proto.Marshal(req)
// 	currBlock := &common.BlockInfo{
// 		StartID: req.StartId,
// 		Round:   req.Round,
// 		ReqNum:  int32(req.ReqNum),
// 		Payload: reqBytes,
// 	}
// 	// n.startId += int32(n.interval)
// 	// n.logger.Debugf("receive client req: %v", req.Round)
// 	n.proposeChan <- currBlock

// }

func (n *Node) getBatch(round uint32) {
	if n.reqNum <= n.cfg.MaxBatchSize {
		payloadBytes, _ := proto.Marshal(n.currBatch)
		n.logger.Debugf("propose req num: %v", n.reqNum)
		n.currBatch.Reset()
		currBlock := &common.BlockInfo{
			StartID: n.startId,
			Round:   round,
			ReqNum:  int32(n.reqNum),
			Payload: payloadBytes,
		}
		n.startId += int32(n.reqNum)
		n.reqNum = 0
		n.proposeChan <- currBlock
	} else {
		reqs := n.currBatch.Reqs[0 : n.cfg.MaxBatchSize/n.interval]
		n.currBatch.Reqs = n.currBatch.Reqs[n.cfg.MaxBatchSize/n.interval:]
		clientreqs := new(common.Batch)
		clientreqs.Reqs = reqs
		payloadBytes, _ := proto.Marshal(clientreqs)
		currBlock := &common.BlockInfo{
			StartID: n.startId,
			Round:   round,
			ReqNum:  int32(n.cfg.MaxBatchSize),
			Payload: payloadBytes,
		}
		n.startId += int32(n.cfg.MaxBatchSize)
		n.reqNum -= n.cfg.MaxBatchSize
		n.proposeChan <- currBlock
		n.logger.Debugf("out of batch propose req num: %v", len(clientreqs.Reqs)*n.interval)
		n.logger.Debugf("remain num: %v", len(n.currBatch.Reqs)*n.interval)
	}
}

func (n *Node) StopClient() {
	var exeStates []int
	exeStates = append(exeStates, n.fastPassStates[FASTPATH])
	exeStates = append(exeStates, n.fastPassStates[COINPATH])
	// exeStates = append(exeStates, n.fastPassStates[ODDFAST])
	// exeStates = append(exeStates, n.fastPassStates[EVENFAST])
	// exeStates = append(exeStates, n.fastPassStates[ODDPENGING])
	// exeStates = append(exeStates, n.fastPassStates[EVENPENGING])
	st := &common.NodeBack{
		NodeID:  n.cfg.ID,
		Addr:    n.cfg.Coordinator,
		States:  exeStates,
		Zero:    uint32(n.execNum), // finidhed block num
		BadCoin: uint32(n.badCoin),
		ReqNum:  n.currRound, // produce round num
	}
	var resp common.Response
	n.logger.Debugln("call back")
	n.cli.Call("Client.NodeFinish", st, &resp)
}

// NewRound之后
func (n *Node) propose(blockinfo *common.BlockInfo) {
	payload := blockinfo.Payload
	round := blockinfo.Round
	n.broadcastPayload(payload, round)

	n.blockInfos[blockinfo.Round] = blockinfo
	proposal := &common.Message{
		Round:  round,
		Sender: n.cfg.ID,
		Type:   common.Message_VAL,
	}
	vertex := &common.Vertex{
		Round:   round,
		Id:      n.cfg.ID,
		Payload: n.myBlocks[len(n.myBlocks)-1],
	}
	if round > 1 {
		vertex.Connections = n.getConnections(round)
		vertex.TentConnect = make([]*common.QC, 0) // n.getTentConnect(round)
		vertex.WeakConnect = n.getWeakConnect(round, len(vertex.Connections)+len(vertex.TentConnect))
	}
	msgBytes, err := proto.Marshal(vertex)
	if err != nil {
		n.logger.Error("marshal bval failed", err)
		return
	}
	proposal.Hash = crypto.Hash(msgBytes)
	proposal.From = n.cfg.ID
	proposal.Vertex = vertex
	n.network.BroadcastMessage(proposal)
	n.logger.Infof("propose val in round %v", round)

}

func (n *Node) broadcastPayload(payload []byte, round uint32) {

	hash := crypto.Hash(payload)
	n.myBlocks = append(n.myBlocks, hash)

	sliceLength := len(payload) / int(n.sliceNum)
	for i := uint32(0); i < n.sliceNum; i++ {
		msgSlice := &common.Message{
			From:            n.cfg.ID,
			Round:           round,
			Sender:          n.cfg.ID,
			Type:            common.Message_PAYLOAD,
			Hash:            hash,
			TotalPayloadNum: n.sliceNum,
			PayloadSlice:    i + 1,
		}
		if i < (n.sliceNum - 1) {
			msgSlice.Payload = payload[i*uint32(sliceLength) : (i+1)*uint32(sliceLength)]
		} else {
			msgSlice.Payload = payload[i*uint32(sliceLength):]
		}
		n.networkPayList[i].BroadcastMessage(msgSlice)
	}

	msg := &common.Message{
		From:    n.cfg.ID,
		Round:   round,
		Sender:  n.cfg.ID,
		Type:    common.Message_PAYLOAD,
		Hash:    hash,
		Payload: payload,
	}
	// n.logger.Debugf("broadcast payload in round %v", round)
	n.startTime[round] = uint64(time.Now().UnixNano() / 1000000)
	_, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	if n.payloads[round] == nil {
		n.payloads[round] = make(map[uint32]struct{})
	}
	n.payloads[round][n.cfg.ID] = struct{}{}
	if _, ok := n.HRBCTerminated[msg.Round][msg.Sender]; ok {
		return
	}
	
	n.handlePayload(msg.Round, msg.Sender)
}

func (n *Node) handleMessage(msg *common.Message) {
	// n.logger.Debugf("receive %v of %v-%v from %v", msg.Type, msg.Round, msg.Sender, msg.From)
	switch msg.Type {
	case common.Message_VAL:
		n.onReceiveVal(msg)
	case common.Message_PAYLOAD:
		n.onReceivePayload(msg)
	case common.Message_ECHO:
		n.onReceiveEcho(msg)
	case common.Message_READY:
		n.onReceiveReady(msg)
	case common.Message_DELIVER:
		n.onReceiveDeliver(msg)
	case common.Message_COIN:
		n.onReceiveCoin(msg)
	case common.Message_SYNC:
		n.onReceiveSync(msg)
	default:
		n.logger.Error("invalid msg type", errors.New("bug in msg dispatch"))
	}
}

func (n *Node) handlePayload(round uint32, sender uint32) {
	if _, ok1 := n.valDAG[round][sender]; ok1 {
		if _, ok2 := n.selfEcho[round][sender]; !ok2 {
			if n.selfEcho[round] == nil {
				n.selfEcho[round] = make(map[uint32]struct{})
			}
			n.selfEcho[round][sender] = struct{}{}
			echoMsg := &common.Message{
				Round:  round,
				Sender: sender,
				Type:   common.Message_ECHO,
				Hash:   n.vals[round][sender].Hash,
			}
			
			echoMsg.From = n.cfg.ID
			n.network.BroadcastMessage(echoMsg)
			if n.echoed[round] == nil {
				n.echoed[round] = make(map[uint32]map[uint32]struct{})
			}
			if n.echoed[round][sender] == nil {
				n.echoed[round][sender] = make(map[uint32]struct{})
			}
			if _, ok3 := n.hasTent[round][sender]; !ok3 && len(n.echoed[round][sender]) >= int(n.cfg.F) {
				if n.hasTent[round] == nil {
					n.hasTent[round] = make(map[uint32]struct{})
				}
				n.hasTent[round][sender] = struct{}{}
				tmpQC := &common.QC{
					Id:    sender,
					Round: round,
					Hash:  n.vals[round][sender].Hash,
					Type:  0,
				}
				sigs := make([]*common.Signature, 0)
				for from, signature := range n.echos[round][sender] {
					sig := &common.Signature{
						Id:  from,
						Sig: signature,
					}
					sigs = append(sigs, sig)
				}
				tmpMsg := &common.Message{
					Round:  echoMsg.Round,
					Sender: echoMsg.Sender,
					Type:   echoMsg.Type,
					Hash:   echoMsg.Hash,
				}
				content, err := proto.Marshal(tmpMsg)
				if err != nil {
					panic(err)
				}
				hash := crypto.Hash(content)
				selfSignature := crypto.Sign(n.cfg.PrivKey, []byte(hash))
				selfSig := &common.Signature{
					Id:  n.cfg.ID,
					Sig: selfSignature,
				}
				sigs = append(sigs, selfSig)
				tmpQC.Sigs = sigs
				if n.weakCerts[round] == nil {
					n.weakCerts[round] = make(map[uint32]*common.QC)
				}
				n.weakCerts[round][sender] = tmpQC
				n.tryBroadcastSync(round, sender)
			}
		}

		if n.echos[round] == nil {
			n.echos[round] = make(map[uint32]map[uint32][]byte)
		}
		if n.echos[round][sender] == nil {
			n.echos[round][sender] = make(map[uint32][]byte)
		}
		if _, ok3 := n.HRBCTerminated[round][sender]; !ok3 {
			if len(n.echos[round][sender]) >= int(n.cfg.N-n.cfg.F) {
				if n.HRBCTerminated[round] == nil {
					n.HRBCTerminated[round] = make(map[uint32]struct{})
				}
				n.HRBCTerminated[round][sender] = struct{}{}
				msg := n.vals[round][sender]
				msg.Qc = &common.QC{
					Id:    sender,
					Round: round,
					Hash:  msg.Hash,
					Type:  0,
				}
				sigs := make([]*common.Signature, 0)
				for from, signature := range n.echos[round][sender] {
					sig := &common.Signature{
						Id:  from,
						Sig: signature,
					}
					sigs = append(sigs, sig)
				}
				msg.Qc.Sigs = sigs
				n.onReceiveDeliver(msg)
			}
		}
	}
}

func (n *Node) onReceivePayload(msgSlice *common.Message) {
	if n.mergeMsg[msgSlice.Round] == nil {
		n.mergeMsg[msgSlice.Round] = make(map[uint32]common.PayloadIds)
	}
	n.mergeMsg[msgSlice.Round][msgSlice.From] = append(n.mergeMsg[msgSlice.Round][msgSlice.From], common.PayloadId{Id: msgSlice.PayloadSlice, Payload: msgSlice.Payload})
	if len(n.mergeMsg[msgSlice.Round][msgSlice.From]) == int(n.sliceNum) {
		sort.Sort(n.mergeMsg[msgSlice.Round][msgSlice.From])
		var buffer bytes.Buffer
		for _, ps := range n.mergeMsg[msgSlice.Round][msgSlice.From] {
			buffer.Write(ps.Payload)
		}
		msg := &common.Message{
			From:    msgSlice.From,
			Round:   msgSlice.Round,
			Sender:  msgSlice.Sender,
			Type:    msgSlice.Type,
			Hash:    msgSlice.Hash,
			Payload: buffer.Bytes(),
		}
		// n.logger.Debugf("receive all payload from %v in round %v", msg.Sender, msg.Round)

		if _, ok := n.payloads[msg.Round][msg.Sender]; ok {
			return
		}
		_, err := proto.Marshal(msg)
		if err != nil {
			panic(err)
		}
		if n.payloads[msg.Round] == nil {
			n.payloads[msg.Round] = make(map[uint32]struct{})
		}
		delete(n.mergeMsg[msgSlice.Round], msgSlice.From)
		n.payloads[msg.Round][msg.Sender] = struct{}{}
		if _, ok := n.HRBCTerminated[msg.Round][msg.Sender]; ok {
			return
		}
		
		n.handlePayload(msg.Round, msg.Sender)
	}
}

func (n *Node) handleVal(msg *common.Message) {
	if _, ok := n.vals[msg.Round][msg.Sender]; ok {
		return
	}
	if n.vals[msg.Round] == nil {
		n.vals[msg.Round] = make(map[uint32]*common.Message)
	}
	n.vals[msg.Round][msg.Sender] = msg
	n.onReceiveReady(msg)

	if n.echos[msg.Round] == nil {
		n.echos[msg.Round] = make(map[uint32]map[uint32][]byte)
	}
	if n.echos[msg.Round][msg.Sender] == nil {
		n.echos[msg.Round][msg.Sender] = make(map[uint32][]byte)
	}
	if _, ok := n.HRBCTerminated[msg.Round][msg.Sender]; !ok {
		if len(n.echos[msg.Round][msg.Sender]) >= int(n.cfg.N-n.cfg.F) {
			if n.HRBCTerminated[msg.Round] == nil {
				n.HRBCTerminated[msg.Round] = make(map[uint32]struct{})
			}
			n.HRBCTerminated[msg.Round][msg.Sender] = struct{}{}
			msg.Qc = &common.QC{
				Id:    msg.Sender,
				Round: msg.Round,
				Hash:  msg.Hash,
				Type:  0,
			}
			sigs := make([]*common.Signature, 0)
			for from, signature := range n.echos[msg.Round][msg.Sender] {
				sig := &common.Signature{
					Id:  from,
					Sig: signature,
				}
				sigs = append(sigs, sig)
			}
			msg.Qc.Sigs = sigs
			n.onReceiveDeliver(msg)
		}
	}
}

func (n *Node) onReceiveVal(msg *common.Message) {
	if _, ok := n.HRBCTerminated[msg.Round][msg.Sender]; ok {
		return
	}

	// if msg.Round > 1 {
	// 	sigNum := 0
	// 	for _, qc := range append(msg.Vertex.Connections, msg.Vertex.TentConnect...) {
	// 		sigNum += len(qc.Sigs)
	// 	}
	// 	resChan := make(chan bool, sigNum)
	// 	for _, qc := range append(msg.Vertex.Connections, msg.Vertex.TentConnect...) {
	// 		if _, ok := n.certs[qc.Round][qc.Id]; ok {
	// 			for i := 0; i < len(qc.Sigs); i++ {
	// 				resChan <- true
	// 			}
	// 		} else {
	// 			toVerify := &common.Message{
	// 				Round:  qc.Round,
	// 				Sender: qc.Id,
	// 				Type:   common.Message_ECHO,
	// 				Hash:   qc.Hash,
	// 			}
	// 			content, err := proto.Marshal(toVerify)
	// 			if err != nil {
	// 				panic(err)
	// 			}
	// 			hash := crypto.Hash(content)
	// 			hashByte := []byte(hash)
	// 			for _, sig := range qc.Sigs {
	// 				go func(round uint32, sender uint32, from uint32, signature []byte) {
	// 					if _, ok := n.echoed[round][sender][from]; ok {
	// 						resChan <- true
	// 					} else if crypto.Verify(n.peers[from].PublicKey, hashByte, signature) {
	// 						resChan <- true
	// 					} else {
	// 						resChan <- false
	// 					}
	// 				}(qc.Round, qc.Id, sig.Id, sig.Sig)
	// 			}
	// 		}
	// 	}
	// 	for i := 0; i < sigNum; i++ {
	// 		res := <-resChan
	// 		if !res {
	// 			n.logger.Debugf("invalid signature in val qc")
	// 		}
	// 	}
	// }
	if msg.Round > 1 {
		if _, ok := n.verify[msg.Round][msg.Sender]; !ok {
			sigNum := 0
			for _, qc := range append(msg.Vertex.Connections, msg.Vertex.TentConnect...) {
				for _, sig := range qc.Sigs {
					if _, ok := n.echoed[qc.Round][qc.Id][sig.Id]; !ok {
						if _, ok := n.verified[qc.Round][qc.Id][sig.Id]; !ok {
							sigNum++
						}
					}
				}
			}
			if n.verify[msg.Round] == nil {
				n.verify[msg.Round] = make(map[uint32]*ValVerify)
			}
			n.verify[msg.Round][msg.Sender] = NewValVerify(n.peers, msg, msg.Round-1, sigNum, n.msgChan, n.logger)
			for _, qc := range append(msg.Vertex.Connections, msg.Vertex.TentConnect...) {
				for _, sig := range qc.Sigs {
					if _, ok := n.echoed[qc.Round][qc.Id][sig.Id]; !ok {
						if _, ok := n.verified[qc.Round][qc.Id][sig.Id]; !ok {
							signature := &common.SigToVerify{
								Sender:    qc.Id,
								Hash:      qc.Hash,
								From:      sig.Id,
								Signature: sig.Sig,
							}
							n.verify[msg.Round][msg.Sender].sigChan <- signature
							if n.verified[qc.Round] == nil {
								n.verified[qc.Round] = make(map[uint32]map[uint32]struct{})
							}
							if n.verified[qc.Round][qc.Id] == nil {
								n.verified[qc.Round][qc.Id] = make(map[uint32]struct{})
							}
							n.verified[qc.Round][qc.Id][sig.Id] = struct{}{}
						}
					}
				}
			}
			return
		} else {
			delete(n.verify[msg.Round], msg.Sender)
		}
	}
	if msg.Round > 1 {
		// n.logger.Debugf("receive val %v-%v from %v", msg.Round, msg.Sender, msg.From)
		for _, qc := range msg.Vertex.Connections {
			// n.logger.Debugf("add qc %v-%v", qc.Round, qc.Id)
			if !n.addQC(qc) {
				n.logger.Debugln("invalid qc")
				return
			}
			if _, ok1 := n.DAG[qc.Round][qc.Id]; !ok1 {
				if _, ok2 := n.vals[qc.Round][qc.Id]; ok2 {
					n.tryAddDAG(n.vals[qc.Round][qc.Id].Vertex)
				} else {
					tmpVertex := &common.Vertex{
						Round: qc.Round,
						Id:    qc.Id,
					}
					if n.virtualVertexBuffer[qc.Round] == nil {
						n.virtualVertexBuffer[qc.Round] = make(map[uint32]*common.Vertex)
					}
					n.virtualVertexBuffer[qc.Round][qc.Id] = tmpVertex
				}
			}
		}
	}

	n.handleVal(msg)

}

func (n *Node) handleEcho(msg *common.Message) {
	if _, ok1 := n.HRBCTerminated[msg.Round][msg.Sender]; !ok1 {
		if _, ok3 := n.vals[msg.Round][msg.Sender]; ok3 {
			if len(n.echos[msg.Round][msg.Sender]) >= int(n.cfg.N-n.cfg.F) {
				if n.HRBCTerminated[msg.Round] == nil {
					n.HRBCTerminated[msg.Round] = make(map[uint32]struct{})
				}
				n.HRBCTerminated[msg.Round][msg.Sender] = struct{}{}
				deliverMsg := n.vals[msg.Round][msg.Sender]
				deliverMsg.Qc = &common.QC{
					Id:    deliverMsg.Sender,
					Round: deliverMsg.Round,
					Hash:  deliverMsg.Hash,
					Type:  0,
				}
				sigs := make([]*common.Signature, 0)
				for from, signature := range n.echos[msg.Round][msg.Sender] {
					sig := &common.Signature{
						Id:  from,
						Sig: signature,
					}
					sigs = append(sigs, sig)
				}
				deliverMsg.Qc.Sigs = sigs
				n.onReceiveDeliver(deliverMsg)
			}
		}
	}
}

func (n *Node) onReceiveEcho(msg *common.Message) {
	if _, ok := n.echoed[msg.Round][msg.Sender][msg.From]; ok {
		return
	}
	if n.echos[msg.Round] == nil {
		n.echos[msg.Round] = make(map[uint32]map[uint32][]byte)
	}
	if n.echos[msg.Round][msg.Sender] == nil {
		n.echos[msg.Round][msg.Sender] = make(map[uint32][]byte)
	}
	n.echos[msg.Round][msg.Sender][msg.From] = msg.Signature
	if n.echoed[msg.Round] == nil {
		n.echoed[msg.Round] = make(map[uint32]map[uint32]struct{})
	}
	if n.echoed[msg.Round][msg.Sender] == nil {
		n.echoed[msg.Round][msg.Sender] = make(map[uint32]struct{})
	}
	n.echoed[msg.Round][msg.Sender][msg.From] = struct{}{}
	if _, ok1 := n.hasTent[msg.Round][msg.Sender]; !ok1 && len(n.echoed[msg.Round][msg.Sender]) >= int(n.cfg.F+1) {
		if _, ok2 := n.vals[msg.Round][msg.Sender]; ok2 {
			if n.hasTent[msg.Round] == nil {
				n.hasTent[msg.Round] = make(map[uint32]struct{})
			}
			n.hasTent[msg.Round][msg.Sender] = struct{}{}
			tmpQC := &common.QC{
				Id:    msg.Sender,
				Round: msg.Round,
				Hash:  n.vals[msg.Round][msg.Sender].Hash,
				Type:  0,
			}
			sigs := make([]*common.Signature, 0)
			for from, signature := range n.echos[msg.Round][msg.Sender] {
				sig := &common.Signature{
					Id:  from,
					Sig: signature,
				}
				sigs = append(sigs, sig)
			}
			tmpQC.Sigs = sigs
			if n.weakCerts[msg.Round] == nil {
				n.weakCerts[msg.Round] = make(map[uint32]*common.QC)
			}
			n.weakCerts[msg.Round][msg.Sender] = tmpQC
			n.tryBroadcastSync(msg.Round, msg.Sender)
		}
	}
	if len(n.echoed[msg.Round][msg.Sender]) >= int(n.cfg.F+1) {
		if _, ok := n.selfEcho[msg.Round][msg.Sender]; !ok {
			if n.selfEcho[msg.Round] == nil {
				n.selfEcho[msg.Round] = make(map[uint32]struct{})
			}
			n.selfEcho[msg.Round][msg.Sender] = struct{}{}
			echoMsg := &common.Message{
				Round:  msg.Round,
				Sender: msg.Sender,
				Type:   common.Message_ECHO,
				Hash:   msg.Hash,
			}
			echoMsg.From = n.cfg.ID
			n.network.BroadcastMessage(echoMsg)
		}
	}
	if _, ok := n.HRBCTerminated[msg.Round][msg.Sender]; ok {
		if _, ok2 := n.echos[msg.Round][msg.Sender][n.cfg.ID]; ok2 {
			delete(n.vals[msg.Round], msg.Sender)
			delete(n.echos[msg.Round], msg.Sender)
		}
		return
	}

	n.handleEcho(msg)
}

func (n *Node) onReceiveReady(msg *common.Message) {
	// preNode := make([]*common.QC, 0)
	// for _, connection := range msg.Vertex.Connections {
	// 	q := &common.QC{
	// 		Id:    connection.Id,
	// 		Round: connection.Round,
	// 	}
	// 	preNode = append(preNode, q)
	// }
	// n.logger.Debugf("tryAddVirtualDAG %v-%v %v:%v", msg.Vertex.Round, msg.Sender, len(msg.Vertex.Connections), preNode)
	n.tryAddVirtualDAG(msg.Vertex)
	n.tryCommit()
}

func (n *Node) onReceiveDeliver(msg *common.Message) {

	if n.certs[msg.Round] == nil {
		n.certs[msg.Round] = make(map[uint32]*common.QC)
	}
	n.certs[msg.Round][msg.Sender] = msg.Qc

	// 打印连接的点
	// preNode := make([]*common.QC, 0)
	// for _, connection := range msg.Vertex.Connections {
	// 	q := &common.QC{
	// 		Id:    connection.Id,
	// 		Round: connection.Round,
	// 	}
	// 	preNode = append(preNode, q)
	// }
	// n.logger.Debugf("tryAddDAG %v-%v %v:%v", msg.Vertex.Round, msg.Sender, len(msg.Vertex.Connections), preNode)

	if (msg.Vertex.Round < n.currRound) || (msg.Vertex.Round == n.currRound && msg.Vertex.Id == n.cfg.ID) {
		if _, ok := n.hasWeak[msg.Vertex.Round][msg.Vertex.Id]; !ok {
			n.weakWait.Push(msg.Vertex)
			if n.hasWeak[msg.Vertex.Round] == nil {
				n.hasWeak[msg.Vertex.Round] = make(map[uint32]struct{})
			}
			n.hasWeak[msg.Vertex.Round][msg.Vertex.Id] = struct{}{}
		}
	}

	n.tryAddDAG(msg.Vertex)

	if _, ok := n.echos[msg.Round][msg.Sender][n.cfg.ID]; ok {
		delete(n.vals[msg.Round], msg.Sender)
		delete(n.echos[msg.Round], msg.Sender)
	}
}

func (n *Node) onReceiveCoin(msg *common.Message) {
	if n.coinMsgs[msg.Round] == nil {
		n.coinMsgs[msg.Round] = make(map[uint32][]byte)
	}
	if _, ok := n.coinMsgs[msg.Round][msg.From]; ok {
		return
	}
	if _, ok := n.coinsBuffer[msg.Round]; ok {
		return
	}
	n.coinMsgs[msg.Round][msg.From] = msg.Payload

	if len(n.coinMsgs[msg.Round]) == int(n.cfg.N-n.cfg.F) {
		coinBytes := crypto.Recover(n.coinMsgs[msg.Round])
		delete(n.coinMsgs, msg.Round)
		leader := binary.LittleEndian.Uint32(coinBytes)%uint32(n.cfg.N) + 1
		n.coinsBuffer[msg.Round] = leader
		for {
			if _, ok := n.coinsBuffer[n.coinRound+1]; !ok {
				return
			}
			n.coinRound = n.coinRound + 1
			// n.logger.Debugf("add leader %v in round %v", n.coinsBuffer[n.coinRound], n.coinRound)
			n.coins[n.coinRound] = n.coinsBuffer[n.coinRound]
			// coin leader valid
			var currLeaderRound uint32
			if n.coinRound%2 == 1 {
				currLeaderRound = uint32(n.FirstRound + 4)
			} else {
				currLeaderRound = uint32(n.SecondRound + 4)
			}
			if currLeaderRound <= n.coinRound {
				n.tryCoinPass(n.coinRound)
			}
		}
	}
}

func (n *Node) onReceiveSync(msg *common.Message) {
	qc := msg.Qc
	for _, sig := range qc.Sigs {
		if _, ok := n.echoed[qc.Round][qc.Id][sig.Id]; !ok {
			echoMsg := &common.Message{
				Round:  qc.Round,
				Sender: qc.Id,
				Type:   common.Message_ECHO,
				Hash:   qc.Hash,
			}
			echoMsg.From = sig.Id
			echoMsg.Signature = sig.Sig
			if _, ok := n.verified[qc.Round][qc.Id][sig.Id]; !ok {
				if n.verified[qc.Round] == nil {
					n.verified[qc.Round] = make(map[uint32]map[uint32]struct{})
				}
				if n.verified[qc.Round][qc.Id] == nil {
					n.verified[qc.Round][qc.Id] = make(map[uint32]struct{})
				}
				n.verified[qc.Round][qc.Id][sig.Id] = struct{}{}
				go n.verifyEcho(echoMsg)
			} else {
				n.msgChan <- echoMsg
			}
		}
	}
}

func (n *Node) newRound(round uint32, wait bool) {
	if n.currRound >= round {
		return
	}
	n.currRound = round
	n.logger.Debugf("start new round %v", n.currRound)
	roundInfo := &common.NewRoundInfo{
		Round: round,
		Wait:  wait,
	}
	n.tryProposeChan <- roundInfo
}

func (n *Node) initRoundState(round uint32) {
	if round <= 0 {
		return
	}
	if _, ok := n.hasInitRound[round]; ok {
		return
	}
	// n.logger.Debugf("initRound:%v", round)
	n.hasInitRound[round] = true
	if n.DAG[round] == nil {
		n.DAG[round] = make(map[uint32]*common.Vertex)
	}
	if n.certs[round] == nil {
		n.certs[round] = make(map[uint32]*common.QC)
	}
	if n.nodeState[round] == nil {
		n.nodeState[round] = make(map[uint32]uint8)
	}
	if n.decideState2[round] == nil {
		n.decideState2[round] = make(map[uint32]uint8)
	}
	if n.execState[round] == nil {
		n.execState[round] = make(map[uint32]uint8)
	}
	if n.directSupport[round] == nil {
		n.directSupport[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.directSupport[round][i] == nil {
				n.directSupport[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.directReject[round] == nil {
		n.directReject[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.directReject[round][i] == nil {
				n.directReject[round][i] = make(map[uint32]bool)
			}
		}
	}

	if n.inDirectSupport[round] == nil {
		n.inDirectSupport[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.inDirectSupport[round][i] == nil {
				n.inDirectSupport[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.inDirectReject[round] == nil {
		n.inDirectReject[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.inDirectReject[round][i] == nil {
				n.inDirectReject[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.valDAG[round] == nil {
		n.valDAG[round] = make(map[uint32]struct{})
	}
	if n.virtualSupport[round] == nil {
		n.virtualSupport[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.virtualSupport[round][i] == nil {
				n.virtualSupport[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.virtualReject[round] == nil {
		n.virtualReject[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.virtualReject[round][i] == nil {
				n.virtualReject[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.tentativeVirtualSupport[round] == nil {
		n.tentativeVirtualSupport[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.tentativeVirtualSupport[round][i] == nil {
				n.tentativeVirtualSupport[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.tentativeSupport[round] == nil {
		n.tentativeSupport[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.tentativeSupport[round][i] == nil {
				n.tentativeSupport[round][i] = make(map[uint32]bool)
			}
		}
	}
	if n.tentativeReject[round] == nil {
		n.tentativeReject[round] = make(map[uint32]map[uint32]bool)
		for i := uint32(1); i <= n.cfg.N; i++ {
			if n.tentativeReject[round][i] == nil {
				n.tentativeReject[round][i] = make(map[uint32]bool)
			}
		}
	}

}
func (n *Node) getConnections(round uint32) []*common.QC {
	retval := make([]*common.QC, 0)
	if round <= 1 {
		// n.logger.Debugln("GetConnections's round less than 1")
		return retval
	}
	n.initRoundState(round - 1)
	for _, v := range n.DAG[round-1] {
		retval = append(retval, n.certs[round-1][v.Id])
		if v.Id == n.cfg.ID {
			n.hasConn[round-1] = struct{}{}
		}
	}
	if len(retval) < int(n.cfg.N-n.cfg.F) {
		n.logger.Debugln("GetConnections's round - 1's vertices less than f + 1")
		return retval
	}
	return retval

}

func (n *Node) getTentConnect(round uint32) []*common.QC {
	retval := make([]*common.QC, 0)
	if round <= 1 {
		return retval
	}
	for _, qc := range n.weakCerts[round-1] {
		if _, ok := n.certs[round-1][qc.Id]; ok {
			continue
		}
		retval = append(retval, qc)
		if qc.Id == n.cfg.ID {
			n.hasConn[round-1] = struct{}{}
		}
	}
	delete(n.weakCerts, round-1)
	return retval
}

func (n *Node) getWeakConnect(round uint32, connectLen int) []*common.QC {
	retval := make([]*common.QC, 0)
	if round <= 1 {
		return retval
	}
	for i := uint32(connectLen); i < n.cfg.N && n.weakWait.Len() != 0; {
		v := n.weakWait.Pop().(*common.Vertex)
		if v.Id == n.cfg.ID {
			if _, ok := n.hasConn[v.Round]; ok {
				continue
			}
		}
		if _, ok := n.certs[v.Round][v.Id]; ok {
			retval = append(retval, n.certs[v.Round][v.Id])
			i++
		}
	}
	return retval
}

func (n *Node) addQC(qc *common.QC) bool {
	if n.certs[qc.Round] == nil {
		n.certs[qc.Round] = make(map[uint32]*common.QC)
	}
	if _, ok := n.certs[qc.Round][qc.Id]; ok {
		return true
	}
	n.certs[qc.Round][qc.Id] = qc
	return true
}

func (n *Node) tryAddDAG(vertex *common.Vertex) {
	if n.DAG[vertex.Round] == nil {
		n.DAG[vertex.Round] = make(map[uint32]*common.Vertex)
	}
	if _, ok := n.DAG[vertex.Round][vertex.Id]; ok {
		return
	}
	//可以提前添加边的信息
	if n.vertexBuffer[vertex.Round] == nil {
		n.vertexBuffer[vertex.Round] = make(map[uint32]*common.Vertex)
	}
	if _, ok := n.valDAG[vertex.Round][vertex.Id]; !ok {
		n.vertexBuffer[vertex.Round][vertex.Id] = vertex
		return
	}
	if n.virtualVertexBuffer[vertex.Round] == nil {
		n.virtualVertexBuffer[vertex.Round] = make(map[uint32]*common.Vertex)
	}
	// n.logger.Debugf("add %v-%v success!", vertex.Round, vertex.Id)
	dagVertex := &common.Vertex{
		Round:       vertex.Round,
		Id:          vertex.Id,
		Connections: make([]*common.QC, 0),
		TentConnect: make([]*common.QC, 0),
		WeakConnect: make([]*common.QC, 0),
	}
	for _, qc := range vertex.Connections {
		dagQC := &common.QC{
			Round: qc.Round,
			Id:    qc.Id,
		}
		dagVertex.Connections = append(dagVertex.Connections, dagQC)
	}
	for _, qc := range vertex.TentConnect {
		dagQC := &common.QC{
			Round: qc.Round,
			Id:    qc.Id,
		}
		dagVertex.TentConnect = append(dagVertex.TentConnect, dagQC)
	}
	for _, qc := range vertex.WeakConnect {
		dagQC := &common.QC{
			Round: qc.Round,
			Id:    qc.Id,
		}
		dagVertex.WeakConnect = append(dagVertex.WeakConnect, dagQC)
	}
	n.DAG[vertex.Round][vertex.Id] = dagVertex
	if n.weakCerts[vertex.Round] == nil {
		n.weakCerts[vertex.Round] = make(map[uint32]*common.QC)
	}
	delete(n.weakCerts[vertex.Round], vertex.Id)
	delete(n.vertexBuffer[vertex.Round], vertex.Id)
	delete(n.virtualVertexBuffer[vertex.Round], vertex.Id)
	n.addConn(vertex)

	// if len(n.DAG[vertex.Round]) == int(n.cfg.N-n.cfg.F) {	// without adaptive wait
	if (!n.attackSim && len(n.DAG[vertex.Round]) >= int(n.cfg.N-n.cfg.F) && len(n.weakCerts[vertex.Round]) == 0) ||
		(n.attackSim && len(n.DAG[vertex.Round]) == int(n.cfg.N)) {		// under attack simulation
		wait := false
		// if d, ok := n.decideState2[vertex.Round-1][n.cfg.ID]; ok {
		// 	if d == 2 && len(n.decideState2[vertex.Round-1]) < int(n.cfg.N) {
		// 		wait = true
		// 	}
		// }
		n.newRound(vertex.Round+1, wait)
	}
	if len(n.DAG[vertex.Round]) == int(n.cfg.N-n.cfg.F) {
		n.getCoin(vertex.Round - 1)
	}
	// if n.vertexBuffer[vertex.Round+1] != nil {
	// 	for _, v := range n.vertexBuffer[vertex.Round+1] {
	// 		n.tryAddDAG(v)
	// 	}
	// }
}

// tryAddDAG调用
// 功能：更新边的状态，从而触发快速执行，或者用来触发coin path
func (n *Node) addConn(vertex *common.Vertex) {
	n.addDirectConn(vertex)
	n.addInDirectConn(vertex)
}
func (n *Node) addDirectConn(vertex *common.Vertex) {
	if vertex.Round <= 1 {
		return
	}
	n.initRoundState(vertex.Round)
	n.initRoundState(vertex.Round - 1)
	// 从Round = 2 才进行判断
	if vertex.Round >= 2 {
		// 直接相连
		// connection.Round + 1 = vertex.Round
		for _, connection := range vertex.Connections {
			// n.logger.Debugf("add directSupport: %v %v %v %v %v", connection.Round, connection.Id, vertex.Round, vertex.Id, n.directSupport[connection.Round][connection.Id] == nil)
			n.directSupport[connection.Round][connection.Id][vertex.Id] = true
			// if len(n.directSupport[connection.Round][connection.Id]) >= int(n.cfg.F+1) {
			// 	// 没有decide,decide 1
			// 	if n.decideState[connection.Round] == nil {
			// 		n.decideState[connection.Round] = make(map[uint32]uint8)
			// 	}
			// 	if _, ok := n.decideState[connection.Round][connection.Id]; !ok {
			// 		n.decideState[connection.Round][connection.Id] = 2
			// 		if n.decidePath[connection.Round] < F1R1D1 {
			// 			n.decidePath[connection.Round] = F1R1D1
			// 		}
			// 		n.logger.Debugf("fast 1 in round + 1: %v-%v", connection.Round, connection.Id)
			// 	}
			// }
			n.tentativeSupport[connection.Round][connection.Id][vertex.Id] = true
			if !n.attackSim {
				if len(n.tentativeSupport[connection.Round][connection.Id]) >= int(n.cfg.N-n.cfg.F) {
					if n.decideState2[connection.Round] == nil {
						n.decideState2[connection.Round] = make(map[uint32]uint8)
					}
					if _, ok := n.decideState2[connection.Round][connection.Id]; !ok {
						n.decideState2[connection.Round][connection.Id] = 2
						if n.decidePath2[connection.Round] < FASTPATH {
							n.decidePath2[connection.Round] = FASTPATH
						}
						n.logger.Debugf("fast 1: %v-%v", connection.Round, connection.Id)
					}
				}
			}
		}
		for _, tentconn := range vertex.TentConnect {
			n.tentativeSupport[tentconn.Round][tentconn.Id][vertex.Id] = true
			if !n.attackSim {
				if len(n.tentativeSupport[tentconn.Round][tentconn.Id]) >= int(n.cfg.N-n.cfg.F) {
					if n.decideState2[tentconn.Round] == nil {
						n.decideState2[tentconn.Round] = make(map[uint32]uint8)
					}
					if _, ok := n.decideState2[tentconn.Round][tentconn.Id]; !ok {
						n.decideState2[tentconn.Round][tentconn.Id] = 2
						if n.decidePath2[tentconn.Round] < FASTPATH {
							n.decidePath2[tentconn.Round] = FASTPATH
						}
						n.logger.Debugf("fast 1: %v-%v", tentconn.Round, tentconn.Id)
					}
				}
			}
		}
		for i := uint32(1); i <= n.cfg.N; i++ {
			if _, ok := n.directSupport[vertex.Round-1][i][vertex.Id]; !ok {
				n.directReject[vertex.Round-1][i][vertex.Id] = true
			}
			if _, ok := n.tentativeSupport[vertex.Round-1][i][vertex.Id]; !ok {
				n.tentativeReject[vertex.Round-1][i][vertex.Id] = true
				if !n.attackSim {
					if len(n.tentativeReject[vertex.Round-1][i]) >= int(n.cfg.N-n.cfg.F) {
						if n.decideState2[vertex.Round-1] == nil {
							n.decideState2[vertex.Round-1] = make(map[uint32]uint8)
						}
						if _, ok2 := n.decideState2[vertex.Round-1][i]; !ok2 {
							n.decideState2[vertex.Round-1][i] = 1
							if n.decidePath2[vertex.Round-1] < FASTPATH {
								n.decidePath2[vertex.Round-1] = FASTPATH
							}
							n.logger.Debugf("fast 0: %v-%v", vertex.Round-1, i)
						}
					}
				}
			}
		}
		n.tryCommit()
	}
}

// 功能：更新边的状态，从而触发快速执行，或者用来触发comm
func (n *Node) addInDirectConn(vertex *common.Vertex) {
	if vertex.Round <= 2 {
		return
	}
	// n.initRoundState(vertex.Round)
	// n.initRoundState(vertex.Round - 2)

	// 从Round = 3 才进行判断
	// if vertex.Round >= 3 {
	// 	preNodes := make(map[uint32]bool)
	// 	for _, connection := range vertex.Connections {
	// 		preNodes[connection.Id] = true
	// 	}
	// 	for id := range preNodes {
	// 		for _, conn := range n.DAG[vertex.Round-1][id].Connections {
	// 			n.inDirectSupport[vertex.Round-2][conn.Id][vertex.Id] = true
	// 			if len(n.inDirectSupport[vertex.Round-2][conn.Id]) == int(n.cfg.N) {
	// 				if n.decideState[vertex.Round-2] == nil {
	// 					n.decideState[vertex.Round-2] = make(map[uint32]uint8)
	// 				}
	// 				if _, ok := n.decideState[vertex.Round-2][conn.Id]; !ok {
	// 					n.decideState[vertex.Round-2][conn.Id] = 2
	// 					if n.decidePath[vertex.Round-2] < F1R2D1 {
	// 						n.decidePath[vertex.Round-2] = F1R2D1
	// 					}
	// 					n.logger.Debugf("fast 1 in round + 2: %v-%v", vertex.Round-2, conn.Id)
	// 				}
	// 			}
	// 		}
	// 	}

	// 	for i := uint32(1); i <= n.cfg.N; i++ {
	// 		if _, ok := n.inDirectSupport[vertex.Round-2][i][vertex.Id]; !ok {
	// 			n.inDirectReject[vertex.Round-2][i][vertex.Id] = true
	// 		}
	// 		if len(n.inDirectReject[vertex.Round-2][i]) >= int(n.cfg.F+1) {
	// 			if n.decideState[vertex.Round-2] == nil {
	// 				n.decideState[vertex.Round-2] = make(map[uint32]uint8)
	// 			}
	// 			if _, ok := n.decideState[vertex.Round-2][i]; !ok {
	// 				n.decideState[vertex.Round-2][i] = 1
	// 				if n.decidePath[vertex.Round-2] < F1R2D0 {
	// 					n.decidePath[vertex.Round-2] = F1R2D0
	// 				}
	// 				n.logger.Debugf("fast 0 in round + 2: %v-%v", vertex.Round-2, i)

	// 			}
	// 		}
	// 	}
	// 	n.tryCommit()
	// }
	if vertex.Round >= 4 {
		// leader, ok := n.coins[vertex.Round-1]
		// if !ok {
		// 	return
		// }
		// n.initRoundState(vertex.Round - 1)
		// //先有coin，由边的信息触发coin路径
		// if len(n.directSupport[vertex.Round-1][leader]) >= int(n.cfg.F+1) {
		// 	// n.logger.Debugf("invoke coinPass round(%v) leader_round(%v)", vertex.Round-5, vertex.Round-2)
		// 	n.coinPass(vertex.Round-3, vertex.Round-1)
		// 	if (vertex.Round-3)%2 == 1 {
		// 		n.FirstRound = int32(vertex.Round - 3)
		// 	} else {
		// 		n.SecondRound = int32(vertex.Round - 3)
		// 	}
		// 	n.tryCommit()
		// 	// n.logger.Infof("update odd(%v) - even(%v)", n.oddRound, n.evenRound)
		// }
		var currLeaderRound uint32
		if (vertex.Round-1)%2 == 1 {
			currLeaderRound = uint32(n.FirstRound + 4)
		} else {
			currLeaderRound = uint32(n.SecondRound + 4)
		}
		if currLeaderRound <= vertex.Round-1 {
			n.tryCoinPass(vertex.Round - 1)
		}

		if vertex.Round%2 == 1 {
			currLeaderRound = uint32(n.FirstRound + 4)
		} else {
			currLeaderRound = uint32(n.SecondRound + 4)
		}
		if currLeaderRound <= vertex.Round {
			if leader, ok := n.coins[vertex.Round]; ok {
				if leader == vertex.Id {
					n.tryCoinPass(vertex.Round)
				}
			}
		}

		if (vertex.Round+1)%2 == 1 {
			currLeaderRound = uint32(n.FirstRound + 4)
		} else {
			currLeaderRound = uint32(n.SecondRound + 4)
		}
		if currLeaderRound <= vertex.Round+1 {
			if leader, ok1 := n.coins[vertex.Round+1]; ok1 {
				if _, ok2 := n.DAG[vertex.Round+1][leader]; ok2 {
					if _, ok3 := n.directSupport[vertex.Round][vertex.Id][leader]; ok3 {
						n.tryCoinPass(vertex.Round + 1)
					}
				}
			}
		}
	}
}

func (n *Node) tryAddVirtualDAG(vertex *common.Vertex) {
	n.addVirtualConn(vertex)
}

// 功能：更新边的状态，从而触发快速执行，或者用来触发coin path
func (n *Node) addVirtualConn(vertex *common.Vertex) {
	if vertex.Round < 1 {
		return
	}
	n.initRoundState(vertex.Round)
	n.valDAG[vertex.Round][vertex.Id] = struct{}{}

	if n.echoed[vertex.Round] == nil {
		n.echoed[vertex.Round] = make(map[uint32]map[uint32]struct{})
	}
	if n.echoed[vertex.Round][vertex.Id] == nil {
		n.echoed[vertex.Round][vertex.Id] = make(map[uint32]struct{})
	}
	if _, ok := n.hasTent[vertex.Round][vertex.Id]; !ok && len(n.echoed[vertex.Round][vertex.Id]) >= int(n.cfg.F+1) {
		if n.hasTent[vertex.Round] == nil {
			n.hasTent[vertex.Round] = make(map[uint32]struct{})
		}
		n.hasTent[vertex.Round][vertex.Id] = struct{}{}
		tmpQC := &common.QC{
			Id:    vertex.Id,
			Round: vertex.Round,
			Hash:  n.vals[vertex.Round][vertex.Id].Hash,
			Type:  0,
		}
		sigs := make([]*common.Signature, 0)
		for from, signature := range n.echos[vertex.Round][vertex.Id] {
			sig := &common.Signature{
				Id:  from,
				Sig: signature,
			}
			sigs = append(sigs, sig)
		}
		tmpQC.Sigs = sigs
		if n.weakCerts[vertex.Round] == nil {
			n.weakCerts[vertex.Round] = make(map[uint32]*common.QC)
		}
		n.weakCerts[vertex.Round][vertex.Id] = tmpQC
		n.tryBroadcastSync(vertex.Round, vertex.Id)
	}

	msg := n.vals[vertex.Round][vertex.Id]
	if _, ok1 := n.payloads[msg.Round][msg.Sender]; ok1 {
		if _, ok2 := n.selfEcho[msg.Round][msg.Sender]; !ok2 {
			if n.selfEcho[msg.Round] == nil {
				n.selfEcho[msg.Round] = make(map[uint32]struct{})
			}
			n.selfEcho[msg.Round][msg.Sender] = struct{}{}
			echoMsg := &common.Message{
				Round:  msg.Round,
				Sender: msg.Sender,
				Type:   common.Message_ECHO,
				Hash:   msg.Hash,
			}
			echoMsg.From = n.cfg.ID
			n.network.BroadcastMessage(echoMsg)
			if _, ok3 := n.hasTent[vertex.Round][vertex.Id]; !ok3 && len(n.echoed[vertex.Round][vertex.Id]) >= int(n.cfg.F) {
				if n.hasTent[vertex.Round] == nil {
					n.hasTent[vertex.Round] = make(map[uint32]struct{})
				}
				n.hasTent[vertex.Round][vertex.Id] = struct{}{}
				tmpQC := &common.QC{
					Id:    vertex.Id,
					Round: vertex.Round,
					Hash:  n.vals[vertex.Round][vertex.Id].Hash,
					Type:  0,
				}
				sigs := make([]*common.Signature, 0)
				for from, signature := range n.echos[vertex.Round][vertex.Id] {
					sig := &common.Signature{
						Id:  from,
						Sig: signature,
					}
					sigs = append(sigs, sig)
				}
				tmpMsg := &common.Message{
					Round:  echoMsg.Round,
					Sender: echoMsg.Sender,
					Type:   echoMsg.Type,
					Hash:   echoMsg.Hash,
				}
				content, err := proto.Marshal(tmpMsg)
				if err != nil {
					panic(err)
				}
				hash := crypto.Hash(content)
				selfSignature := crypto.Sign(n.cfg.PrivKey, []byte(hash))
				selfSig := &common.Signature{
					Id:  n.cfg.ID,
					Sig: selfSignature,
				}
				sigs = append(sigs, selfSig)
				tmpQC.Sigs = sigs
				if n.weakCerts[vertex.Round] == nil {
					n.weakCerts[vertex.Round] = make(map[uint32]*common.QC)
				}
				n.weakCerts[vertex.Round][vertex.Id] = tmpQC
				n.tryBroadcastSync(vertex.Round, vertex.Id)
			}
		}
	}
	_, ok1 := n.vertexBuffer[vertex.Round][vertex.Id]
	_, ok2 := n.virtualVertexBuffer[vertex.Round][vertex.Id]
	if ok1 || ok2 {
		n.tryAddDAG(vertex)
	}

	// 从Round = 2 才进行判断
	// if vertex.Round >= 2 {
	// 	n.initRoundState(vertex.Round - 1)
	// 	// connection.Round + 1 = vertex.Round
	// 	for _, connection := range vertex.Connections {
	// 		n.virtualSupport[connection.Round][connection.Id][vertex.Id] = true
	// 		// if len(n.virtualSupport[connection.Round][connection.Id]) >= int(n.cfg.N-n.cfg.F) {
	// 		// 	// 没有decide,decide 1
	// 		// 	if _, ok := n.decideState[connection.Round][connection.Id]; !ok {
	// 		// 		n.decideState[connection.Round][connection.Id] = 2
	// 		// 		if n.decidePath[connection.Round] < F1R1D1 {
	// 		// 			n.decidePath[connection.Round] = F1R1D1
	// 		// 		}
	// 		// 		n.logger.Debugf("%v-%v fast decide 1 in val of round + 1", connection.Round, connection.Id)
	// 		// 	}
	// 		// }
	// 		n.tentativeVirtualSupport[connection.Round][connection.Id][vertex.Id] = true
	// 		if len(n.tentativeVirtualSupport[connection.Round][connection.Id]) == int(n.cfg.N) {
	// 			if _, ok := n.decideState2[connection.Round][connection.Id]; !ok {
	// 				n.decideState2[connection.Round][connection.Id] = 2
	// 				if n.decidePath2[connection.Round] < FASTPATH {
	// 					n.decidePath2[connection.Round] = FASTPATH
	// 				}
	// 				n.logger.Debugf("fast 1: %v-%v", connection.Round, connection.Id)
	// 			}
	// 		}
	// 	}
	// 	for _, tentconn := range vertex.TentConnect {
	// 		n.tentativeVirtualSupport[tentconn.Round][tentconn.Id][vertex.Id] = true
	// 		if len(n.tentativeVirtualSupport[tentconn.Round][tentconn.Id]) == int(n.cfg.N) {
	// 			if _, ok := n.decideState2[tentconn.Round][tentconn.Id]; !ok {
	// 				n.decideState2[tentconn.Round][tentconn.Id] = 2
	// 				if n.decidePath2[tentconn.Round] < FASTPATH {
	// 					n.decidePath2[tentconn.Round] = FASTPATH
	// 				}
	// 				n.logger.Debugf("fast 1: %v-%v", tentconn.Round, tentconn.Id)
	// 			}
	// 		}
	// 	}
	// }

	// 从Round = 3 才进行判断
	// if vertex.Round >= 3 {
	// 	n.initRoundState(vertex.Round - 2)
	// 	preNodes := make(map[uint32]bool)
	// 	for _, connection := range vertex.Connections {
	// 		preNodes[connection.Id] = true
	// 	}
	// 	ppreNodes := make(map[uint32]bool)

	// 	for id := range preNodes {
	// 		for _, conn := range n.DAG[vertex.Round-1][id].Connections {
	// 			ppreNodes[conn.Id] = true
	// 		}
	// 	}

	// 	for i := uint32(1); i <= n.cfg.N; i++ {
	// 		if _, ok := ppreNodes[i]; !ok {
	// 			n.virtualReject[vertex.Round-2][i][vertex.Id] = true
	// 		}
	// 		if len(n.virtualReject[vertex.Round-2][i]) >= int(n.cfg.N-n.cfg.F) {
	// 			if _, ok := n.decideState[vertex.Round-2][i]; !ok {
	// 				n.decideState[vertex.Round-2][i] = 1
	// 				if n.decidePath[vertex.Round-2] < F1R2D0 {
	// 					n.decidePath[vertex.Round-2] = F1R2D0
	// 				}
	// 				n.logger.Debugf("%v-%v fast decide 0 in val of round + 2", vertex.Round-2, i)

	// 			}
	// 		}
	// 	}
	// }
}

// 是否存在 <r1, v1> <-- <r2, v2> 这条边
func (n *Node) Path(r1 uint32, v1 uint32, r2 uint32, v2 uint32) bool {
	if r1 == r2 && v1 == v2 {
		return true
	}
	if r1 == r2 && v1 != v2 {
		return false
	}
	if r1 > r2 {
		return false
	}
	//bfs 寻找
	mp := make(map[uint32]int)
	mp[v1] = 1
	startRound := r1
	for startRound != r2 {
		tmp := make(map[uint32]int)
		for i := range mp {
			for v := range n.directSupport[r1][i] {
				tmp[v] = 1
			}
		}
		mp = tmp
		startRound = startRound + 1
		if startRound == r2 && tmp[v2] == 1 {
			return true
		}
	}
	return false
}

// onReceiveCoin触发：收到足够coin恢复出leader, coin leader合法，前序round的leader都恢复出来了
func (n *Node) tryCoinPass(leaderRound uint32) {
	var currLeaderRound uint32
	if leaderRound%2 == 1 {
		currLeaderRound = uint32(n.FirstRound + 4)
	} else {
		currLeaderRound = uint32(n.SecondRound + 4)
	}

	for currLeaderRound <= leaderRound {
		if _, ok := n.coins[currLeaderRound]; !ok {
			n.logger.Debugf("fail leader path without leader %v", currLeaderRound)
			return
		}
		currLeaderRound += 2
	}

	leader := n.coins[leaderRound]
	toCheck := make(map[uint32]struct{})
	toCheck[leader] = struct{}{}
	excFlag := n.coinPass(leaderRound-2, leaderRound, false, toCheck)
	if excFlag {
		if (leaderRound-2)%2 == 1 {
			n.FirstRound = int32(leaderRound - 2)
		} else {
			n.SecondRound = int32(leaderRound - 2)
		}
		n.tryCommit()
	}
}

// round 需要进行decide的round, 通过leader决定
func (n *Node) coinPass(round uint32, leaderRound uint32, checkValid bool, toCheckVertex map[uint32]struct{}) bool {
	if round%2 == 1 && int32(round) <= n.FirstRound {
		return true
	}
	if round%2 == 0 && int32(round) <= n.SecondRound {
		return true
	}
	leader := n.coins[leaderRound]
	n.initRoundState(leaderRound)
	if checkValid || len(n.directSupport[leaderRound][leader]) >= int(n.cfg.F+1) {
		toCheck := make(map[uint32]struct{})
		for id := range toCheckVertex {
			if _, ok := n.DAG[round+2][id]; !ok {
				n.logger.Debugf("fail leader path without leader qc %v-%v", leaderRound, leader)
				return false
			}
			for _, qc := range n.DAG[round+2][id].Connections {
				if _, ok := n.DAG[qc.Round][qc.Id]; !ok {
					n.logger.Debugf("fail leader path without leader %v-%v pre-qc", leaderRound, leader)
					return false
				}
				for _, preQC := range n.DAG[qc.Round][qc.Id].Connections {
					toCheck[preQC.Id] = struct{}{}
				}
			}
		}
		excFlag := true
		preFlag := true
		if round == 1 || round == 2 {
			preFlag = false
		}
		if round%2 == 1 && round == uint32(n.FirstRound+2) {
			preFlag = false
		}
		if round%2 == 0 && round == uint32(n.SecondRound+2) {
			preFlag = false
		}
		if preFlag {
			leader_r := n.coins[round]
			if n.Path(round, leader_r, leaderRound, leader) {
				toCheck = make(map[uint32]struct{})
				toCheck[leader_r] = struct{}{}
				excFlag = n.coinPass(round-2, round, true, toCheck)
			} else {
				excFlag = n.coinPass(round-2, leaderRound, true, toCheck)
			}
		}
		if !excFlag {
			return false
		} else {
			if n.decideState2[round] == nil {
				n.decideState2[round] = make(map[uint32]uint8)
			}
			if len(n.decideState2[round]) != int(n.cfg.N) {
				for i := uint32(1); i <= n.cfg.N; i++ {
					// coin 判断
					if _, ok2 := n.decideState2[round][i]; !ok2 {
						conn := 0
						n.initRoundState(round)
						for id := range n.tentativeSupport[round][i] {
							n.logger.Debugf("tentative support by %v", id)
							if n.Path(round+1, id, leaderRound, leader) {
								n.logger.Debugf("has path to leader")
								conn = conn + 1
							}
						}
						n.decidePath2[round] = COINPATH
						if conn >= int(n.cfg.F+1) {
							n.logger.Debugf("decide 1: %v-%v", round, i)
							n.decideState2[round][i] = 2
						} else {
							n.logger.Debugf("decide 0: %v-%v", round, i)
							n.decideState2[round][i] = 1
						}
					}
				}
			}
			return true
		}
	}
	return false
}

func (n *Node) getVertexRound(pos uint32) uint32 {
	if pos%n.cfg.N == 0 {
		return pos / n.cfg.N
	}
	return pos/n.cfg.N + 1
}

func (n *Node) getVertexId(pos uint32) uint32 {
	if pos%n.cfg.N == 0 {
		return n.cfg.N
	}
	return pos % n.cfg.N
}

func (n *Node) getRandomVertexId(pos uint32) uint32 {
	round := n.getVertexRound(pos)
	target := pos + round - 1
	if target%n.cfg.N == 0 {
		return n.cfg.N
	}
	return target % n.cfg.N
}

func (n *Node) tryCommit() {
	var ok2 bool
	var d2 uint8
	d2, ok2 = n.decideState2[n.getVertexRound(n.committedVertex+1)][n.getRandomVertexId(n.committedVertex+1)]
	for ok2 {
		// decide 0: skip a vertex
		round := n.getVertexRound(n.committedVertex + 1)
		id := n.getRandomVertexId(n.committedVertex + 1)
		// n.logger.Debugf("try commit vertex: %v-%v", round, id)
		if d2 == 1 {
			// 目前节点跳过
			n.committedVertex += 1
			// 检查下一节点
			d2, ok2 = n.decideState2[n.getVertexRound(n.committedVertex+1)][n.getRandomVertexId(n.committedVertex+1)]
			// 确定每一轮的路径
			if n.committedVertex%n.cfg.N == 0 {
				n.fastPassStates[n.decidePath2[round]]++
				if round%20 == 0 {
					n.logger.Debugf("till round %v: fast %v, leader %v", round, n.fastPassStates[FASTPATH], n.fastPassStates[COINPATH])
				}
			}
			continue
		}

		if _, valOK := n.valDAG[round][id]; !valOK {
			n.logger.Debugf("fail try commit without val: %v-%v", round, id)
			return
		}
		if _, payOK := n.payloads[round][id]; !payOK {
			n.logger.Debugf("fail try commit without payload: %v-%v", round, id)
			return
		}
		// decide 1: commit a vertex
		if id == n.cfg.ID {
			n.blockBack(round)
		}
		delete(n.payloads[round], id)
		// delete(n.certs[round], id)
		n.initRoundState(round)
		n.initRoundState(round + 1)
		n.execState[round][id] = 1
		fatherNodes := make(map[uint32]bool)
		fatherNodes[id] = true
		n.executeBack(round-1, fatherNodes)
		n.weakExc(round, id)
		// 目前节点已提交
		n.committedVertex += 1
		// 检查下一节点
		d2, ok2 = n.decideState2[n.getVertexRound(n.committedVertex+1)][n.getRandomVertexId(n.committedVertex+1)]

		// 确定每一轮的路径
		if n.committedVertex%n.cfg.N == 0 {
			n.fastPassStates[n.decidePath2[round]]++
			if round%20 == 0 {
				n.logger.Debugf("till round %v: fast %v, leader %v", round, n.fastPassStates[FASTPATH], n.fastPassStates[COINPATH])
			}
		}
	}
}

// 以轮为单位执行逻辑，并执行返回客户端的逻辑
// func (n *Node) tryCommit() {
// 	n.updateReadyRound()
// 	if n.decideState[n.committedVertex+1] == nil {
// 		n.decideState[n.committedVertex+1] = make(map[uint32]uint8)
// 	}
// 	for len(n.decideState[n.committedVertex+1]) == int(n.cfg.N) {
// 		// n.logger.Infof("try commit round %v", n.committedVertex+1)
// 		n.committedVertex = n.committedVertex + 1
// 		if n.execState[n.committedVertex] == nil {
// 			n.execState[n.committedVertex] = make(map[uint32]uint8)
// 		}
// 		fatherNodes := make(map[uint32]bool)
// 		for i := uint32(1); i <= n.cfg.N; i++ {
// 			if n.decideState[n.committedVertex][i] == 2 {
// 				fatherNodes[i] = true
// 				if _, ok := n.execState[n.committedVertex][i]; !ok {
// 					//没有执行就执行
// 					// n.logger.Infoln("in try commitRound")
// 					n.logger.Debugf("execute %v-%v", n.committedVertex, i)
// 					// fmt.Printf("execute %v-%v\n", n.committedVertex, i)
// 					delete(n.payloads[n.committedVertex], i)
// 					n.execState[n.committedVertex][i] = 1
// 					if i == n.cfg.ID {
// 						n.blockBack(n.committedVertex)
// 					}
// 				}

// 			} else if n.decideState[n.committedVertex][i] != 1 {
// 				n.logger.Debugln("unexcept status")
// 			}
// 		}
// 		if n.decideState[n.committedVertex+1] == nil {
// 			n.decideState[n.committedVertex+1] = make(map[uint32]uint8)
// 		}
// 		n.executeBack(n.committedVertex-1, fatherNodes)

// 	}
// }

func (n *Node) weakExc(round uint32, id uint32) {
	if v, ok := n.DAG[round][id]; ok {
		for _, conn := range v.WeakConnect {
			if n.execState[conn.Round] == nil {
				n.execState[conn.Round] = make(map[uint32]uint8)
			}
			if len(n.execState[conn.Round]) == int(n.cfg.N) {
				return
			}
			if _, ok := n.execState[conn.Round][conn.Id]; !ok {
				delete(n.payloads[conn.Round], conn.Id)
				// delete(n.certs[conn.Round], conn.Id)
				n.execState[conn.Round][conn.Id] = 1
				if conn.Id == n.cfg.ID {
					n.logger.Infof("weak execute")
					n.blockBack(conn.Round)
				}
			}
		}
	}
}

// 按照轮次循环找前序历史节点
func (n *Node) executeBack(round uint32, fatherNodes map[uint32]bool) {
	if round == 0 {
		return
	}
	if n.execState[round] == nil {
		n.execState[round] = make(map[uint32]uint8)
	}
	if len(n.execState[round]) == int(n.cfg.N) {
		return
	}
	mp := make(map[uint32]bool)
	flag := false //标志是否有新的要执行的节点
	toCheck := make([]uint32, n.cfg.N+1)

	//基数排序：O(N) + O(N)
	for id := range fatherNodes {
		toCheck[id] = 1
	}
	for id := uint32(1); id <= n.cfg.N; id++ {
		if toCheck[id] != 1 {
			continue
		}
		//TOCHECK
		if v, ok := n.DAG[round+1][id]; ok {
			for _, conn := range v.Connections {
				mp[conn.Id] = true
				// n.logger.Debugf("%v-%v connect %v-%v", v.Round, v.Id, round, conn.Id)
				if _, ok := n.execState[round][conn.Id]; !ok {
					//没有执行就执行
					flag = true
					delete(n.payloads[round], conn.Id)
					// delete(n.certs[round], conn.Id)
					n.execState[round][conn.Id] = 1
					// n.logger.Debugf("execute %v-%v", round, conn.Id)
					// fmt.Printf("execute %v-%v\n", round, conn.Id)
					if conn.Id == n.cfg.ID {
						n.blockBack(round)
					}
					n.weakExc(round, conn.Id)
				}
			}
		}

	}
	if flag {
		n.executeBack(round-1, mp)
	}

}

// 答复客户端
func (n *Node) blockBack(round uint32) {
	finishTime := uint64(time.Now().UnixNano() / 1000000)
	latency := finishTime - n.startTime[round]
	n.executionLatencies[round] = latency
	n.logger.Infof("my round %v execute using %v ms", round, latency)
	st := &common.NodeBack{
		NodeID:  0,
		StartID: uint32(n.blockInfos[round].StartID),
		ReqNum:  uint32(n.blockInfos[round].ReqNum),
	}
	if finishTime-n.startSystemTime >= 10000 {
		st.Steady = true
	} else {
		st.Steady = false
	}
	delete(n.blockInfos, round)
	n.execNum++
	var resp common.Response
	go n.cli.Call("Client.NodeFinish", st, &resp)
}

func (n *Node) getCoinData(round uint32) []byte {
	var buffer bytes.Buffer
	buffer.WriteString(strconv.FormatUint(uint64(round), 10))
	buffer.WriteString("-")
	buffer.WriteString(string(n.cfg.MasterPK))
	return buffer.Bytes()
}

func (n *Node) getCoin(round uint32) {
	if round <= 2 {
		return
	}
	data := n.getCoinData(round)
	sigShare := crypto.BlsSign(data, n.cfg.ThresholdSK)
	msg := &common.Message{
		Round:   round,
		From:    n.cfg.ID,
		Sender:  n.cfg.ID,
		Type:    common.Message_COIN,
		Payload: sigShare,
	}
	n.network.BroadcastMessage(msg)
}

func (n *Node) verifyEcho(msg *common.Message) {
	tmp := &common.Message{
		Round:  msg.Round,
		Sender: msg.Sender,
		Type:   msg.Type,
		Hash:   msg.Hash,
	}
	content, err := proto.Marshal(tmp)
	if err != nil {
		panic(err)
	}
	hash := crypto.Hash(content)
	b := crypto.Verify(n.peers[msg.From].PublicKey, []byte(hash), msg.Signature)
	if b {
		n.msgChan <- msg
	} else {
		panic("verify failed!")
	}
}

// forward f+1 echos
func (n *Node) tryBroadcastSync(round uint32, sender uint32) {
	if round == n.currRound && len(n.DAG[round]) >= int(n.cfg.N-n.cfg.F) {
		syncMsg := &common.Message{
			Type: common.Message_SYNC,
			Qc:   n.weakCerts[round][sender],
		}
		n.network.BroadcastMessage(syncMsg)
	}
}
