package network

import (
	"context"
	"net"
	"strconv"
	"strings"
	"chitu/common"
	"chitu/crypto"
	"chitu/logger"

	"github.com/gogo/protobuf/proto"
	"github.com/perlin-network/noise"
)

type NoiseMessage struct {
	Msg *common.Message
}

func (m NoiseMessage) Marshal() []byte {
	data, _ := m.Msg.Marshal()
	return data
}

func UnmarshalNoiseMessage(buf []byte) (NoiseMessage, error) {
	m := NoiseMessage{Msg: new(common.Message)}
	err := m.Msg.Unmarshal(buf)
	if err != nil {
		return NoiseMessage{}, err
	}
	return m, nil
}

type NoiseNetWork struct {
	id      uint32
	f       uint32
	node    *noise.Node
	peers   map[uint32]common.Peer
	msgChan chan *common.Message
	logger  logger.Logger
	priv    []byte
		
	identity bool
}

func NewNoiseNetWork(id uint32, f uint32, addr string, batchsize int, peers map[uint32]*common.Peer, msgChan chan *common.Message,
	logger logger.Logger, priv []byte, change bool, multi uint32, identity bool) *NoiseNetWork {
	ip := strings.Split(addr, ":")
	port, _ := strconv.ParseUint(ip[1], 10, 64)
	myPeers := make(map[uint32]common.Peer)
	if change {
		port += 20 + uint64(multi)
		for id, p := range peers {
			peerIP := strings.Split(p.Addr, ":")
			peerPort, _ := strconv.ParseUint(peerIP[1], 10, 64)
			peerPort += 20 + uint64(multi)
			newAddr := peerIP[0] + ":" + strconv.FormatInt(int64(peerPort), 10)
			myPeers[id] = common.Peer{
				ID:              id,
				Addr:            newAddr,
				PublicKey:       p.PublicKey,
				ThresholdPubKey: p.ThresholdPubKey,
				// EcdsaKey:        p.EcdsaKey,
			}
		}
	} else {
		for id, p := range peers {
			myPeers[id] = common.Peer{
				ID:              id,
				Addr:            p.Addr,
				PublicKey:       p.PublicKey,
				ThresholdPubKey: p.ThresholdPubKey,
				// EcdsaKey:        p.EcdsaKey,
			}
		}
	}

	buffersize := uint32(3)
	if change {
		buffersize = uint32(batchsize*2/1000)
	}
	node, _ := noise.NewNode(noise.WithNodeBindHost(net.ParseIP(ip[0])),
		noise.WithNodeBindPort(uint16(port)), noise.WithNodeMaxRecvMessageSize(buffersize*1024*1024))
	n := &NoiseNetWork{
		id:      id,
		f:       f,
		node:    node,
		peers:   myPeers,
		msgChan: msgChan,
		logger:  logger,
		priv:    priv,

		identity: identity,
	}
	n.node.RegisterMessage(NoiseMessage{}, UnmarshalNoiseMessage)
	n.node.Handle(n.Handler)
	err := n.node.Listen()
	if err != nil {
		panic(err)
	}
	n.logger.Debugf("listening on %v", n.node.Addr())
	return n
}

func (n *NoiseNetWork) Start() {
}

func (n *NoiseNetWork) Stop() {
	n.node.Close()
}

func (n *NoiseNetWork) BroadcastMessage(msg *common.Message) {
	if n.identity && (msg.Type != common.Message_VAL && msg.Type != common.Message_PAYLOAD) {
		return
	}
	m := NoiseMessage{Msg: msg}
	if msg.Type == common.Message_ECHO {
		n.Sign(n.priv, m.Msg)
	}
	for _, p := range n.peers {
		if p.ID == n.id {
			go n.OnReceiveMessage(m.Msg)
			continue
		}
		go n.node.SendMessage(context.TODO(), p.Addr, m)
	}
}

func (n *NoiseNetWork) SendMessage(id uint32, msg *common.Message) {
	m := NoiseMessage{Msg: msg}
	if msg.Type == common.Message_ECHO {
		n.Sign(n.priv, m.Msg)
	}
	n.node.SendMessage(context.TODO(), n.peers[id].Addr, m)
	// for {
	// 	if err == nil {
	// 		return
	// 	}
	// 	err = n.node.SendMessage(context.TODO(), n.peers[id].Addr, m)
	// }
}

func (n *NoiseNetWork) Handler(ctx noise.HandlerContext) error {
	obj, err := ctx.DecodeMessage()
	if err != nil {
		n.logger.Error("decode msg failed", err)
		return err
	}
	msg, ok := obj.(NoiseMessage)
	if !ok {
		n.logger.Error("cast msg failed", err)
		return nil
	}
	n.OnReceiveMessage(msg.Msg)
	return nil
}

func (n *NoiseNetWork) OnReceiveMessage(msg *common.Message) {
	// if n.Verify(msg, &n.peers[msg.From].EcdsaKey.PublicKey) {
	// 	if msg.Type == common.Message_VAL {
	// 		if n.VerifyQc(msg) {
	// 			n.msgChan <- msg
	// 		}
	// 	} else {
	// 		n.msgChan <- msg
	// 	}
	// } else {
	// 	panic("verify failed!")
	// }
	switch msg.Type {
	case common.Message_VAL:
		// if n.VerifyQc(msg) {
		// 	n.msgChan <- msg
		// }
		n.msgChan <- msg
	case common.Message_ECHO:
		if msg.From == n.id {
			n.msgChan <- msg
		} else if n.Verify(n.peers[msg.From].PublicKey, msg) {
			n.msgChan <- msg
		} else {
			panic("verify failed!")
		}
	default:
		n.msgChan <- msg
	}
}

func (n *NoiseNetWork) Verify(pub []byte, msg *common.Message) bool {
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
	b := crypto.Verify(pub, []byte(hash), msg.Signature)
	// if err != nil {
	// 	fmt.Println("Failed to verify a message: ", err)
	// }
	return b
}

func (n *NoiseNetWork) VerifyQc(msg *common.Message) bool {
	for _, qc := range msg.Vertex.Connections {
		if qc.Type == 0 {
			numOfSig := 0
			for _, sig := range qc.Sigs {
				toVerify := &common.Message{
					Round:  qc.Round,
					Sender: qc.Id,
					Type:   common.Message_ECHO,
					Hash:   qc.Hash,
				}
				content, err := proto.Marshal(toVerify)
				if err != nil {
					panic(err)
				}
				hash := crypto.Hash(content)
				if !crypto.Verify(n.peers[sig.Id].PublicKey, []byte(hash), sig.Sig) {
					n.logger.Debugf("invalid echo signature: sigId(%v) qc.round(%v) qc.id(%v)", sig.Id, qc.Round, qc.Id)
					return false
				}
				numOfSig++
			}
			if numOfSig < int(2*n.f+1) {
				n.logger.Debugf("invalid echo cert")
				return false
			}
		}
	}

	for _, qc := range msg.Vertex.WeakConnect {
		if qc.Type == 1 {
			for _, sig := range qc.Sigs {
				toVerify := &common.Message{
					Round:  qc.Round,
					Sender: qc.Id,
					Type:   common.Message_ECHO,
					Hash:   qc.Hash,
				}
				content, err := proto.Marshal(toVerify)
				if err != nil {
					panic(err)
				}
				hash := crypto.Hash(content)
				if !crypto.Verify(n.peers[sig.Id].PublicKey, []byte(hash), sig.Sig) {
					n.logger.Debugf("invalid echo signature: sigId(%v) qc.round(%v) qc.id(%v)", sig.Id, qc.Round, qc.Id)
					return false
				}
			}
		}
	}

	return true
}

func (n *NoiseNetWork) Sign(priv []byte, msg *common.Message) {
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
	sig := crypto.Sign(priv, []byte(hash))
	// if err != nil {
	// 	panic("myecdsa signECDSA failed")
	// }
	msg.Signature = sig
}
