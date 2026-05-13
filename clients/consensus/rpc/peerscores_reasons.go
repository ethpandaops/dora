package rpc

import "strings"

// Controlled reason-code vocabulary. Keep in sync with
// peer_scoring_reason_vocab.md. Unknown native reasons fall through to
// a category-level "*_other" bucket or the catch-all "unknown".
const (
	ReasonUnknown = "unknown"

	ReasonRPCInvalidRequest      = "rpc_invalid_request"
	ReasonRPCInvalidResponseSSZ  = "rpc_invalid_response_ssz"
	ReasonRPCResponseTimeout     = "rpc_response_timeout"
	ReasonRPCServerError         = "rpc_server_error"
	ReasonRPCResourceUnavailable = "rpc_resource_unavailable"
	ReasonRPCRateLimited         = "rpc_rate_limited"
	ReasonRPCUnsupportedProtocol = "rpc_unsupported_protocol"
	ReasonRPCIncompleteStream    = "rpc_incomplete_stream"
	ReasonRPCDialError           = "rpc_dial_error"
	ReasonRPCBadBlocksByRange    = "rpc_bad_blocks_by_range"
	ReasonRPCBadBlocksByRoot     = "rpc_bad_blocks_by_root"
	ReasonRPCBadBlobs            = "rpc_bad_blobs"
	ReasonRPCBadDataColumns      = "rpc_bad_data_columns"
	ReasonRPCOther               = "rpc_other"

	ReasonGossipInvalidBlock             = "gossip_invalid_block"
	ReasonGossipInvalidAttestation       = "gossip_invalid_attestation"
	ReasonGossipInvalidSyncMessage       = "gossip_invalid_sync_message"
	ReasonGossipInvalidBlobSidecar       = "gossip_invalid_blob_sidecar"
	ReasonGossipInvalidDataColumnSidecar = "gossip_invalid_data_column_sidecar"
	ReasonGossipInvalidSlashing          = "gossip_invalid_slashing"
	ReasonGossipInvalidVoluntaryExit     = "gossip_invalid_voluntary_exit"
	ReasonGossipInvalidBLSChange         = "gossip_invalid_bls_change"
	ReasonGossipOther                    = "gossip_other"

	ReasonSyncBadBatch             = "sync_bad_batch"
	ReasonSyncChainInvalid         = "sync_chain_invalid"
	ReasonSyncLookupFailed         = "sync_lookup_failed"
	ReasonSyncMaxProcessingAttempt = "sync_max_processing_attempts"
	ReasonSyncOther                = "sync_other"

	ReasonStatusBadForkDigest        = "status_bad_fork_digest"
	ReasonStatusInvalidFinalizedRoot = "status_invalid_finalized_root"
	ReasonStatusUnviableFork         = "status_unviable_fork"
	ReasonStatusLowHead              = "status_low_head"
	ReasonStatusStale                = "status_stale"
	ReasonStatusOther                = "status_other"

	ReasonColocation               = "colocation"
	ReasonBehaviourPenalty         = "behaviour_penalty"
	ReasonDASBadColumnIntersection = "das_bad_column_intersection"

	// Synthesized reasons: clients don't emit these, dora infers them
	// when a peer is in disconnect/banned state without an explicit
	// client-side reason. NativeReason carries the value that drove it.
	ReasonGossipsubLow            = "gossipsub_low"
	ReasonBadResponsesAccumulated = "bad_responses_accumulated"
	ReasonPeerStatusFailed        = "peer_status_failed"

	ReasonRewardGoodResponse  = "reward_good_response"
	ReasonRewardGoodStatus    = "reward_good_status"
	ReasonRewardBlockProvider = "reward_block_provider"
)

// CategoryFor returns the broad category bucket a reason code belongs
// to ("rpc", "gossip", "sync", "status", "connection", "reward",
// "other"). Used both for fallback translation and for DB grouping.
func CategoryFor(reasonCode string) string {
	switch {
	case strings.HasPrefix(reasonCode, "rpc_"):
		return "rpc"
	case strings.HasPrefix(reasonCode, "gossip_"):
		return "gossip"
	case strings.HasPrefix(reasonCode, "sync_"):
		return "sync"
	case strings.HasPrefix(reasonCode, "status_"):
		return "status"
	case strings.HasPrefix(reasonCode, "reward_"):
		return "reward"
	case reasonCode == ReasonColocation || reasonCode == ReasonBehaviourPenalty || reasonCode == ReasonDASBadColumnIntersection:
		return "connection"
	default:
		return "other"
	}
}

// lighthouseReasons maps Lighthouse's PeerAction tags to our controlled
// vocabulary. Anything not listed falls through to translateLighthouseReason's
// prefix heuristics.
var lighthouseReasons = map[string]string{
	"bad_gossip_block_ssz":                     ReasonGossipInvalidBlock,
	"gossip_block_low":                         ReasonGossipInvalidBlock,
	"gossip_block_mid":                         ReasonGossipInvalidBlock,
	"gossip_block_high":                        ReasonGossipInvalidBlock,
	"bad_gossip_blob_ssz":                      ReasonGossipInvalidBlobSidecar,
	"gossip_blob_low":                          ReasonGossipInvalidBlobSidecar,
	"gossip_blob_high":                         ReasonGossipInvalidBlobSidecar,
	"bad_gossip_data_column_ssz":               ReasonGossipInvalidDataColumnSidecar,
	"gossip_data_column_low":                   ReasonGossipInvalidDataColumnSidecar,
	"gossip_data_column_high":                  ReasonGossipInvalidDataColumnSidecar,
	"invalid_gossip_exit":                      ReasonGossipInvalidVoluntaryExit,
	"invalid_gossip_attester_slashing":         ReasonGossipInvalidSlashing,
	"invalid_gossip_proposer_slashing":         ReasonGossipInvalidSlashing,
	"invalid_bls_to_execution_change":          ReasonGossipInvalidBLSChange,
	"handle_rpc_error":                         ReasonRPCOther,
	"faulty_batch":                             ReasonSyncBadBatch,
	"faulty_chain":                             ReasonSyncBadBatch,
	"batch_reprocessed_too_many_times":         ReasonSyncMaxProcessingAttempt,
	"lookup_block_processing_failure":          ReasonSyncLookupFailed,
	"lookup_blobs_processing_failure":          ReasonSyncLookupFailed,
	"lookup_custody_column_processing_failure": ReasonSyncLookupFailed,
	"missing_oldest_block_root":                ReasonSyncChainInvalid,
	"goodbye_peer":                             ReasonUnknown,
}

// translateLighthouseReason maps a Lighthouse last_action.reason tag to
// our controlled vocabulary. The native string is always preserved by
// the caller in PeerScoreEvent.NativeReason.
func translateLighthouseReason(native string) string {
	if native == "" {
		return ReasonUnknown
	}
	if code, ok := lighthouseReasons[native]; ok {
		return code
	}
	switch {
	case strings.HasPrefix(native, "attn_"):
		return ReasonGossipInvalidAttestation
	case strings.HasPrefix(native, "sync_"):
		// "sync_*" tags in this enum are the gossipsub sync-committee
		// message family. Lighthouse uses a separate "faulty_*" /
		// "lookup_*" family for the sync-protocol downscores handled
		// in the explicit map above.
		return ReasonGossipInvalidSyncMessage
	case strings.HasPrefix(native, "batch_reprocessed"):
		return ReasonSyncMaxProcessingAttempt
	case strings.Contains(native, "rpc"):
		return ReasonRPCOther
	case strings.Contains(native, "gossip"):
		return ReasonGossipOther
	default:
		return ReasonUnknown
	}
}

// lodestarReasons maps Lodestar's lastActionName values to our
// controlled vocabulary. Anything not listed falls through to
// translateLodestarReason's prefix heuristics.
var lodestarReasons = map[string]string{
	"BadGossipBlock":                 ReasonGossipInvalidBlock,
	"duplicate_block":                ReasonGossipInvalidBlock,
	"BadSyncBlocks":                  ReasonSyncBadBatch,
	"SyncChainInvalidBatchSelf":      ReasonSyncBadBatch,
	"SyncChainInvalidBatchOther":     ReasonSyncBadBatch,
	"SyncChainMaxProcessingAttempts": ReasonSyncMaxProcessingAttempt,
	"BadBlockByRoot":                 ReasonRPCBadBlocksByRoot,
	"rate_limit_rpc":                 ReasonRPCRateLimited,

	"REQUEST_ERROR_INVALID_REQUEST":          ReasonRPCInvalidRequest,
	"REQUEST_ERROR_INVALID_RESPONSE_SSZ":     ReasonRPCInvalidResponseSSZ,
	"REQUEST_ERROR_SSZ_OVER_MAX_SIZE":        ReasonRPCInvalidResponseSSZ,
	"REQUEST_ERROR_RESP_TIMEOUT":             ReasonRPCResponseTimeout,
	"REQUEST_ERROR_SERVER_ERROR":             ReasonRPCServerError,
	"REQUEST_ERROR_RESP_RATE_LIMITED":        ReasonRPCRateLimited,
	"REQUEST_ERROR_ERR_UNSUPPORTED_PROTOCOL": ReasonRPCUnsupportedProtocol,
	"REQUEST_ERROR_UNKNOWN_ERROR_STATUS":     ReasonRPCOther,
	"REQUEST_ERROR_DIAL_ERROR":               ReasonRPCDialError,
	"REQUEST_ERROR_DIAL_TIMEOUT":             ReasonRPCDialError,
}

// translateLodestarReason maps a Lodestar lastActionName to our
// controlled vocabulary.
func translateLodestarReason(native string) string {
	if native == "" {
		return ReasonUnknown
	}
	if code, ok := lodestarReasons[native]; ok {
		return code
	}
	lower := strings.ToLower(native)
	switch {
	case strings.HasPrefix(lower, "rpc_") || strings.HasPrefix(lower, "rpc"):
		return ReasonRPCOther
	case strings.HasPrefix(lower, "sync_") || strings.HasPrefix(lower, "sync"):
		return ReasonSyncOther
	case strings.HasPrefix(lower, "gossip_") || strings.Contains(lower, "gossip"):
		return ReasonGossipOther
	case strings.HasPrefix(lower, "status") || strings.Contains(lower, "fork"):
		return ReasonStatusOther
	default:
		return ReasonUnknown
	}
}

// translatePrysmReason maps Prysm's last_downscore_topic + info pair
// to our controlled vocabulary. Topic is checked first (the topic
// prefix carries the strongest signal); info is checked for the
// remaining categories.
func translatePrysmReason(topic, info string) string {
	if topic != "" {
		switch {
		case strings.Contains(topic, "/beacon_block/"):
			return ReasonGossipInvalidBlock
		case strings.Contains(topic, "/beacon_attestation_"):
			return ReasonGossipInvalidAttestation
		case strings.Contains(topic, "/sync_committee_"):
			return ReasonGossipInvalidSyncMessage
		case strings.Contains(topic, "/blob_sidecar_"):
			return ReasonGossipInvalidBlobSidecar
		case strings.Contains(topic, "/data_column_sidecar_"):
			return ReasonGossipInvalidDataColumnSidecar
		case strings.Contains(topic, "/voluntary_exit"):
			return ReasonGossipInvalidVoluntaryExit
		case strings.Contains(topic, "/attester_slashing") || strings.Contains(topic, "/proposer_slashing"):
			return ReasonGossipInvalidSlashing
		case strings.Contains(topic, "/bls_to_execution_change"):
			return ReasonGossipInvalidBLSChange
		}
	}

	lower := strings.ToLower(info)
	switch {
	case lower == "":
		return ReasonUnknown
	case strings.Contains(lower, "rate limit"):
		return ReasonRPCRateLimited
	case strings.Contains(lower, "fork digest"):
		return ReasonStatusBadForkDigest
	case strings.Contains(lower, "peer behind our head"):
		return ReasonStatusLowHead
	case strings.Contains(lower, "unviable fork"):
		return ReasonStatusUnviableFork
	case strings.Contains(lower, "badvalues") || strings.Contains(lower, "badresponse"):
		return ReasonRPCInvalidResponseSSZ
	default:
		return ReasonUnknown
	}
}
