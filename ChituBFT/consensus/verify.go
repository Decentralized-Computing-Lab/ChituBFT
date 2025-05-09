package consensus

import (
	"chitu/common"
	"chitu/crypto"
	"chitu/logger"

	"github.com/gogo/protobuf/proto"
)

type ValVerify struct {
	peers   map[uint32]*common.Peer
	val     *common.Message
	round   uint32 // sig.Round
	sigNum  int
	sigChan chan *common.SigToVerify
	resChan chan bool
	valChan chan *common.Message
	logger  logger.Logger
}

func NewValVerify(peers map[uint32]*common.Peer, val *common.Message, round uint32, sigNum int, valChan chan *common.Message, logger logger.Logger) *ValVerify {
	valVerify := &ValVerify{
		peers:   peers,
		val:     val,
		round:   round,
		sigNum:  sigNum,
		sigChan: make(chan *common.SigToVerify, sigNum),
		resChan: make(chan bool, sigNum),
		valChan: valChan,
		logger:  logger,
	}
	go valVerify.run()
	return valVerify
}

func (v *ValVerify) run() {
	if v.sigNum == 0 {
		v.logger.Debugf("verify %v-%v success", v.val.Round, v.val.Sender)
		v.valChan <- v.val
		return
	}
	resNum := 0
	for {
		select {
		case sig := <-v.sigChan:
			go v.Verify(sig)
		case res := <-v.resChan:
			if res {
				resNum++
				if resNum == v.sigNum {
					v.logger.Debugf("verify %v-%v success", v.val.Round, v.val.Sender)
					v.valChan <- v.val
					return
				}
			} else {
				v.logger.Debugln("invalid signature in val qc")
			}
		}
	}
}

func (v *ValVerify) Verify(sig *common.SigToVerify) {
	toVerify := &common.Message{
		Round:  v.round,
		Sender: sig.Sender,
		Type:   common.Message_ECHO,
		Hash:   sig.Hash,
	}
	content, err := proto.Marshal(toVerify)
	if err != nil {
		panic(err)
	}
	hash := crypto.Hash(content)
	hashByte := []byte(hash)
	v.resChan <- crypto.Verify(v.peers[sig.From].PublicKey, hashByte, sig.Signature)
}
