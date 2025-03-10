package chain

import (
	"fmt"
	"math/big"

	"storage-mining/configs"
	"storage-mining/internal/logger"
	"storage-mining/tools"
	"strconv"
	"time"

	"github.com/centrifuge/go-substrate-rpc-client/v4/signature"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/pkg/errors"
)

// custom event type
type Event_SegmentBook_ParamSet struct {
	Phase     types.Phase
	PeerId    types.U64
	SegmentId types.U64
	Random    types.U32
	Topics    []types.Hash
}

type Event_VPABCD_Submit_Verify struct {
	Phase     types.Phase
	PeerId    types.U64
	SegmentId types.U64
	Topics    []types.Hash
}

type Event_Sminer_TimedTask struct {
	Phase  types.Phase
	Topics []types.Hash
}

type Event_Sminer_Registered struct {
	Phase   types.Phase
	PeerAcc types.AccountID
	Staking types.U128
	Topics  []types.Hash
}

type MyEventRecords struct {
	System_ExtrinsicSuccess  []types.EventSystemExtrinsicSuccess
	System_ExtrinsicFailed   []types.EventSystemExtrinsicFailed
	SegmentBook_ParamSet     []Event_SegmentBook_ParamSet
	SegmentBook_VPASubmitted []Event_VPABCD_Submit_Verify
	SegmentBook_VPBSubmitted []Event_VPABCD_Submit_Verify
	SegmentBook_VPCSubmitted []Event_VPABCD_Submit_Verify
	SegmentBook_VPDSubmitted []Event_VPABCD_Submit_Verify
	SegmentBook_VPAVerified  []Event_VPABCD_Submit_Verify
	SegmentBook_VPBVerified  []Event_VPABCD_Submit_Verify
	SegmentBook_VPCVerified  []Event_VPABCD_Submit_Verify
	SegmentBook_VPDVerified  []Event_VPABCD_Submit_Verify
	Sminer_TimedTask         []Event_Sminer_TimedTask
	Sminer_Registered        []Event_Sminer_Registered
}

// miner register
func RegisterToChain(identifyAccountPhrase, incomeAccountPublicKey, ipAddr, TransactionName string, pledgeTokens uint64, port, fileport uint32) (bool, error) {
	var (
		err         error
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	ipint, err := tools.InetAtoN(ipAddr)
	if err != nil {
		return false, errors.Wrap(err, "InetAtoN err")
	}

	pTokens := strconv.FormatUint(pledgeTokens, 10)
	pTokens += configs.TokenAccuracy
	realTokens, ok := new(big.Int).SetString(pTokens, 10)
	if !ok {
		return false, errors.New("SetString err")
	}
	amount := types.NewUCompact(realTokens)

	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return false, errors.Wrap(err, "KeyringPairFromSecret err")
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetMetadataLatest err")
	}

	incomeAccount, err := types.NewMultiAddressFromHexAccountID(incomeAccountPublicKey)
	if err != nil {
		return false, errors.Wrap(err, "NewMultiAddressFromHexAccountID err")
	}

	c, err := types.NewCall(meta, TransactionName, incomeAccount, types.NewU32(uint32(ipint)), types.NewU32(port), types.NewU32(fileport), amount)
	if err != nil {
		return false, errors.Wrap(err, "NewCall err")
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return false, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return false, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey System  Account err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return false, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return false, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return false, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return false, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return false, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				if events.Sminer_Registered != nil {
					for i := 0; i < len(events.Sminer_Registered); i++ {
						if events.Sminer_Registered[i].PeerAcc == types.NewAccountID(keyring.PublicKey) {
							return true, nil
						}
					}
				}
				return false, nil
			}
		case err = <-sub.Err():
			return false, err
		case <-timeout:
			return false, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}

//
func IntentSubmitToChain(identifyAccountPhrase, TransactionName string, segsizetype, segtype uint8, peerid uint64, unsealedcid [][]byte, hash, shardhash []byte) (uint64, uint32, error) {
	var (
		err         error
		ok          bool
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return 0, 0, errors.Wrap(err, "KeyringPairFromSecret err")
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return 0, 0, errors.Wrap(err, "GetMetadataLatest err")
	}
	var uncid []types.Bytes = make([]types.Bytes, len(unsealedcid))
	for i := 0; i < len(unsealedcid); i++ {
		uncid[i] = make(types.Bytes, 0)
		uncid[i] = append(uncid[i], unsealedcid[i]...)
	}
	c, err := types.NewCall(meta, TransactionName, types.NewU8(segsizetype), types.NewU8(segtype), types.NewU64(peerid), uncid, types.NewBytes(hash), types.NewBytes(shardhash))
	if err != nil {
		return 0, 0, errors.Wrap(err, "NewCall err")
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return 0, 0, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return 0, 0, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return 0, 0, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return 0, 0, errors.Wrap(err, "CreateStorageKey err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return 0, 0, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return 0, 0, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return 0, 0, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return 0, 0, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return 0, 0, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return 0, 0, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				if events.SegmentBook_ParamSet != nil {
					for i := 0; i < len(events.SegmentBook_ParamSet); i++ {
						if events.SegmentBook_ParamSet[i].PeerId == types.NewU64(configs.MinerId_I) {
							return uint64(events.SegmentBook_ParamSet[i].SegmentId), uint32(events.SegmentBook_ParamSet[i].Random), nil
						}
					}
				}
				return 0, 0, nil
			}
		case err = <-sub.Err():
			return 0, 0, err
		case <-timeout:
			return 0, 0, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}

//
func IntentSubmitPostToChain(identifyAccountPhrase, TransactionName string, segmentid uint64, segsizetype, segtype uint8) (uint32, error) {
	var (
		err         error
		ok          bool
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return 0, errors.Wrap(err, "KeyringPairFromSecret err")
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return 0, errors.Wrap(err, "GetMetadataLatest err")
	}

	c, err := types.NewCall(meta, TransactionName, types.NewU64(segmentid), types.NewU8(segsizetype), types.NewU8(segtype))
	if err != nil {
		return 0, errors.Wrap(err, "NewCall err")
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return 0, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return 0, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return 0, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return 0, errors.Wrap(err, "CreateStorageKey err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return 0, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return 0, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return 0, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return 0, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return 0, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return 0, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				if events.SegmentBook_ParamSet != nil {
					for i := 0; i < len(events.SegmentBook_ParamSet); i++ {
						if events.SegmentBook_ParamSet[i].PeerId == types.NewU64(configs.MinerId_I) {
							return uint32(events.SegmentBook_ParamSet[i].Random), nil
						}
					}
				}
				return 0, nil
			}
		case err = <-sub.Err():
			return 0, err
		case <-timeout:
			return 0, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}

// Submit To Vpa or Vpb
func SegmentSubmitToVpaOrVpb(identifyAccountPhrase, TransactionName string, peerid, segmentid uint64, proofs, cid []byte) (bool, error) {
	var (
		err         error
		ok          bool
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return false, errors.Wrapf(err, "KeyringPairFromSecret err [%v]", TransactionName)
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return false, errors.Wrapf(err, "GetMetadataLatest err [%v]", TransactionName)
	}

	c, err := types.NewCall(meta, TransactionName, types.NewU64(peerid), types.NewU64(segmentid), types.NewBytes(proofs), types.NewBytes(cid))
	if err != nil {
		return false, errors.Wrapf(err, "NewCall err [%v]", TransactionName)
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return false, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return false, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return false, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return false, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return false, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return false, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return false, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				switch TransactionName {
				case configs.ChainTx_SegmentBook_SubmitToVpa:
					if events.SegmentBook_VPASubmitted != nil {
						for i := 0; i < len(events.SegmentBook_VPASubmitted); i++ {
							if events.SegmentBook_VPASubmitted[i].PeerId == types.NewU64(configs.MinerId_I) && events.SegmentBook_VPASubmitted[i].SegmentId == types.NewU64(segmentid) {
								return true, nil
							}
						}
					} else {
						return false, nil
					}
				case configs.ChainTx_SegmentBook_SubmitToVpb:
					if events.SegmentBook_VPBSubmitted != nil {
						for i := 0; i < len(events.SegmentBook_VPBSubmitted); i++ {
							if events.SegmentBook_VPBSubmitted[i].PeerId == types.NewU64(configs.MinerId_I) && events.SegmentBook_VPBSubmitted[i].SegmentId == types.NewU64(segmentid) {
								return true, nil
							}
						}
					} else {
						return false, nil
					}
				}
				return false, nil
			}
		case err = <-sub.Err():
			return false, err
		case <-timeout:
			return false, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}

// Submit To Vpc
func SegmentSubmitToVpc(identifyAccountPhrase, TransactionName string, peerid, segmentid uint64, proofs [][]byte, sealcid []types.Bytes) (bool, error) {
	var (
		err         error
		ok          bool
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return false, errors.Wrap(err, "KeyringPairFromSecret err")
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetMetadataLatest err")
	}

	var fileVpc []types.Bytes = make([]types.Bytes, len(proofs))
	for i := 0; i < len(proofs); i++ {
		fileVpc[i] = make(types.Bytes, 0)
		fileVpc[i] = append(fileVpc[i], proofs[i]...)
	}
	// var sealedcid []types.Bytes = make([]types.Bytes, len(sealcid))
	// for i := 0; i < len(sealcid); i++ {
	// 	sealedcid[i] = make(types.Bytes, 0)
	// 	sealedcid[i] = append(sealedcid[i], sealcid[i]...)
	// }
	c, err := types.NewCall(meta, TransactionName, types.NewU64(peerid), types.NewU64(segmentid), fileVpc, sealcid)
	if err != nil {
		return false, errors.Wrap(err, "NewCall err")
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return false, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return false, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return false, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return false, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return false, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return false, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return false, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				if events.SegmentBook_VPCSubmitted != nil {
					for i := 0; i < len(events.SegmentBook_VPCSubmitted); i++ {
						if events.SegmentBook_VPCSubmitted[i].PeerId == types.NewU64(configs.MinerId_I) && events.SegmentBook_VPCSubmitted[i].SegmentId == types.NewU64(segmentid) {
							return true, nil
						}
					}
				}
				return false, nil
			}
		case err = <-sub.Err():
			return false, err
		case <-timeout:
			return false, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}

// Submit To Vpd
func SegmentSubmitToVpd(identifyAccountPhrase, TransactionName string, peerid, segmentid uint64, proofs [][]byte, sealcid []types.Bytes) (bool, error) {
	var (
		err         error
		ok          bool
		accountInfo types.AccountInfo
	)
	api := getSubstrateAPI()
	defer func() {
		releaseSubstrateAPI()
		err := recover()
		if err != nil {
			logger.ErrLogger.Sugar().Errorf("[panic]: %v", err)
		}
	}()
	keyring, err := signature.KeyringPairFromSecret(identifyAccountPhrase, 0)
	if err != nil {
		return false, errors.Wrap(err, "KeyringPairFromSecret err")
	}

	meta, err := api.RPC.State.GetMetadataLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetMetadataLatest err")
	}

	var fileVpd []types.Bytes = make([]types.Bytes, len(proofs))
	for i := 0; i < len(proofs); i++ {
		fileVpd[i] = make(types.Bytes, 0)
		fileVpd[i] = append(fileVpd[i], proofs[i]...)
	}
	c, err := types.NewCall(meta, TransactionName, types.NewU64(peerid), types.NewU64(segmentid), fileVpd, sealcid)
	if err != nil {
		return false, errors.Wrap(err, "NewCall err")
	}

	ext := types.NewExtrinsic(c)
	if err != nil {
		return false, errors.Wrap(err, "NewExtrinsic err")
	}

	genesisHash, err := api.RPC.Chain.GetBlockHash(0)
	if err != nil {
		return false, errors.Wrap(err, "GetBlockHash err")
	}

	rv, err := api.RPC.State.GetRuntimeVersionLatest()
	if err != nil {
		return false, errors.Wrap(err, "GetRuntimeVersionLatest err")
	}

	key, err := types.CreateStorageKey(meta, "System", "Account", keyring.PublicKey)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey err")
	}

	keye, err := types.CreateStorageKey(meta, "System", "Events", nil)
	if err != nil {
		return false, errors.Wrap(err, "CreateStorageKey System Events err")
	}

	ok, err = api.RPC.State.GetStorageLatest(key, &accountInfo)
	if err != nil {
		return false, errors.Wrap(err, "GetStorageLatest err")
	}
	if !ok {
		return false, errors.New("GetStorageLatest return value is empty")
	}

	o := types.SignatureOptions{
		BlockHash:          genesisHash,
		Era:                types.ExtrinsicEra{IsMortalEra: false},
		GenesisHash:        genesisHash,
		Nonce:              types.NewUCompactFromUInt(uint64(accountInfo.Nonce)),
		SpecVersion:        rv.SpecVersion,
		Tip:                types.NewUCompactFromUInt(0),
		TransactionVersion: rv.TransactionVersion,
	}

	// Sign the transaction
	err = ext.Sign(keyring, o)
	if err != nil {
		return false, errors.Wrap(err, "Sign err")
	}

	// Do the transfer and track the actual status
	sub, err := api.RPC.Author.SubmitAndWatchExtrinsic(ext)
	if err != nil {
		return false, errors.Wrap(err, "SubmitAndWatchExtrinsic err")
	}
	defer sub.Unsubscribe()

	timeout := time.After(time.Second * configs.TimeToWaitEvents_S)
	for {
		select {
		case status := <-sub.Chan():
			if status.IsInBlock {
				events := MyEventRecords{}
				h, err := api.RPC.State.GetStorageRaw(keye, status.AsInBlock)
				if err != nil {
					return false, err
				}
				err = types.EventRecordsRaw(*h).DecodeEventRecords(meta, &events)
				if err != nil {
					fmt.Println("+++ DecodeEvent err: ", err)
				}
				if events.SegmentBook_VPDSubmitted != nil {
					for i := 0; i < len(events.SegmentBook_VPDSubmitted); i++ {
						if events.SegmentBook_VPDSubmitted[i].PeerId == types.NewU64(configs.MinerId_I) && events.SegmentBook_VPDSubmitted[i].SegmentId == types.NewU64(segmentid) {
							return true, nil
						}
					}
				}
				return false, nil
			}
		case err = <-sub.Err():
			return false, err
		case <-timeout:
			return false, errors.New("SubmitAndWatchExtrinsic timeout")
		}
	}
}
